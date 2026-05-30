package cron

import (
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestPG(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	connStr := os.Getenv("PG_TEST_URL")
	if connStr == "" {
		connStr = os.Getenv("DATABASE_URL")
	}
	if connStr == "" {
		t.Skip("skipping: set PG_TEST_URL or DATABASE_URL for postgres tests")
	}
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	return db, func() { db.Close() }
}

func TestParseCron_EveryMinute(t *testing.T) {
	expr, err := ParseCron("* * * * *")
	require.NoError(t, err)
	require.NotNil(t, expr)

	for i := 0; i <= 59; i++ {
		assert.True(t, expr.Minute.matches(i), "minute %d should match", i)
		assert.True(t, expr.Hour.matches(i%24), "hour %d should match", i%24)
	}
}

func TestParseCron_SpecificMinute(t *testing.T) {
	expr, err := ParseCron("30 * * * *")
	require.NoError(t, err)

	assert.True(t, expr.Minute.matches(30))
	assert.False(t, expr.Minute.matches(29))
	assert.False(t, expr.Minute.matches(31))
}

func TestParseCron_HourRange(t *testing.T) {
	expr, err := ParseCron("* 9-17 * * *")
	require.NoError(t, err)

	for i := 9; i <= 17; i++ {
		assert.True(t, expr.Hour.matches(i), "hour %d should match", i)
	}
	assert.False(t, expr.Hour.matches(8))
	assert.False(t, expr.Hour.matches(18))
}

func TestParseCron_Step(t *testing.T) {
	expr, err := ParseCron("*/5 * * * *")
	require.NoError(t, err)

	for i := 0; i <= 59; i++ {
		if i%5 == 0 {
			assert.True(t, expr.Minute.matches(i), "minute %d should match", i)
		} else {
			assert.False(t, expr.Minute.matches(i), "minute %d should not match", i)
		}
	}
}

func TestParseCron_List(t *testing.T) {
	expr, err := ParseCron("0 1,13 * * *")
	require.NoError(t, err)

	assert.True(t, expr.Hour.matches(1))
	assert.True(t, expr.Hour.matches(13))
	assert.False(t, expr.Hour.matches(0))
	assert.False(t, expr.Hour.matches(12))
	assert.True(t, expr.Minute.matches(0))
}

func TestParseCron_Invalid(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"too few fields", "* * *"},
		{"too many fields", "* * * * * *"},
		{"empty string", ""},
		{"invalid minute", "60 * * * *"},
		{"invalid hour", "* 24 * * *"},
		{"invalid range", "5-1 * * * *"},
		{"invalid value", "abc * * * *"},
		{"zero step", "*/0 * * * *"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCron(tt.expr)
			assert.Error(t, err)
		})
	}
}

func TestCronExpr_Next_EveryMinute(t *testing.T) {
	expr, err := ParseCron("* * * * *")
	require.NoError(t, err)

	now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	next := expr.Next(now)

	expected := time.Date(2025, 6, 15, 10, 31, 0, 0, time.UTC)
	assert.Equal(t, expected, next)
}

func TestCronExpr_Next_SpecificHour(t *testing.T) {
	expr, err := ParseCron("0 9 * * *")
	require.NoError(t, err)

	now := time.Date(2025, 6, 15, 14, 0, 0, 0, time.UTC)
	next := expr.Next(now)

	expected := time.Date(2025, 6, 16, 9, 0, 0, 0, time.UTC)
	assert.Equal(t, expected, next)
}

func TestCronExpr_Next_Weekday(t *testing.T) {
	expr, err := ParseCron("0 9 * * 1")
	require.NoError(t, err)

	thursday := time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC)
	next := expr.Next(thursday)

	assert.Equal(t, time.Monday, next.Weekday())
	assert.True(t, next.After(thursday))
}

func TestScheduler_AddJob(t *testing.T) {
	s, err := NewScheduler(Config{})
	require.NoError(t, err)

	job := &Job{
		Name:     "test",
		Schedule: "*/5 * * * *",
		Command:  "echo hello",
	}
	err = s.AddJob(job)
	require.NoError(t, err)

	assert.NotEmpty(t, job.ID)
	assert.Equal(t, StatusActive, job.Status)
	assert.False(t, job.NextRun.IsZero())

	jobs := s.ListJobs()
	assert.Len(t, jobs, 1)
	assert.Equal(t, job.ID, jobs[0].ID)
}

