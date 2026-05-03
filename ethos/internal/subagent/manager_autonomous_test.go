package subagent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestManager_SpawnContract_Lifecycle(t *testing.T) {
	m := NewManager(Config{MaxChildren: 3})
	d := &fakeDriver{max: 100000, completeAt: 2, specs: []string{"a"}}
	c := runnerContract([]string{"a"})

	id, err := m.SpawnContract(context.Background(), c, d, "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if id != c.ID {
		t.Fatalf("id = %q want %q", id, c.ID)
	}

	rep, err := m.AutonomousWait(context.Background(), id)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if rep == nil || rep.Status != "completed" {
		t.Fatalf("report = %+v want completed", rep)
	}

	st, ok := m.AutonomousStatus(id)
	if !ok || st.ContractID != id {
		t.Fatalf("status missing or wrong: %+v ok=%v", st, ok)
	}
}

func TestManager_SpawnContract_DuplicateRejected(t *testing.T) {
	m := NewManager(Config{MaxChildren: 3})
	c := runnerContract([]string{"a"})
	d := &fakeDriver{max: 100000}
	if _, err := m.SpawnContract(context.Background(), c, d, "", nil); err != nil {
		t.Fatalf("spawn1: %v", err)
	}
	defer m.AutonomousCancel(c.ID)
	if _, err := m.SpawnContract(context.Background(), c, &fakeDriver{max: 100000}, "", nil); err == nil {
		t.Fatal("expected duplicate spawn to error")
	}
}

func TestManager_AutonomousCancel(t *testing.T) {
	m := NewManager(Config{MaxChildren: 3})
	d := &fakeDriver{max: 100000}
	c := runnerContract([]string{"never-satisfied"})
	c.Budget.Steps = 100000
	id, err := m.SpawnContract(context.Background(), c, d, "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if !m.AutonomousCancel(id) {
		t.Fatal("cancel should return true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rep, err := m.AutonomousWait(ctx, id)
	if err != nil {
		t.Fatalf("wait after cancel: %v", err)
	}
	if rep.Status != "handed_off" {
		t.Fatalf("status = %q want handed_off", rep.Status)
	}
}

func TestManager_AutonomousReport_BeforeAndAfter(t *testing.T) {
	m := NewManager(Config{MaxChildren: 3})
	d := &fakeDriver{max: 100000, completeAt: 2, specs: []string{"a"}}
	c := runnerContract([]string{"a"})
	id, _ := m.SpawnContract(context.Background(), c, d, "", nil)

	if _, err := m.AutonomousWait(context.Background(), id); err != nil {
		t.Fatalf("wait: %v", err)
	}
	rep, running, err := m.AutonomousReport(id)
	if err != nil || running || rep == nil {
		t.Fatalf("report after done: rep=%v running=%v err=%v", rep, running, err)
	}
}

func TestManager_AutonomousList(t *testing.T) {
	m := NewManager(Config{MaxChildren: 3})
	d := &fakeDriver{max: 100000}
	c := runnerContract([]string{"a"})
	c.ID = "list-1"
	if _, err := m.SpawnContract(context.Background(), c, d, "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer m.AutonomousCancel(c.ID)
	got := m.AutonomousList()
	if len(got) != 1 || got[0] != "list-1" {
		t.Fatalf("list = %v want [list-1]", got)
	}
}

func TestManager_SpawnContract_RejectsInvalid(t *testing.T) {
	m := NewManager(Config{MaxChildren: 3})
	if _, err := m.SpawnContract(context.Background(), nil, &fakeDriver{}, "", nil); err == nil {
		t.Fatal("nil contract must error")
	}
	bad := &Contract{ID: "x", Goal: "x"}
	if _, err := m.SpawnContract(context.Background(), bad, &fakeDriver{}, "", nil); err == nil {
		t.Fatal("invalid contract must error")
	}
}

type recordingSink struct {
	mu    sync.Mutex
	calls []sinkCall
}
type sinkCall struct {
	parentSession string
	contractID    string
	status        string
	reason        string
}

func (s *recordingSink) OnDelegationFailure(parentSession string, c *Contract, rep *FinalReport, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := sinkCall{parentSession: parentSession}
	if c != nil {
		row.contractID = c.ID
	}
	if rep != nil {
		row.status = rep.Status
		row.reason = rep.Reason
	}
	if err != nil && row.reason == "" {
		row.reason = err.Error()
	}
	s.calls = append(s.calls, row)
}
func (s *recordingSink) snapshot() []sinkCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sinkCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func TestManager_FailureSink_FiresOnViolation(t *testing.T) {
	m := NewManager(Config{MaxChildren: 3})
	sink := &recordingSink{}
	m.SetFailureSink(sink)

	c := runnerContract([]string{"a"})
	c.ParentSession = "parent-1"
	d := &fakeDriver{max: 100000, doneAt: 1} // done before satisfying outputs → violation

	id, err := m.SpawnContract(context.Background(), c, d, "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if _, err := m.AutonomousWait(context.Background(), id); err != nil {
		t.Fatalf("wait: %v", err)
	}
	// Allow goroutine fire-and-forget to land.
	time.Sleep(50 * time.Millisecond)
	calls := sink.snapshot()
	if len(calls) != 1 {
		t.Fatalf("got %d sink calls want 1", len(calls))
	}
	if calls[0].parentSession != "parent-1" {
		t.Fatalf("parent session = %q want parent-1", calls[0].parentSession)
	}
	if calls[0].status != "violated" {
		t.Fatalf("status = %q want violated", calls[0].status)
	}
}

func TestManager_FailureSink_NotFiredOnSuccess(t *testing.T) {
	m := NewManager(Config{MaxChildren: 3})
	sink := &recordingSink{}
	m.SetFailureSink(sink)

	c := runnerContract([]string{"a"})
	d := &fakeDriver{max: 100000, completeAt: 2, specs: []string{"a"}}

	id, _ := m.SpawnContract(context.Background(), c, d, "", nil)
	_, _ = m.AutonomousWait(context.Background(), id)
	time.Sleep(50 * time.Millisecond)
	if calls := sink.snapshot(); len(calls) != 0 {
		t.Fatalf("expected 0 sink calls on success, got %v", calls)
	}
}
