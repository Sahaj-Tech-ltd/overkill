package cron

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type JobStore interface {
	SaveJob(job *Job) error
	LoadJobs() ([]Job, error)
	DeleteJob(id string) error
}

type Config struct {
	Timezone string
	Store    JobStore
	OnFire   func(job *Job) error
}

type Scheduler struct {
	mu       sync.RWMutex
	jobs     map[string]*Job
	location *time.Location
	stop     chan struct{}
	done     chan struct{}
	store    JobStore
	onFire   func(job *Job) error
	tick     time.Duration
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
		done:     make(chan struct{}),
		store:    cfg.Store,
		onFire:   cfg.OnFire,
		tick:     time.Second,
	}

	if s.store != nil {
		jobs, err := s.store.LoadJobs()
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
	go s.run()
}

func (s *Scheduler) run() {
	defer close(s.done)

	ticker := time.NewTicker(s.tick)
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

		loc := s.location
		if sn.timezone != "" {
			jl, err := time.LoadLocation(sn.timezone)
			if err == nil {
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
			s.fireJob(sn.job)
		}
	}
}

func (s *Scheduler) fireJob(j *Job) {
	s.mu.Lock()
	j.RunCount++
	j.LastRun = time.Now().UTC()
	s.mu.Unlock()

	if s.onFire != nil {
		if err := s.onFire(j); err != nil {
			s.mu.Lock()
			j.FailureCount++
			if j.FailureCount > j.MaxRetries {
				j.Status = StatusFailed
			} else {
				backoff := retryBackoff(j.FailureCount)
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
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.store != nil {
		_ = s.store.SaveJob(j)
	}
}

func retryBackoff(failures int) time.Duration {
	switch failures {
	case 1:
		return 60 * time.Second
	case 2:
		return 120 * time.Second
	default:
		return 300 * time.Second
	}
}

func (s *Scheduler) Stop() {
	close(s.stop)
	<-s.done
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
	s.jobs[job.ID] = job
	s.mu.Unlock()

	if s.store != nil {
		if err := s.store.SaveJob(job); err != nil {
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
		if err := s.store.DeleteJob(id); err != nil {
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
		return s.store.SaveJob(j)
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
		return s.store.SaveJob(j)
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
		loc, err = time.LoadLocation(job.Timezone)
		if err != nil {
			return time.Time{}, fmt.Errorf("cron: loading timezone: %w", err)
		}
	}

	now := time.Now().In(loc)
	return expr.Next(now), nil
}

func jobKey(id string) []byte {
	return []byte("cron:job:" + id)
}
