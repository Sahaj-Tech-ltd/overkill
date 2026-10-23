package automation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

var errSOPDisk = errors.New("injected durable store failure")

// The optional commit-before-error mode models a lost acknowledgement: callers
// cannot assume a failed SaveSOP means no write reached durable storage.
type sopFaultStore struct {
	mu                sync.Mutex
	saved             map[string]*SOP
	fail              func(*SOP) bool
	commitBeforeError bool
	loadErr           error
	backing           SOPStore
}

func newSOPFaultStore() *sopFaultStore { return &sopFaultStore{saved: map[string]*SOP{}} }
func (s *sopFaultStore) SaveSOP(sop *SOP) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := s.fail != nil && s.fail(sop)
	if !fail || s.commitBeforeError {
		if s.backing != nil {
			if err := s.backing.SaveSOP(sop); err != nil {
				return err
			}
		} else {
			s.saved[sop.ID] = cloneSOP(sop)
		}
	}
	if fail {
		return errSOPDisk
	}
	return nil
}
func (s *sopFaultStore) LoadSOPs() ([]SOP, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.backing != nil {
		return s.backing.LoadSOPs()
	}
	var out []SOP
	for _, saved := range s.saved {
		out = append(out, *cloneSOP(saved))
	}
	return out, nil
}
func (s *sopFaultStore) DeleteSOP(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.saved, id)
	return nil
}
func (s *sopFaultStore) fault(f func(*SOP) bool, commitBeforeError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = f
	s.commitBeforeError = commitBeforeError
}
func sopOneStep(mode SOPMode) *SOP {
	return &SOP{ID: "workflow", Mode: mode, Steps: []Step{{ID: "charge", Action: "charge credit card", RequiresApproval: mode == ModeSupervised}}}
}

func TestSOPCreatePersistenceFailureDoesNotPublish(t *testing.T) {
	store := newSOPFaultStore()
	store.fault(func(*SOP) bool { return true }, false)
	var calls atomic.Int32
	engine := NewSOPEngine(store, func(string) (string, error) { calls.Add(1); return "receipt", nil })
	original := sopOneStep(ModeAuto)
	require.ErrorIs(t, engine.Create(original), errSOPDisk)
	require.ErrorIs(t, engine.Execute(t.Context(), "workflow"), ErrNotFound)
	_, exists := engine.Get("workflow")
	require.False(t, exists)
	require.Empty(t, original.Status)
	store.fault(nil, false)
	require.NoError(t, engine.Create(original))
	require.NoError(t, engine.Execute(t.Context(), "workflow"))
	require.EqualValues(t, 1, calls.Load())
}

func TestSOPTransitionFailuresLeaveAcknowledgedStateUnchanged(t *testing.T) {
	for _, name := range []string{"waiting", "approval", "pause", "resume", "cancel", "context cancellation"} {
		t.Run(name, func(t *testing.T) {
			store := newSOPFaultStore()
			var calls atomic.Int32
			engine := NewSOPEngine(store, func(string) (string, error) { calls.Add(1); return "receipt", nil })
			mode := ModeAuto
			if name == "waiting" || name == "approval" {
				mode = ModeSupervised
			}
			require.NoError(t, engine.Create(sopOneStep(mode)))
			if name == "approval" {
				require.ErrorIs(t, engine.Execute(t.Context(), "workflow"), ErrStepWaiting)
			}
			if name == "resume" {
				require.NoError(t, engine.Pause("workflow"))
			}
			before, _ := engine.Get("workflow")
			store.fault(func(*SOP) bool { return true }, false)
			var err error
			switch name {
			case "waiting":
				err = engine.Execute(t.Context(), "workflow")
				require.NotErrorIs(t, err, ErrStepWaiting)
			case "approval":
				err = engine.ApproveStep("workflow", "charge")
			case "pause":
				err = engine.Pause("workflow")
			case "resume":
				err = engine.Resume(t.Context(), "workflow")
			case "cancel":
				err = engine.Cancel("workflow")
			case "context cancellation":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				err = engine.Execute(ctx, "workflow")
				require.ErrorIs(t, err, context.Canceled)
			}
			require.ErrorIs(t, err, ErrSOPPersistence)
			require.ErrorIs(t, err, errSOPDisk)
			after, _ := engine.Get("workflow")
			require.Equal(t, before, after)
			require.Zero(t, calls.Load())
			restarted := NewSOPEngine(store, func(string) (string, error) { calls.Add(1); return "receipt", nil })
			recovered, _ := restarted.Get("workflow")
			require.Equal(t, before, recovered)
		})
	}
}

