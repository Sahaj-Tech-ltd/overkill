package automation

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"
)

func openAutomationDB(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = os.Getenv("PG_TEST_URL")
	}
	if connStr == "" {
		connStr = "postgres://postgres:***@localhost:5432/overkill_test?sslmode=disable"
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Skipf("skipping: cannot open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("skipping: cannot ping postgres: %v (set PG_TEST_URL or DATABASE_URL)", err)
	}
	// Create tables needed for automation tests.
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS automation_sops (
		id TEXT PRIMARY KEY, name TEXT, mode TEXT, steps JSONB, status TEXT,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS automation_routines (
		id TEXT PRIMARY KEY, name TEXT, trigger TEXT, action TEXT,
		cooldown_ns BIGINT, enabled BOOLEAN, fire_count INTEGER,
		last_fired TIMESTAMPTZ
	)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS automation_alarms (
		id TEXT PRIMARY KEY, name TEXT, action TEXT, schedule TEXT,
		enabled BOOLEAN, created_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	t.Cleanup(func() {
		db.Exec("DELETE FROM automation_sops")
		db.Exec("DELETE FROM automation_routines")
		db.Exec("DELETE FROM automation_alarms")
		db.Close()
	})
	return db
}

func newTestSOP(id string, steps []Step, mode SOPMode) *SOP {
	return &SOP{
		ID:       id,
		Name:     "test-" + id,
		Mode:     mode,
		Steps:    steps,
		Metadata: make(map[string]string),
	}
}

func newTestStep(id, action string, approval bool) Step {
	return Step{
		ID:               id,
		Name:             "step-" + id,
		Action:           action,
		Status:           StepPending,
		RequiresApproval: approval,
	}
}

func TestSOPEngine_Create(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})

	sop := newTestSOP("sop-1", []Step{
		newTestStep("step-1", "echo hello", false),
		newTestStep("step-2", "echo world", false),
	}, ModeAuto)

	err := engine.Create(sop)
	require.NoError(t, err)

	got, ok := engine.Get("sop-1")
	require.True(t, ok)
	assert.Equal(t, "sop-1", got.ID)
	assert.Equal(t, SOPStatusActive, got.Status)
	assert.Len(t, got.Steps, 2)
	assert.Equal(t, StepPending, got.Steps[0].Status)
}

func TestSOPEngine_Execute_Auto(t *testing.T) {
	var executed []string
	var mu sync.Mutex
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		mu.Lock()
		executed = append(executed, action)
		mu.Unlock()
		return "output: " + action, nil
	})

	sop := newTestSOP("sop-auto", []Step{
		newTestStep("s1", "echo hello", false),
		newTestStep("s2", "echo world", false),
	}, ModeAuto)

	require.NoError(t, engine.Create(sop))
	require.NoError(t, engine.Execute(context.Background(), "sop-auto"))

	got, _ := engine.Get("sop-auto")
	assert.Equal(t, SOPStatusCompleted, got.Status)
	assert.Equal(t, StepDone, got.Steps[0].Status)
	assert.Equal(t, StepDone, got.Steps[1].Status)
	assert.Equal(t, "output: echo hello", got.Steps[0].Output)
	assert.Equal(t, "output: echo world", got.Steps[1].Output)

	mu.Lock()
	assert.Equal(t, []string{"echo hello", "echo world"}, executed)
	mu.Unlock()
}

func TestSOPEngine_Execute_Supervised(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})

	sop := newTestSOP("sop-sup", []Step{
		newTestStep("s1", "echo first", false),
		newTestStep("s2", "echo second", true),
		newTestStep("s3", "echo third", false),
	}, ModeSupervised)

	require.NoError(t, engine.Create(sop))

	err := engine.Execute(context.Background(), "sop-sup")
	assert.ErrorIs(t, err, ErrStepWaiting)

	got, _ := engine.Get("sop-sup")
	assert.Equal(t, SOPStatusActive, got.Status)
	assert.Equal(t, StepDone, got.Steps[0].Status)
	assert.Equal(t, StepWaiting, got.Steps[1].Status)
	assert.Equal(t, StepPending, got.Steps[2].Status)
}