func TestScheduler_RemoveJob(t *testing.T) {
	s, err := NewScheduler(Config{})
	require.NoError(t, err)

	job := &Job{
		Name:     "test",
		Schedule: "* * * * *",
		Command:  "echo hello",
	}
	require.NoError(t, s.AddJob(job))

	err = s.RemoveJob(job.ID)
	require.NoError(t, err)

	jobs := s.ListJobs()
	assert.Empty(t, jobs)
}

func TestScheduler_RemoveJob_NotFound(t *testing.T) {
	s, err := NewScheduler(Config{})
	require.NoError(t, err)

	err = s.RemoveJob("nonexistent")
	assert.ErrorIs(t, err, ErrJobNotFound)
}

func TestScheduler_PauseResume(t *testing.T) {
	s, err := NewScheduler(Config{})
	require.NoError(t, err)

	job := &Job{
		Name:     "test",
		Schedule: "* * * * *",
		Command:  "echo hello",
	}
	require.NoError(t, s.AddJob(job))

	err = s.PauseJob(job.ID)
	require.NoError(t, err)

	j, ok := s.GetJob(job.ID)
	require.True(t, ok)
	assert.Equal(t, StatusPaused, j.Status)

	err = s.ResumeJob(job.ID)
	require.NoError(t, err)

	j, ok = s.GetJob(job.ID)
	require.True(t, ok)
	assert.Equal(t, StatusActive, j.Status)
	assert.False(t, j.NextRun.IsZero())
}

func TestScheduler_NextRunTime(t *testing.T) {
	s, err := NewScheduler(Config{Timezone: "UTC"})
	require.NoError(t, err)

	job := &Job{
		Name:     "every hour",
		Schedule: "0 * * * *",
		Command:  "echo hello",
	}

	next, err := s.NextRunTime(job)
	require.NoError(t, err)
	assert.False(t, next.IsZero())
	assert.Equal(t, 0, next.Minute())
	assert.True(t, next.After(time.Now().Add(-time.Second)))
}

func TestScheduler_Fire(t *testing.T) {
	var fired []*Job
	var mu sync.Mutex

	s, err := NewScheduler(Config{
		OnFire: func(job *Job) error {
			mu.Lock()
			fired = append(fired, job)
			mu.Unlock()
			return nil
		},
	})
	require.NoError(t, err)
	s.tick = 10 * time.Millisecond

	now := time.Now().UTC()
	next := now.Add(50 * time.Millisecond).Truncate(10 * time.Millisecond)

	job := &Job{
		Name:     "fire-test",
		Schedule: "* * * * *",
		Command:  "echo hello",
	}
	require.NoError(t, s.AddJob(job))

	s.mu.Lock()
	job.NextRun = next
	s.mu.Unlock()

	s.Start()
	defer s.Stop()

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count := len(fired)
	mu.Unlock()
	assert.Equal(t, 1, count)
}

func TestScheduler_RetryOnFailure(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	s, err := NewScheduler(Config{
		OnFire: func(job *Job) error {
			mu.Lock()
			callCount++
			mu.Unlock()
			return fmt.Errorf("fail")
		},
	})
	require.NoError(t, err)

	job := &Job{
		Name:         "retry-test",
		Schedule:     "* * * * *",
		Command:      "echo hello",
		MaxRetries:   2,
		FailureCount: 0,
	}
	require.NoError(t, s.AddJob(job))

	now := time.Now().UTC()
	s.mu.Lock()
	job.NextRun = now.Add(-time.Second)
	s.mu.Unlock()

	s.fireJob(job)

	s.mu.RLock()
	fc := job.FailureCount
	st := job.Status
	nextRun := job.NextRun
	s.mu.RUnlock()

	assert.Equal(t, 1, fc)
	assert.Equal(t, StatusActive, st)
	assert.True(t, nextRun.After(now))

	for i := 0; i < 3; i++ {
		s.fireJob(job)
	}

	s.mu.RLock()
	fc = job.FailureCount
	st = job.Status
	s.mu.RUnlock()

	assert.Equal(t, StatusFailed, st)
	assert.True(t, fc > 2)
}

