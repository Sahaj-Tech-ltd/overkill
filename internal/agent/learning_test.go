package agent

import (
	"sync"
	"testing"
)

type fakeLearner struct {
	mu      sync.Mutex
	classes []string
}

func (f *fakeLearner) RecordSuccess(class string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.classes = append(f.classes, class)
	return true
}

func (f *fakeLearner) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.classes))
	copy(out, f.classes)
	return out
}

func TestRecordRecoverySuccess_NoPriorError(t *testing.T) {
	a := &Agent{}
	rec := &fakeLearner{}
	a.SetLearningRecorder(rec)
	a.recordRecoverySuccess()
	if got := rec.seen(); len(got) != 0 {
		t.Errorf("expected no calls when lastErrorClass is empty, got %v", got)
	}
}

func TestRecordRecoverySuccess_FiresOnce(t *testing.T) {
	a := &Agent{}
	rec := &fakeLearner{}
	a.SetLearningRecorder(rec)
	a.mu.Lock()
	a.lastErrorClass = "compile"
	a.mu.Unlock()

	a.recordRecoverySuccess()
	a.recordRecoverySuccess() // second call: class was cleared, should be no-op

	got := rec.seen()
	if len(got) != 1 || got[0] != "compile" {
		t.Errorf("expected single RecordSuccess(\"compile\"), got %v", got)
	}
}

func TestRecordRecoverySuccess_NilRecorderSafe(t *testing.T) {
	a := &Agent{}
	a.mu.Lock()
	a.lastErrorClass = "test"
	a.mu.Unlock()
	a.recordRecoverySuccess() // must not panic with nil recorder
	a.mu.RLock()
	cleared := a.lastErrorClass == ""
	a.mu.RUnlock()
	if !cleared {
		t.Error("lastErrorClass should be cleared even without recorder")
	}
}

func TestRecordRecoverySuccess_PanicRecovered(t *testing.T) {
	a := &Agent{}
	a.SetLearningRecorder(panickyLearner{})
	a.mu.Lock()
	a.lastErrorClass = "runtime"
	a.mu.Unlock()
	// must not panic out
	a.recordRecoverySuccess()
}

type panickyLearner struct{}

func (panickyLearner) RecordSuccess(string) bool { panic("intentional") }