func TestSOPEngine_ApproveStep(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok: " + action, nil
	})

	sop := newTestSOP("sop-approve", []Step{
		newTestStep("s1", "echo first", true),
		newTestStep("s2", "echo second", false),
	}, ModeSupervised)

	require.NoError(t, engine.Create(sop))

	err := engine.Execute(context.Background(), "sop-approve")
	assert.ErrorIs(t, err, ErrStepWaiting)

	got, _ := engine.Get("sop-approve")
	assert.Equal(t, StepWaiting, got.Steps[0].Status)

	require.NoError(t, engine.ApproveStep("sop-approve", "s1"))
	require.NoError(t, engine.Execute(context.Background(), "sop-approve"))

	got, _ = engine.Get("sop-approve")
	assert.Equal(t, SOPStatusCompleted, got.Status)
	assert.Equal(t, StepDone, got.Steps[0].Status)
	assert.Equal(t, StepDone, got.Steps[1].Status)
}

func TestSOPEngine_Execute_FailedStep(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "", assert.AnError
	})

	sop := newTestSOP("sop-fail", []Step{
		newTestStep("s1", "will-fail", false),
		newTestStep("s2", "never-reached", false),
	}, ModeAuto)

	require.NoError(t, engine.Create(sop))

	err := engine.Execute(context.Background(), "sop-fail")
	assert.Error(t, err)

	got, _ := engine.Get("sop-fail")
	assert.Equal(t, SOPStatusFailed, got.Status)
	assert.Equal(t, StepFailed, got.Steps[0].Status)
	assert.NotEmpty(t, got.Steps[0].Error)
	assert.Equal(t, StepPending, got.Steps[1].Status)
}

func TestSOPEngine_Cancel(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})

	sop := newTestSOP("sop-cancel", []Step{
		newTestStep("s1", "echo hi", false),
	}, ModeAuto)

	require.NoError(t, engine.Create(sop))
	require.NoError(t, engine.Cancel("sop-cancel"))

	got, _ := engine.Get("sop-cancel")
	assert.Equal(t, SOPStatusCancelled, got.Status)
}

func TestSOPEngine_PauseResume(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})

	sop := newTestSOP("sop-pause", []Step{
		newTestStep("s1", "echo hi", false),
	}, ModeAuto)

	require.NoError(t, engine.Create(sop))
	require.NoError(t, engine.Pause("sop-pause"))

	got, _ := engine.Get("sop-pause")
	assert.Equal(t, SOPStatusPaused, got.Status)

	err := engine.Execute(context.Background(), "sop-pause")
	assert.ErrorIs(t, err, ErrInvalidState)

	require.NoError(t, engine.Resume(context.Background(), "sop-pause"))

	got, _ = engine.Get("sop-pause")
	assert.Equal(t, SOPStatusCompleted, got.Status)
}

func TestSOPEngine_Deterministic(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		mu.Lock()
		calls = append(calls, action)
		mu.Unlock()
		return "transformed: " + action, nil
	})

	sop := newTestSOP("sop-det", []Step{
		newTestStep("s1", "initial-data", false),
		newTestStep("s2", "unused-action", false),
	}, ModeDeterministic)

	require.NoError(t, engine.Create(sop))
	require.NoError(t, engine.Execute(context.Background(), "sop-det"))

	got, _ := engine.Get("sop-det")
	assert.Equal(t, SOPStatusCompleted, got.Status)
	assert.Equal(t, "transformed: initial-data", got.Steps[0].Output)
	assert.Equal(t, "transformed: transformed: initial-data", got.Steps[1].Output)

	mu.Lock()
	assert.Equal(t, "initial-data", calls[0])
	assert.Equal(t, "transformed: initial-data", calls[1])
	mu.Unlock()
}

