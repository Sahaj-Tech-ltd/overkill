package automation

import (
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captureStderr swaps the alarm stderr sink to a buffer for the test.
// Used to assert non-fatal warning paths fire (store errors etc.).
func captureStderr(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	SetAlarmStderrSink(buf)
	t.Cleanup(func() { SetAlarmStderrSink(nil) })
	return buf
}

func TestAlarmClock_SetPersists(t *testing.T) {
	store := NewMemoryAlarmStore()
	clock := NewAlarmClockWithStore(func(*Alarm) error { return nil }, store)

	a := &Alarm{ID: "a1", Name: "test", FireAt: time.Now().Add(time.Hour), Prompt: "hi"}
	if err := clock.Set(a); err != nil {
		t.Fatalf("set: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].ID != "a1" {
		t.Fatalf("alarm not persisted: %+v", loaded)
	}
}

func TestAlarmClock_SetRollsBackOnPersistFailure(t *testing.T) {
	failingStore := &failOnSaveStore{err: errors.New("disk full")}
	clock := NewAlarmClockWithStore(func(*Alarm) error { return nil }, failingStore)

	a := &Alarm{ID: "a1", FireAt: time.Now().Add(time.Hour), Prompt: "hi"}
	if err := clock.Set(a); err == nil {
		t.Fatal("expected persist failure to surface")
	}
	// In-memory state must NOT have the alarm — caller sees one
	// consistent failure path, not "Set returned an error but the
	// alarm still ticks".
	if len(clock.List()) != 0 {
		t.Errorf("rollback failed: %d alarms still in memory", len(clock.List()))
	}
}

func TestAlarmClock_CancelPersists(t *testing.T) {
	store := NewMemoryAlarmStore()
	clock := NewAlarmClockWithStore(func(*Alarm) error { return nil }, store)

	a := &Alarm{ID: "a1", FireAt: time.Now().Add(time.Hour), Prompt: "x"}
	if err := clock.Set(a); err != nil {
		t.Fatal(err)
	}
	if !clock.Cancel("a1") {
		t.Fatal("cancel returned false")
	}
	loaded, _ := store.Load()
	if !loaded[0].Cancelled {
		t.Errorf("cancellation not persisted: %+v", loaded[0])
	}
}

func TestAlarmClock_CancelIsIdempotent(t *testing.T) {
	store := NewMemoryAlarmStore()
	clock := NewAlarmClockWithStore(func(*Alarm) error { return nil }, store)
	_ = clock.Set(&Alarm{ID: "x", FireAt: time.Now().Add(time.Hour), Prompt: "p"})

	if !clock.Cancel("x") {
		t.Fatal("first cancel should return true")
	}
	if !clock.Cancel("x") {
		t.Error("second cancel should also return true (idempotent), got false")
	}
}

func TestAlarmClock_ReloadResumesPendingAlarms(t *testing.T) {
	store := NewMemoryAlarmStore()
	// Seed the store directly — simulates a prior daemon process
	// that set alarms and exited.
	_ = store.Save(&Alarm{ID: "pending", FireAt: time.Now().Add(time.Hour), Prompt: "future"})
	_ = store.Save(&Alarm{ID: "ancient-done", FireAt: time.Now().Add(-25 * time.Hour), Prompt: "old", Fired: true})

	clock := NewAlarmClockWithStore(func(*Alarm) error { return nil }, store)
	if err := clock.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	all := clock.List()
	if len(all) != 1 {
		t.Errorf("ancient terminal alarm should have been pruned, got %d: %+v", len(all), all)
	}
	if all[0].ID != "pending" {
		t.Errorf("wrong alarm preserved: %+v", all[0])
	}
}

func TestAlarmClock_FirePersistsResult(t *testing.T) {
	store := NewMemoryAlarmStore()
	clock := NewAlarmClockWithStore(func(a *Alarm) error {
		a.Result = "all good"
		return nil
	}, store)

	// Use a fake clock so we can guarantee fire-on-next-tick.
	fakeNow := atomic.Pointer[time.Time]{}
	now0 := time.Now()
	fakeNow.Store(&now0)
	clock.now = func() time.Time { return *fakeNow.Load() }

	id := "a1"
	if err := clock.Set(&Alarm{ID: id, FireAt: now0.Add(-1 * time.Second), Prompt: "x"}); err != nil {
		t.Fatal(err)
	}

	clock.checkAlarms()

	loaded, _ := store.Load()
	if len(loaded) != 1 {
		t.Fatalf("want 1 stored alarm, got %d", len(loaded))
	}
	if !loaded[0].Fired {
		t.Error("Fired flag not persisted")
	}
	if loaded[0].Result != "all good" {
		t.Errorf("Result not persisted: %q", loaded[0].Result)
	}
	if loaded[0].FiredAt.IsZero() {
		t.Error("FiredAt not set")
	}
}

func TestAlarmClock_FireFailureRecordsResult(t *testing.T) {
	store := NewMemoryAlarmStore()
	clock := NewAlarmClockWithStore(func(a *Alarm) error {
		return errors.New("boom")
	}, store)

	now0 := time.Now()
	clock.now = func() time.Time { return now0 }

	_ = clock.Set(&Alarm{ID: "x", FireAt: now0.Add(-time.Second), Prompt: "p"})

	// Post-BUG-39 semantics: a failing callback no longer marks Fired
	// immediately — it bumps Attempts, records the error, and reschedules
	// with linear backoff. After maxAlarmAttempts (3) consecutive
	// failures the alarm gives up and finally gets Fired=true with the
	// last error in Result.
	for i := 0; i < 3; i++ {
		// Advance the clock past the backoff so the next checkAlarms
		// re-picks the alarm.
		clock.now = func() time.Time { return now0.Add(time.Duration(i+1) * 5 * time.Minute) }
		clock.checkAlarms()
	}

	loaded, _ := store.Load()
	if !loaded[0].Fired {
		t.Error("Fired should be set after max retries exhausted")
	}
	if loaded[0].Result == "" || loaded[0].Result == "all good" {
		t.Errorf("failure message not recorded: %q", loaded[0].Result)
	}
	if loaded[0].Attempts < 3 {
		t.Errorf("Attempts = %d, want >= 3", loaded[0].Attempts)
	}
}

func TestAlarmClock_ConcurrentSetCancel(t *testing.T) {
	clock := NewAlarmClock(func(*Alarm) error { return nil })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := uuidFake(i)
			_ = clock.Set(&Alarm{ID: id, FireAt: time.Now().Add(time.Hour), Prompt: "p"})
			clock.Cancel(id)
		}(i)
	}
	wg.Wait()
	// Just need to not race or deadlock. The cancelled state is
	// terminal-like in effect.
	for _, a := range clock.List() {
		if !a.Cancelled {
			t.Errorf("alarm %s not cancelled after Cancel call", a.ID)
		}
	}
}

