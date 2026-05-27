package automation

import (
	"fmt"
	"sync"
	"testing"
)

func TestLedger_TerminalSinkFiresOnComplete(t *testing.T) {
	l := NewLedger(10)
	var got []LedgerTask
	var mu sync.Mutex
	l.SetTerminalSink(func(t LedgerTask) {
		mu.Lock()
		got = append(got, t)
		mu.Unlock()
	})
	tk := l.Begin("test", "demo")
	l.Complete(tk.ID, "all done")

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 sink fire, got %d", len(got))
	}
	if got[0].State != TaskCompleted {
		t.Errorf("expected Completed state, got %s", got[0].State)
	}
	if got[0].Result != "all done" {
		t.Errorf("result not propagated: %q", got[0].Result)
	}
}

func TestLedger_TerminalSinkFiresOnFailLostTimedOutCancel(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(l *Ledger, id string)
		want   TaskState
	}{
		{"fail", func(l *Ledger, id string) { l.Fail(id, fmt.Errorf("boom")) }, TaskFailed},
		{"lost", func(l *Ledger, id string) { l.MarkLost(id, "no heartbeat") }, TaskLost},
		{"timeout", func(l *Ledger, id string) { l.TimedOut(id, "budget hit") }, TaskTimedOut},
		{"cancel", func(l *Ledger, id string) { l.Cancel(id) }, TaskCancelled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := NewLedger(10)
			var snap LedgerTask
			l.SetTerminalSink(func(t LedgerTask) { snap = t })
			tk := l.Begin("test", "demo")
			c.mutate(l, tk.ID)
			if snap.State != c.want {
				t.Errorf("state: got %s want %s", snap.State, c.want)
			}
		})
	}
}

func TestLedger_TerminalSinkDoesNotFireTwice(t *testing.T) {
	l := NewLedger(10)
	calls := 0
	l.SetTerminalSink(func(LedgerTask) { calls++ })
	tk := l.Begin("test", "demo")
	l.Complete(tk.ID, "done")
	l.Complete(tk.ID, "done again") // already terminal — should not re-fire
	if calls != 1 {
		t.Errorf("expected 1 fire, got %d", calls)
	}
}

func TestLedger_TerminalSinkNilSafe(t *testing.T) {
	l := NewLedger(10)
	// No sink installed — Update must not panic.
	tk := l.Begin("test", "demo")
	l.Complete(tk.ID, "done")
}