func TestSOPEngine_ContextCancelled(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "ok", nil
	})

	sop := newTestSOP("sop-ctx", []Step{
		newTestStep("s1", "first", false),
		newTestStep("s2", "second", false),
		newTestStep("s3", "third", false),
	}, ModeAuto)

	require.NoError(t, engine.Create(sop))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := engine.Execute(ctx, "sop-ctx")
	assert.Error(t, err)

	got, _ := engine.Get("sop-ctx")
	assert.Equal(t, SOPStatusFailed, got.Status)
}

func TestRoutineEngine_Register(t *testing.T) {
	engine := NewRoutineEngine(func(action string) (string, error) {
		return "ok", nil
	})

	r := &Routine{
		ID:      "r1",
		Name:    "test routine",
		Trigger: "file.changed",
		Action:  "run tests",
		Enabled: true,
	}

	require.NoError(t, engine.Register(r))

	list := engine.List()
	assert.Len(t, list, 1)
	assert.Equal(t, "r1", list[0].ID)

	err := engine.Register(r)
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

func TestRoutineEngine_HandleEvent(t *testing.T) {
	var fired atomic.Int32
	engine := NewRoutineEngine(func(action string) (string, error) {
		fired.Add(1)
		return "ok", nil
	})

	require.NoError(t, engine.Register(&Routine{
		ID:      "r1",
		Trigger: "file.changed",
		Action:  "run tests",
		Enabled: true,
	}))
	require.NoError(t, engine.Register(&Routine{
		ID:      "r2",
		Trigger: "other.event",
		Action:  "do something",
		Enabled: true,
	}))

	didFire, err := engine.HandleEvent("file.changed")
	require.NoError(t, err)
	assert.True(t, didFire)
	assert.Equal(t, int32(1), fired.Load())
}

func TestRoutineEngine_Cooldown(t *testing.T) {
	var fired atomic.Int32
	engine := NewRoutineEngine(func(action string) (string, error) {
		fired.Add(1)
		return "ok", nil
	})

	require.NoError(t, engine.Register(&Routine{
		ID:       "r1",
		Trigger:  "file.changed",
		Action:   "run tests",
		Enabled:  true,
		Cooldown: 1 * time.Hour,
	}))

	didFire, err := engine.HandleEvent("file.changed")
	require.NoError(t, err)
	assert.True(t, didFire)
	assert.Equal(t, int32(1), fired.Load())

	didFire, err = engine.HandleEvent("file.changed")
	require.NoError(t, err)
	assert.False(t, didFire)
	assert.Equal(t, int32(1), fired.Load())
}

func TestRoutineEngine_NoMatch(t *testing.T) {
	engine := NewRoutineEngine(func(action string) (string, error) {
		return "ok", nil
	})

	require.NoError(t, engine.Register(&Routine{
		ID:      "r1",
		Trigger: "file.changed",
		Action:  "run tests",
		Enabled: true,
	}))

	didFire, err := engine.HandleEvent("unknown.event")
	require.NoError(t, err)
	assert.False(t, didFire)
}

func TestRoutineEngine_EnableDisable(t *testing.T) {
	engine := NewRoutineEngine(func(action string) (string, error) {
		return "ok", nil
	})

	require.NoError(t, engine.Register(&Routine{
		ID:      "r1",
		Trigger: "file.changed",
		Action:  "run tests",
		Enabled: true,
	}))

	require.NoError(t, engine.Disable("r1"))

	didFire, err := engine.HandleEvent("file.changed")
	require.NoError(t, err)
	assert.False(t, didFire)

	require.NoError(t, engine.Enable("r1"))

	didFire, err = engine.HandleEvent("file.changed")
	require.NoError(t, err)
	assert.True(t, didFire)

	err = engine.Disable("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound)

	err = engine.Enable("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestAlarmClock_SetAndFire(t *testing.T) {
	var fired atomic.Int32
	clock := NewAlarmClock(func(alarm *Alarm) error {
		fired.Add(1)
		return nil
	})

	alarm := &Alarm{
		ID:     "a1",
		Name:   "test alarm",
		FireAt: time.Now().Add(2 * time.Second),
		Action: "notify user",
	}

	require.NoError(t, clock.Set(alarm))
	clock.Start()
	defer clock.Stop()

	time.Sleep(3 * time.Second)
	assert.Equal(t, int32(1), fired.Load())

	got := clock.List()
	assert.Len(t, got, 1)
	assert.True(t, got[0].Fired)
}

func TestAlarmClock_Cancel(t *testing.T) {
	var fired atomic.Int32
	clock := NewAlarmClock(func(alarm *Alarm) error {
		fired.Add(1)
		return nil
	})

	alarm := &Alarm{
		ID:     "a2",
		Name:   "cancel test",
		FireAt: time.Now().Add(2 * time.Second),
		Action: "should not fire",
	}

	require.NoError(t, clock.Set(alarm))
	assert.True(t, clock.Cancel("a2"))
	assert.False(t, clock.Cancel("nonexistent"))

	clock.Start()
	defer clock.Stop()

	time.Sleep(3 * time.Second)
	assert.Equal(t, int32(0), fired.Load())
}

func TestAlarmClock_Pending(t *testing.T) {
	clock := NewAlarmClock(func(alarm *Alarm) error {
		return nil
	})

	now := time.Now()
	require.NoError(t, clock.Set(&Alarm{
		ID:     "a1",
		FireAt: now.Add(10 * time.Second),
	}))
	require.NoError(t, clock.Set(&Alarm{
		ID:     "a2",
		FireAt: now.Add(5 * time.Second),
	}))
	require.NoError(t, clock.Set(&Alarm{
		ID:        "a3",
		FireAt:    now.Add(15 * time.Second),
		Cancelled: true,
	}))
	require.NoError(t, clock.Set(&Alarm{
		ID:     "a4",
		FireAt: now.Add(1 * time.Second),
		Fired:  true,
	}))

	pending := clock.Pending()
	require.Len(t, pending, 2)
	assert.Equal(t, "a2", pending[0].ID)
	assert.Equal(t, "a1", pending[1].ID)
}

func TestPostgresSOPStore_SaveLoad(t *testing.T) {
	db := openAutomationDB(t)

	store, err := NewPostgresSOPStore(db)
	require.NoError(t, err)

	sop := &SOP{
		ID:     "sop-1",
		Name:   "test SOP",
		Mode:   ModeAuto,
		Steps:  []Step{newTestStep("s1", "echo hello", false)},
		Status: SOPStatusActive,
	}

	require.NoError(t, store.SaveSOP(sop))

	sops, err := store.LoadSOPs()
	require.NoError(t, err)
	require.Len(t, sops, 1)
	assert.Equal(t, "sop-1", sops[0].ID)
	assert.Equal(t, "test SOP", sops[0].Name)
	assert.Len(t, sops[0].Steps, 1)
}

func TestPostgresSOPStore_Delete(t *testing.T) {
	db := openAutomationDB(t)

	store, err := NewPostgresSOPStore(db)
	require.NoError(t, err)

	sop := &SOP{ID: "sop-del", Name: "delete me", Status: SOPStatusActive}
	require.NoError(t, store.SaveSOP(sop))

	require.NoError(t, store.DeleteSOP("sop-del"))

	sops, err := store.LoadSOPs()
	require.NoError(t, err)
	assert.Empty(t, sops)

	err = store.DeleteSOP("nonexistent")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSOPEngine_ConcurrentAccess(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})

	for i := 0; i < 10; i++ {
		sop := newTestSOP(
			"concurrent-"+string(rune('A'+i)),
			[]Step{newTestStep("s1", "action", false)},
			ModeAuto,
		)
		require.NoError(t, engine.Create(sop))
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := "concurrent-" + string(rune('A'+idx%10))
			if idx%3 == 0 {
				_ = engine.Execute(context.Background(), id)
			} else if idx%3 == 1 {
				engine.Get(id)
			} else {
				engine.List()
			}
		}(i)
	}
	wg.Wait()

	list := engine.List()
	assert.Len(t, list, 10)
}