func TestPostgresJobStore_SaveLoad(t *testing.T) {
	db, cleanup := openTestPG(t)
	defer cleanup()

	store, err := NewPostgresJobStore(db)
	require.NoError(t, err)

	job := &Job{
		ID:        "test-job-1",
		Name:      "test",
		Schedule:  "*/5 * * * *",
		Command:   "echo hello",
		Status:    StatusActive,
		CreatedAt: time.Now().UTC(),
		NextRun:   time.Now().Add(5 * time.Minute).UTC(),
	}

	require.NoError(t, store.SaveJob(job))

	jobs, err := store.LoadJobs()
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, job.ID, jobs[0].ID)
	assert.Equal(t, job.Name, jobs[0].Name)
	assert.Equal(t, job.Schedule, jobs[0].Schedule)
	assert.Equal(t, job.Command, jobs[0].Command)
}

func TestPostgresJobStore_Delete(t *testing.T) {
	db, cleanup := openTestPG(t)
	defer cleanup()

	store, err := NewPostgresJobStore(db)
	require.NoError(t, err)

	job := &Job{
		ID:       "delete-me",
		Name:     "test",
		Schedule: "* * * * *",
		Command:  "echo bye",
	}
	require.NoError(t, store.SaveJob(job))

	require.NoError(t, store.DeleteJob(job.ID))

	jobs, err := store.LoadJobs()
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

func TestPostgresJobStore_LoadEmpty(t *testing.T) {
	db, cleanup := openTestPG(t)
	defer cleanup()

	store, err := NewPostgresJobStore(db)
	require.NoError(t, err)

	jobs, err := store.LoadJobs()
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

func TestScheduler_Timezone(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	s, err := NewScheduler(Config{Timezone: "America/New_York"})
	require.NoError(t, err)

	job := &Job{
		Name:     "9am est",
		Schedule: "0 9 * * *",
		Timezone: "America/New_York",
		Command:  "echo morning",
	}

	next, err := s.NextRunTime(job)
	require.NoError(t, err)
	assert.False(t, next.IsZero())

	nextInLoc := next.In(loc)
	assert.Equal(t, 9, nextInLoc.Hour())
	assert.Equal(t, 0, nextInLoc.Minute())
}

func TestScheduler_ConcurrentAccess(t *testing.T) {
	s, err := NewScheduler(Config{})
	require.NoError(t, err)

	var wg sync.WaitGroup
	const goroutines = 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			job := &Job{
				Name:     fmt.Sprintf("job-%d", idx),
				Schedule: "* * * * *",
				Command:  fmt.Sprintf("echo %d", idx),
			}
			if err := s.AddJob(job); err != nil {
				t.Errorf("AddJob failed: %v", err)
				return
			}
		}(i)
	}
	wg.Wait()

	jobs := s.ListJobs()
	assert.Len(t, jobs, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs := s.ListJobs()
			if len(jobs) != goroutines {
				t.Errorf("expected %d jobs, got %d", goroutines, len(jobs))
			}
		}()
	}
	wg.Wait()

	allJobs := s.ListJobs()
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if err := s.RemoveJob(allJobs[idx].ID); err != nil {
				t.Errorf("RemoveJob failed: %v", err)
			}
		}(i)
	}
	wg.Wait()

	assert.Empty(t, s.ListJobs())
}

func TestScheduler_AddJob_Invalid(t *testing.T) {
	s, err := NewScheduler(Config{})
	require.NoError(t, err)

	t.Run("empty schedule", func(t *testing.T) {
		job := &Job{Command: "echo hi"}
		err := s.AddJob(job)
		assert.ErrorIs(t, err, ErrInvalidJob)
	})

	t.Run("empty command", func(t *testing.T) {
		job := &Job{Schedule: "* * * * *"}
		err := s.AddJob(job)
		assert.ErrorIs(t, err, ErrInvalidJob)
	})

	t.Run("bad cron expression", func(t *testing.T) {
		job := &Job{Schedule: "not a cron", Command: "echo hi"}
		err := s.AddJob(job)
		assert.ErrorIs(t, err, ErrInvalidJob)
	})
}
