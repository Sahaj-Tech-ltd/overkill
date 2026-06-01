package cron

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type JobStore interface {
	SaveJob(ctx context.Context, job *Job) error
	LoadJobs(ctx context.Context) ([]Job, error)
	DeleteJob(ctx context.Context, id string) error
}

type Config struct {
	Timezone string
	Store    JobStore
	OnFire   func(job *Job) error
	// OnRunLog is called after each job execution to persist the run record.
	// Receives job, output text (may be empty), error, and success flag.
	// Best-effort — errors from this callback are logged but not surfaced.
	OnRunLog func(job *Job, output, errMsg string, success bool)
}

type Scheduler struct {
	mu       sync.RWMutex
	jobs     map[string]*Job
	location *time.Location
	stop     chan struct{}
	wg       sync.WaitGroup // tracks the run goroutine lifecycle (avoids Stop→Start race)
	store    JobStore
	onFire   func(job *Job) error
	onRunLog func(job *Job, output, errMsg string, success bool)
	tick     time.Duration
	running  bool // guarded by mu; prevents double-Start panics
	stopped  bool // guarded by mu; prevents double-Stop panics
	// inflight tracks job IDs currently executing in onFire so
	// tickJobs doesn't double-fire a slow callback.
	inflight map[string]bool
	// tzCache memoises time.LoadLocation results so tickJobs doesn't
	// pay the parse + zoneinfo lookup on every fire decision. The
	// tzdata file is parsed once per zone name encountered.
	tzMu    sync.RWMutex
	tzCache map[string]*time.Location
}

// loadLocation returns a cached *time.Location for name, parsing on
// miss. Empty name returns s.location (the scheduler's default).
func (s *Scheduler) loadLocation(name string) (*time.Location, error) {
	if name == "" {
		return s.location, nil
	}
	s.tzMu.RLock()
	loc, ok := s.tzCache[name]
	s.tzMu.RUnlock()
	if ok {
		return loc, nil
	}
	parsed, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	s.tzMu.Lock()
	if s.tzCache == nil {
		s.tzCache = make(map[string]*time.Location, 4)
	}
	s.tzCache[name] = parsed
	s.tzMu.Unlock()
	return parsed, nil
}

func NewScheduler(cfg Config) (*Scheduler, error) {
	loc := time.UTC
	if cfg.Timezone != "" {
		var err error
		loc, err = time.LoadLocation(cfg.Timezone)
		if err != nil {
			return nil, fmt.Errorf("cron: loading timezone %q: %w", cfg.Timezone, err)
		}
	}

	s := &Scheduler{
		jobs:     make(map[string]*Job),
		location: loc,
		stop:     make(chan struct{}),
		store:    cfg.Store,
		onFire:   cfg.OnFire,
		onRunLog: cfg.OnRunLog,
		tick:     time.Second,
		inflight: make(map[string]bool),
	}

	if s.store != nil {
		jobs, err := s.store.LoadJobs(context.Background())
		if err != nil {
			return nil, fmt.Errorf("cron: loading jobs: %w", err)
		}
		for i := range jobs {
			j := jobs[i]
			s.jobs[j.ID] = &j
		}
	}

	return s, nil
}

func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopped = false
	// Re-arm stop channel in case this is a Start-after-Stop.
	s.stop = make(chan struct{})
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run()
	}()
}

func (s *Scheduler) run() {
	tick := s.tick
	if tick <= 0 {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case now := <-ticker.C:
			s.tickJobs(now.In(s.location))
		}
	}
}

func (s *Scheduler) tickJobs(now time.Time) {
	// Snapshot the fields we need under RLock so the unlock-then-read
	// race goes away. The old loop read j.NextRun / j.Timezone after
	// releasing the lock — a concurrent SetJob/UpdateJob writer could
	// be mutating those fields, and -race flagged it.
	type jobSnap struct {
		job      *Job
		nextRun  time.Time
		timezone string
	}
	s.mu.RLock()
	snaps := make([]jobSnap, 0, len(s.jobs))
	for _, j := range s.jobs {
		if j.Status == StatusActive {
			snaps = append(snaps, jobSnap{
				job:      j,
				nextRun:  j.NextRun,
				timezone: j.Timezone,
			})
		}
	}
	s.mu.RUnlock()

	for _, sn := range snaps {
		if sn.nextRun.IsZero() {
			continue
		}

		// Inflight dedup: skip jobs already executing in a slow onFire
		// callback. Without this guard, a callback that runs longer than
		// the tick interval would be double-fired by subsequent ticks.
		s.mu.RLock()
		inFlight := s.inflight[sn.job.ID]
		s.mu.RUnlock()
		if inFlight {
			continue
		}

		loc := s.location
		if sn.timezone != "" {
			if jl, err := s.loadLocation(sn.timezone); err == nil {
				loc = jl
			}
		}
		nextInLoc := sn.nextRun.In(loc)

		// We're past the scheduled time. The previous design used a
		// tight 2-tick window: if we missed the window (long GC pause,
		// suspended host, daemon downtime), NextRun never advanced and
		// the job was silently dead forever. Now: fire once if we're
		// past due, then advance NextRun. Catch-up semantics, never
		// re-fire skipped intermediate times.
		if !now.Before(nextInLoc) {
			s.mu.Lock()
			s.inflight[sn.job.ID] = true
			s.mu.Unlock()
			go func(job *Job) {
				defer func() {
					s.mu.Lock()
					delete(s.inflight, job.ID)
					s.mu.Unlock()
				}()
				s.fireJob(job)
			}(sn.job)
		}
	}
}