func TestSOPRunningCheckpointMustPersistBeforeSideEffect(t *testing.T) {
	for _, lostAck := range []bool{false, true} {
		t.Run(fmt.Sprint("lost-ack=", lostAck), func(t *testing.T) {
			store := newSOPFaultStore()
			var calls atomic.Int32
			executor := func(string) (string, error) { calls.Add(1); return "receipt", nil }
			engine := NewSOPEngine(store, executor)
			require.NoError(t, engine.Create(sopOneStep(ModeAuto)))
			store.fault(func(sop *SOP) bool { return sop.Steps[0].Status == StepRunning }, lostAck)
			require.ErrorIs(t, engine.Execute(t.Context(), "workflow"), ErrSOPPersistence)
			require.Zero(t, calls.Load())
			current, _ := engine.Get("workflow")
			require.Equal(t, StepPending, current.Steps[0].Status)
			store.fault(nil, false)
			restarted := NewSOPEngine(store, executor)
			if lostAck {
				recovered, _ := restarted.Get("workflow")
				require.Equal(t, SOPStatusDegraded, recovered.Status)
				require.ErrorIs(t, restarted.RetryPersistence("workflow"), ErrSOPUncertain)
				require.Error(t, restarted.Execute(t.Context(), "workflow"))
				require.Zero(t, calls.Load())
			} else {
				require.NoError(t, restarted.Execute(t.Context(), "workflow"))
				require.EqualValues(t, 1, calls.Load())
			}
		})
	}
}

func TestSOPOutcomePersistenceFailureNeverReplaysSideEffect(t *testing.T) {
	for _, stepFailure := range []bool{false, true} {
		for _, lostAck := range []bool{false, true} {
			t.Run(fmt.Sprintf("executor-error=%v/lost-ack=%v", stepFailure, lostAck), func(t *testing.T) {
				store := newSOPFaultStore()
				var calls atomic.Int32
				effectErr := errors.New("external charge accepted but reply failed")
				executor := func(string) (string, error) {
					calls.Add(1)
					if stepFailure {
						return "", effectErr
					}
					return "receipt", nil
				}
				engine := NewSOPEngine(store, executor)
				require.NoError(t, engine.Create(sopOneStep(ModeAuto)))
				store.fault(func(sop *SOP) bool { return sop.Steps[0].Status == StepDone || sop.Steps[0].Status == StepFailed }, lostAck)
				err := engine.Execute(t.Context(), "workflow")
				require.ErrorIs(t, err, ErrSOPPersistence)
				if stepFailure {
					require.ErrorIs(t, err, effectErr)
				}
				current, _ := engine.Get("workflow")
				require.Equal(t, SOPStatusDegraded, current.Status)
				require.NotEmpty(t, current.PersistenceError)
				require.Error(t, engine.Execute(t.Context(), "workflow"))
				require.EqualValues(t, 1, calls.Load())
				store.fault(nil, false)
				restarted := NewSOPEngine(store, executor)
				if !lostAck {
					recovered, _ := restarted.Get("workflow")
					require.Equal(t, SOPStatusDegraded, recovered.Status)
					require.Equal(t, StepRunning, recovered.Steps[0].Status)
					require.Error(t, restarted.Execute(t.Context(), "workflow"))
					require.ErrorIs(t, restarted.RetryPersistence("workflow"), ErrSOPUncertain)
				} else if stepFailure {
					require.Error(t, restarted.Execute(t.Context(), "workflow"))
				} else {
					require.NoError(t, restarted.Execute(t.Context(), "workflow"))
				}
				require.EqualValues(t, 1, calls.Load())
				// The original process knows the outcome, so persisting that receipt is safe.
				require.NoError(t, engine.RetryPersistence("workflow"))
				require.EqualValues(t, 1, calls.Load())
				if !stepFailure {
					require.NoError(t, engine.Execute(t.Context(), "workflow"))
				}
				require.EqualValues(t, 1, calls.Load())
			})
		}
	}
}

func TestSOPCompletionFailureHasPersistenceOnlyRetry(t *testing.T) {
	store := newSOPFaultStore()
	var calls atomic.Int32
	executor := func(string) (string, error) { calls.Add(1); return "receipt", nil }
	engine := NewSOPEngine(store, executor)
	require.NoError(t, engine.Create(sopOneStep(ModeAuto)))
	store.fault(func(sop *SOP) bool { return sop.Status == SOPStatusCompleted }, false)
	require.ErrorIs(t, engine.Execute(t.Context(), "workflow"), ErrSOPPersistence)
	current, _ := engine.Get("workflow")
	require.Equal(t, SOPStatusDegraded, current.Status)
	require.Equal(t, StepDone, current.Steps[0].Status)
	require.ErrorIs(t, engine.RetryPersistence("workflow"), ErrSOPPersistence)
	require.EqualValues(t, 1, calls.Load())
	store.fault(nil, false)
	restarted := NewSOPEngine(store, executor)
	require.NoError(t, restarted.Execute(t.Context(), "workflow"))
	require.EqualValues(t, 1, calls.Load())
	require.NoError(t, engine.RetryPersistence("workflow"))
	current, _ = engine.Get("workflow")
	require.Equal(t, SOPStatusCompleted, current.Status)
	require.EqualValues(t, 1, calls.Load())
}

