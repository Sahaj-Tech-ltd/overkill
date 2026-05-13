// Package agent — Task Flow checkpoint machine (master plan §7.1
// Layer 7). When the agent loop hits maxSteps mid-task, we save the
// in-flight state and exit with TaskTimedOut so a follow-up alarm can
// fire a sub-agent that resumes from step N+1.
//
// The mental model: an interrupted task is a *recoverable* failure,
// not a permanent one. We can't just retry the last step in isolation
// because LLM context drives the next action — we need the full
// history. The flow record captures everything the resume sub-agent
// needs to continue: history, original user input, model, where we
// were in the step loop, and a small reason payload for the user.
//
// State corruption is treated as a hard abort: a malformed JSON blob
// in BadgerDB shouldn't trigger an infinite retry loop. The resume
// path inspects the blob and either resumes cleanly or reports
// "checkpoint corrupted, won't retry" to the alarm dispatcher.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// FlowState is what gets persisted when a task times out. The struct
// is the on-wire JSON shape — keep fields stable.
type FlowState struct {
	// ID is the flow's persistence key. Multiple resume attempts share
	// the same ID so the audit trail is linear.
	ID string `json:"id"`
	// SessionID anchors the flow to a session. Resume builds an agent
	// scoped to this session so cost/usage tracking stays correct.
	SessionID string `json:"session_id"`
	// UserInput is the original user prompt that kicked off the task.
	// Sub-agent resume includes this verbatim in the resume prompt so
	// the model has the original intent, not just the partial history.
	UserInput string `json:"user_input"`
	// Model + Provider pin the resume to the same backend the task
	// started on. Cross-model resume would muddy the audit trail and
	// confuse pricing.
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
	// History is the full conversation up to and including the last
	// completed step's tool result. Resume restores this into the
	// agent before continuing.
	History []providers.Message `json:"history"`
	// Step is the loop iteration index at which the task timed out.
	// Resume starts at Step (i.e. retry the step that ran out of
	// budget) — the saved history already contains all turns BEFORE
	// Step, so this is "where to continue from".
	Step int `json:"step"`
	// Reason is the human-readable cause of the timeout for the user
	// (e.g. "exceeded max steps (50) during auth refactor").
	Reason string `json:"reason"`
	// CreatedAt + ResumedAt give an audit trail: when did the task
	// first time out, when did it resume.
	CreatedAt  time.Time   `json:"created_at"`
	ResumedAt  []time.Time `json:"resumed_at,omitempty"`
	// Resumes counts how many times this flow was resumed. Used to
	// stop runaway resume loops — three resume attempts and we mark
	// the flow `failed` instead of saving another checkpoint.
	Resumes int `json:"resumes"`
}

// flowIDFor derives a stable flow ID for the (session, userInput)
// pair so successive max-steps exits on the same task overwrite the
// same record rather than piling up checkpoints. The ID is a short
// hash — humans see it once in toast output, the resume dispatcher
// uses it to look up state.
func flowIDFor(sessionID, userInput string) string {
	// Cheap deterministic hash. We don't need crypto strength — just
	// enough that two different (session, input) pairs don't collide
	// at the scale of one user's sessions.
	const fnvOffset uint64 = 14695981039346656037
	const fnvPrime uint64 = 1099511628211
	h := fnvOffset
	for i := 0; i < len(sessionID); i++ {
		h ^= uint64(sessionID[i])
		h *= fnvPrime
	}
	h ^= '|'
	h *= fnvPrime
	for i := 0; i < len(userInput); i++ {
		h ^= uint64(userInput[i])
		h *= fnvPrime
	}
	return fmt.Sprintf("flow-%016x", h)
}

// MaxResumes is the cap on how many times a single flow can be
// resumed. Three is generous — most "stuck on max_steps" timeouts
// finish on the first or second resume. Beyond that, the task is
// genuinely too large for the configured budget and the user should
// see "give up" rather than the agent pinging itself forever.
const MaxResumes = 3

// ErrFlowCorrupt signals "the persisted checkpoint can't be parsed".
// Resume callers MUST treat this as terminal — DO NOT retry, because
// retrying corrupt state is what produces infinite loops.
var ErrFlowCorrupt = errors.New("flow: checkpoint corrupt")

// ErrFlowExhausted signals "this flow has been resumed MaxResumes
// times". The task is too large for the agent's budget; the user
// should see the final state instead of another resume attempt.
var ErrFlowExhausted = errors.New("flow: max resumes exceeded")

// FlowStore is the persistence surface. Save is idempotent (overwrite
// by ID). Load returns ErrFlowCorrupt for blobs that can't be parsed
// so the caller doesn't have to guess.
type FlowStore interface {
	Save(state *FlowState) error
	Load(id string) (*FlowState, error)
	Delete(id string) error
	List() ([]*FlowState, error)
}

// MemoryFlowStore is the test/no-persistence implementation. Safe for
// concurrent use.
type MemoryFlowStore struct {
	mu    sync.RWMutex
	flows map[string][]byte // raw JSON so a corrupt-blob test can inject bad bytes
}