func (s *Scheduler) fireJob(j *Job) {
	s.mu.Lock()
	j.RunCount++
	j.LastRun = time.Now().UTC()
	s.mu.Unlock()

	var fireErr error
	if s.onFire != nil {
		fireErr = safeFire(s.onFire, j)
	}

	if fireErr != nil {
		s.mu.Lock()
		j.FailureCount++
		if j.FailureCount > j.MaxRetries {
			j.Status = StatusFailed
		} else {
			backoff := retryBackoff(j)
			j.NextRun = time.Now().Add(backoff)
		}
		s.mu.Unlock()
	} else {
		s.mu.Lock()
		next, err := s.NextRunTime(j)
		if err != nil {
			j.Status = StatusFailed
		} else {
			j.NextRun = next
		}
		s.mu.Unlock()
	}

	// Persist run log if a logger is configured (M13: cron output persistence).
	if s.onRunLog != nil {
		errMsg := ""
		success := true
		if fireErr != nil {
			errMsg = fireErr.Error()
			success = false
		}
		s.onRunLog(j, "", errMsg, success)
	}

	// Snapshot the store ref under the lock, then release BEFORE
	// the I/O. Old code held RLock across SaveJob — a slow store
	// (network mount, fsync stall) blocked every concurrent
	// SetJob/Cancel/Status caller, and could deadlock if the store
	// implementation took any lock that re-entered the scheduler.
	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()
	if store != nil {
		_ = store.SaveJob(context.Background(), j)
	}
}

func retryBackoff(j *Job) time.Duration {
	base := j.BackoffBaseSec
	if base <= 0 {
		base = 60
	}
	max := j.BackoffMaxSec
	if max <= 0 {
		max = 300
	}
	delay := base
	for i := 1; i < j.FailureCount; i++ {
		delay *= 2
		if delay > max {
			return time.Duration(max) * time.Second
		}
	}
	return time.Duration(delay) * time.Second
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()
	close(s.stop)
	s.wg.Wait()
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

func (s *Scheduler) AddJob(job *Job) error {
	if job.Schedule == "" {
		return fmt.Errorf("cron: %w: schedule is empty", ErrInvalidJob)
	}
	if job.Command == "" {
		return fmt.Errorf("cron: %w: command is empty", ErrInvalidJob)
	}

	if _, err := ParseCron(job.Schedule); err != nil {
		return fmt.Errorf("cron: %w: %v", ErrInvalidJob, err)
	}

	if job.ID == "" {
		job.ID = uuid.New().String()
	}
	if job.Status == "" {
		job.Status = StatusActive
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if job.Metadata == nil {
		job.Metadata = make(map[string]string)
	}

	next, err := s.NextRunTime(job)
	if err != nil {
		return fmt.Errorf("cron: calculating next run: %w", err)
	}
	job.NextRun = next

	s.mu.Lock()
	if _, exists := s.jobs[job.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("cron: job %q already exists", job.ID)
	}
	s.jobs[job.ID] = job
	s.mu.Unlock()

	if s.store != nil {
		if err := s.store.SaveJob(context.Background(), job); err != nil {
			return fmt.Errorf("cron: persisting job: %w", err)
		}
	}

	return nil
}

func (s *Scheduler) RemoveJob(id string) error {
	s.mu.Lock()
	_, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return ErrJobNotFound
	}
	delete(s.jobs, id)
	s.mu.Unlock()

	if s.store != nil {
		if err := s.store.DeleteJob(context.Background(), id); err != nil {
			return fmt.Errorf("cron: deleting job: %w", err)
		}
	}

	return nil
}

func (s *Scheduler) PauseJob(id string) error {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return ErrJobNotFound
	}
	j.Status = StatusPaused
	s.mu.Unlock()

	if s.store != nil {
		return s.store.SaveJob(context.Background(), j)
	}
	return nil
}

func (s *Scheduler) ResumeJob(id string) error {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return ErrJobNotFound
	}
	j.Status = StatusActive

	next, err := s.NextRunTime(j)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("cron: calculating next run: %w", err)
	}
	j.NextRun = next
	s.mu.Unlock()

	if s.store != nil {
		return s.store.SaveJob(context.Background(), j)
	}
	return nil
}

// GetJob returns a defensive copy of the job so the caller cannot
// observe torn writes from fireJob mutating NextRun / RunCount /
// LastRun under the scheduler's lock. Returns (nil, false) when
// the id is unknown.
func (s *Scheduler) GetJob(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	dup := *j
	return &dup, true
}

// ListJobs returns defensive copies of every job. See GetJob for
// the race rationale — the live *Job pointers are mutated by
// fireJob under the scheduler's lock; returning the pointers
// directly lets external readers race against those writes on
// multi-word fields like time.Time.
func (s *Scheduler) ListJobs() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		dup := *j
		result = append(result, &dup)
	}
	return result
}

func (s *Scheduler) NextRunTime(job *Job) (time.Time, error) {
	expr, err := ParseCron(job.Schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("cron: parsing schedule: %w", err)
	}

	loc := s.location
	if job.Timezone != "" {
		loc, err = s.loadLocation(job.Timezone)
		if err != nil {
			return time.Time{}, fmt.Errorf("cron: loading timezone: %w", err)
		}
	}

	now := time.Now().In(loc)
	return expr.Next(now), nil
}

// safeFire wraps the user-supplied onFire callback with panic recovery.
// Without this, a panic in the callback would crash the tick goroutine
// and prevent done from closing, causing Stop() to deadlock forever.
func safeFire(fn func(job *Job) error, job *Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("cron: onFire panicked: %v", r)
		}
	}()
	return fn(job)
}

func jobKey(id string) []byte {
	return []byte("cron:job:" + id)
}