func TestSOPConcurrentExecutionAndPauseDoNotDuplicate(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	store := newSOPFaultStore()
	engine := NewSOPEngine(store, func(string) (string, error) { calls.Add(1); close(started); <-release; return "receipt", nil })
	require.NoError(t, engine.Create(sopOneStep(ModeAuto)))
	result := make(chan error, 1)
	go func() { result <- engine.Execute(t.Context(), "workflow") }()
	<-started
	require.ErrorIs(t, engine.Execute(t.Context(), "workflow"), ErrInvalidState)
	require.NoError(t, engine.Pause("workflow"))
	require.ErrorIs(t, engine.Resume(t.Context(), "workflow"), ErrInvalidState)
	close(release)
	require.NoError(t, <-result)
	current, _ := engine.Get("workflow")
	require.Equal(t, SOPStatusPaused, current.Status)
	require.Equal(t, StepDone, current.Steps[0].Status)
	require.NoError(t, engine.Resume(t.Context(), "workflow"))
	require.EqualValues(t, 1, calls.Load())
}

func TestSOPLoadFailureBlocksMutationAndSnapshotAliases(t *testing.T) {
	store := newSOPFaultStore()
	store.loadErr = errSOPDisk
	engine := NewSOPEngine(store, func(string) (string, error) { t.Fatal("executor reached"); return "", nil })
	require.ErrorIs(t, engine.Create(sopOneStep(ModeAuto)), errSOPDisk)
	require.ErrorIs(t, engine.Execute(t.Context(), "workflow"), errSOPDisk)
	store.loadErr = nil
	engine = NewSOPEngine(store, func(string) (string, error) { return "receipt", nil })
	sop := sopOneStep(ModeAuto)
	sop.Metadata = map[string]string{"key": "original"}
	require.NoError(t, engine.Create(sop))
	sop.Metadata["key"] = "external"
	sop.Steps[0].Action = "external"
	got, _ := engine.Get("workflow")
	got.Metadata["key"] = "external"
	got.Steps[0].Action = "external"
	listed := engine.List()
	listed[0].Metadata["key"] = "external"
	got, _ = engine.Get("workflow")
	require.Equal(t, "original", got.Metadata["key"])
	require.Equal(t, "charge credit card", got.Steps[0].Action)
}

func TestSOPPostgresRunningMarkerSurvivesOutcomeWriteFailure(t *testing.T) {
	database, _ := testpg.Open(t)
	backing, err := NewPostgresSOPStore(database)
	require.NoError(t, err)
	store := newSOPFaultStore()
	store.backing = backing
	var calls atomic.Int32
	executor := func(string) (string, error) { calls.Add(1); return "charge receipt", nil }
	engine := NewSOPEngine(store, executor)
	require.NoError(t, engine.Create(sopOneStep(ModeAuto)))
	store.fault(func(sop *SOP) bool { return sop.Steps[0].Status == StepDone }, false)
	require.ErrorIs(t, engine.Execute(t.Context(), "workflow"), ErrSOPPersistence)
	// A new engine reads real PostgreSQL, with no access to in-process receipts.
	restarted := NewSOPEngine(backing, executor)
	recovered, _ := restarted.Get("workflow")
	require.Equal(t, SOPStatusDegraded, recovered.Status)
	require.Equal(t, StepRunning, recovered.Steps[0].Status)
	require.Error(t, restarted.Execute(t.Context(), "workflow"))
	require.ErrorIs(t, restarted.RetryPersistence("workflow"), ErrSOPUncertain)
	require.EqualValues(t, 1, calls.Load())
}

func TestSOPPersistenceOnlyRetryContinuesAfterReceiptWithoutReplaying(t *testing.T) {
	store := newSOPFaultStore()
	var actions []string
	var mu sync.Mutex
	executor := func(action string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		actions = append(actions, action)
		return "receipt " + action, nil
	}
	engine := NewSOPEngine(store, executor)
	sop := sopOneStep(ModeAuto)
	sop.Steps = append(sop.Steps, Step{ID: "notify", Action: "send receipt"})
	require.NoError(t, engine.Create(sop))
	store.fault(func(sop *SOP) bool { return sop.Steps[0].Status == StepDone }, false)
	require.ErrorIs(t, engine.Execute(t.Context(), "workflow"), ErrSOPPersistence)
	require.Equal(t, []string{"charge credit card"}, actions)
	store.fault(nil, false)
	require.NoError(t, engine.RetryPersistence("workflow"))
	require.Equal(t, []string{"charge credit card"}, actions)
	require.NoError(t, engine.Execute(t.Context(), "workflow"))
	require.Equal(t, []string{"charge credit card", "send receipt"}, actions)
}