// NewMemoryFlowStore returns an empty in-memory store.
func NewMemoryFlowStore() *MemoryFlowStore {
	return &MemoryFlowStore{flows: map[string][]byte{}}
}

// Save serializes the state and stores it under state.ID.
func (s *MemoryFlowStore) Save(state *FlowState) error {
	if state == nil {
		return fmt.Errorf("flow store: nil state")
	}
	if state.ID == "" {
		return fmt.Errorf("flow store: empty ID")
	}
	b, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("flow store: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[state.ID] = b
	return nil
}

// Load returns the parsed state or ErrFlowCorrupt for unparseable
// blobs. Missing IDs return (nil, nil) so callers can distinguish
// "not found" from "found but broken".
func (s *MemoryFlowStore) Load(id string) (*FlowState, error) {
	s.mu.RLock()
	raw, ok := s.flows[id]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	var state FlowState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, ErrFlowCorrupt
	}
	return &state, nil
}

// Delete removes a flow record. Missing IDs are no-ops.
func (s *MemoryFlowStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.flows, id)
	return nil
}

// List returns every parseable flow. Corrupt entries are dropped from
// the list AND from the store so a follow-up call doesn't keep
// rediscovering them. (Tests can also use saveRaw to seed corrupt
// blobs intentionally.)
func (s *MemoryFlowStore) List() ([]*FlowState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*FlowState, 0, len(s.flows))
	for id, raw := range s.flows {
		var state FlowState
		if err := json.Unmarshal(raw, &state); err != nil {
			delete(s.flows, id)
			continue
		}
		cp := state
		out = append(out, &cp)
	}
	return out, nil
}

// saveRaw is a test-only escape hatch for injecting corrupt blobs.
// Not exported because production callers should never need it.
func (s *MemoryFlowStore) saveRaw(id string, raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[id] = raw
}

// flowMutexes serializes Save+Resume on the same flow ID so two
// concurrent resume attempts can't trample each other. Per-ID locks
// keep the contention narrow — unrelated flows aren't blocked.
var flowMutexes = struct {
	sync.Mutex
	locks map[string]*sync.Mutex
}{locks: map[string]*sync.Mutex{}}

// flowLock returns the mutex for flowID, creating it on first use.
func flowLock(flowID string) *sync.Mutex {
	flowMutexes.Lock()
	defer flowMutexes.Unlock()
	lk, ok := flowMutexes.locks[flowID]
	if !ok {
		lk = &sync.Mutex{}
		flowMutexes.locks[flowID] = lk
	}
	return lk
}

// CheckpointFlow saves the in-flight state when the agent loop hits
// maxSteps. Called from stream.go's exit path. Idempotent — calling
// twice with the same flowID overwrites (last write wins; the more
// recent state has more history).
//
// Returns the persisted FlowState so the caller can emit it on the
// stream's final event (which lets the TUI surface "timed out, will
// resume in N minutes" instead of an opaque error).
func CheckpointFlow(store FlowStore, flowID, sessionID, userInput, model, provider string, history []providers.Message, step int, reason string) (*FlowState, error) {
	if store == nil {
		return nil, fmt.Errorf("flow: store not wired")
	}
	if flowID == "" {
		return nil, fmt.Errorf("flow: flowID required")
	}

	lock := flowLock(flowID)
	lock.Lock()
	defer lock.Unlock()

	// Preserve resume count from any prior checkpoint so MaxResumes
	// stays accurate across multiple resume attempts.
	resumes := 0
	if prior, _ := store.Load(flowID); prior != nil {
		resumes = prior.Resumes
	}

	state := &FlowState{
		ID:        flowID,
		SessionID: sessionID,
		UserInput: userInput,
		Model:     model,
		Provider:  provider,
		History:   append([]providers.Message(nil), history...),
		Step:      step,
		Reason:    reason,
		CreatedAt: time.Now().UTC(),
		Resumes:   resumes,
	}
	if err := store.Save(state); err != nil {
		return nil, fmt.Errorf("flow checkpoint: %w", err)
	}
	return state, nil
}

// MarkResumed bumps the resume counter and stamps ResumedAt. Returns
// ErrFlowExhausted when the flow has already been resumed MaxResumes
// times — callers should NOT proceed with another resume; the task
// is too large for the configured budget.
func MarkResumed(store FlowStore, flowID string) (*FlowState, error) {
	if store == nil {
		return nil, fmt.Errorf("flow: store not wired")
	}
	lock := flowLock(flowID)
	lock.Lock()
	defer lock.Unlock()

	state, err := store.Load(flowID)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("flow: %s not found", flowID)
	}
	if state.Resumes >= MaxResumes {
		return state, ErrFlowExhausted
	}
	state.Resumes++
	state.ResumedAt = append(state.ResumedAt, time.Now().UTC())
	if err := store.Save(state); err != nil {
		return nil, fmt.Errorf("flow: persist resume: %w", err)
	}
	return state, nil
}
