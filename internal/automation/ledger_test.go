package automation

import (
	"errors"
	"testing"
	"time"
)

func TestLedger_BeginTransitionList(t *testing.T) {
	l := NewLedger(50)
	t1 := l.Begin("cron", "daily-backup")
	if t1.State != TaskRunning {
		t.Fatalf("state=%s want running", t1.State)
	}
	l.Complete(t1.ID, "ok")
	got, ok := l.Get(t1.ID)
	if !ok || got.State != TaskCompleted {
		t.Fatalf("complete didn't transition: %+v", got)
	}
	if got.EndedAt.IsZero() {
		t.Fatal("EndedAt should be set on completion")
	}
}

func TestLedger_FailRecordsError(t *testing.T) {
	l := NewLedger(50)
	t1 := l.Begin("subagent", "build")
	l.Fail(t1.ID, errors.New("compile error"))
	got, _ := l.Get(t1.ID)
	if got.State != TaskFailed || got.Error != "compile error" {
		t.Fatalf("fail not recorded: %+v", got)
	}
}

func TestLedger_ActiveExcludesTerminal(t *testing.T) {
	l := NewLedger(50)
	a := l.Begin("cron", "a")
	_ = l.Begin("cron", "b") // still running
	l.Complete(a.ID, "")

	active := l.Active()
	if len(active) != 1 {
		t.Fatalf("active count=%d want 1", len(active))
	}
	if active[0].State != TaskRunning {
		t.Fatalf("active[0] state=%s want running", active[0].State)
	}
}

func TestLedger_EvictsOldestTerminalWhenFull(t *testing.T) {
	l := NewLedger(3)
	first := l.Begin("cron", "first")
	l.Complete(first.ID, "")
	time.Sleep(2 * time.Millisecond)
	second := l.Begin("cron", "second")
	l.Complete(second.ID, "")
	time.Sleep(2 * time.Millisecond)
	_ = l.Begin("cron", "third")
	_ = l.Begin("cron", "fourth") // pushes us to 4 > max 3 → evict oldest terminal

	if _, ok := l.Get(first.ID); ok {
		t.Fatal("oldest terminal should have been evicted")
	}
	if _, ok := l.Get(second.ID); !ok {
		t.Fatal("newer terminal should remain")
	}
}

func TestLedger_DoesNotEvictRunning(t *testing.T) {
	l := NewLedger(2)
	a := l.Begin("cron", "a")
	b := l.Begin("cron", "b")
	c := l.Begin("cron", "c") // 3 in flight, all running

	if _, ok := l.Get(a.ID); !ok {
		t.Error("a evicted while running")
	}
	if _, ok := l.Get(b.ID); !ok {
		t.Error("b evicted while running")
	}
	if _, ok := l.Get(c.ID); !ok {
		t.Error("c evicted while running")
	}
}

func TestLedger_ListNewestFirst(t *testing.T) {
	l := NewLedger(50)
	a := l.Begin("cron", "a")
	time.Sleep(2 * time.Millisecond)
	b := l.Begin("cron", "b")
	list := l.List()
	if len(list) != 2 {
		t.Fatalf("got %d", len(list))
	}
	if list[0].ID != b.ID {
		t.Fatal("not sorted newest first")
	}
	if list[1].ID != a.ID {
		t.Fatal("oldest not last")
	}
}

func TestLedger_GetUnknown(t *testing.T) {
	l := NewLedger(5)
	if _, ok := l.Get("nope"); ok {
		t.Fatal("expected miss")
	}
}