func TestAlarmClock_NonPersistentStillFires(t *testing.T) {
	// No store wired — clock must still tick + fire correctly. This is
	// the test/no-daemon path.
	var fired atomic.Int32
	clock := NewAlarmClock(func(*Alarm) error {
		fired.Add(1)
		return nil
	})
	now0 := time.Now()
	clock.now = func() time.Time { return now0 }

	_ = clock.Set(&Alarm{ID: "x", FireAt: now0.Add(-time.Second), Prompt: "p"})
	clock.checkAlarms()
	if got := fired.Load(); got != 1 {
		t.Errorf("expected 1 fire, got %d", got)
	}
}

func TestAlarmClock_FireCallbackErrorIsLogged(t *testing.T) {
	stderr := captureStderr(t)
	// Failing persist after fire — we want to see the stderr warning.
	store := &failOnSaveAfterN{count: 1} // first save succeeds (Set), second fails (post-fire)
	clock := NewAlarmClockWithStore(func(*Alarm) error { return nil }, store)
	now0 := time.Now()
	clock.now = func() time.Time { return now0 }
	_ = clock.Set(&Alarm{ID: "x", FireAt: now0.Add(-time.Second), Prompt: "p"})
	clock.checkAlarms()
	if !bytes.Contains(stderr.Bytes(), []byte("persist post-fire")) {
		t.Errorf("expected post-fire persist warning, got: %q", stderr.String())
	}
}

// --- test fixtures ---

type failOnSaveStore struct{ err error }

func (s *failOnSaveStore) Save(*Alarm) error       { return s.err }
func (s *failOnSaveStore) Load() ([]*Alarm, error) { return nil, nil }
func (s *failOnSaveStore) Delete(string) error     { return nil }

type failOnSaveAfterN struct {
	mu    sync.Mutex
	calls int
	count int // succeed for `count` calls, then fail forever
}

func (s *failOnSaveAfterN) Save(*Alarm) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls > s.count {
		return errors.New("simulated persist failure")
	}
	return nil
}
func (s *failOnSaveAfterN) Load() ([]*Alarm, error) { return nil, nil }
func (s *failOnSaveAfterN) Delete(string) error     { return nil }

// uuidFake returns deterministic IDs without bringing in the uuid
// package's random source — easier to debug test failures.
func uuidFake(i int) string {
	return "test-" + string(rune('a'+i%26)) + "-" + intStr(i)
}

func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [10]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
