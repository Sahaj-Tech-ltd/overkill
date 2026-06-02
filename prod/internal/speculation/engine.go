// Package speculation provides idle-time predictive execution.
// After the agent completes a turn, a speculative fork runs with
// read-only tools to predict the next action. When the user accepts,
// the prediction is applied; when the user types, it's discarded.
//
// Architecture ported from Claude Code's speculation.ts.
package speculation

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// State tracks a single speculation lifecycle.
type State int

const (
	StateIdle      State = iota // no speculation running
	StateRunning                // forked agent is thinking
	StateReady                  // prediction complete, waiting for user
	StateAccepted               // user accepted, applying results
	StateDiscarded              // user typed or timed out
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateRunning:
		return "running"
	case StateReady:
		return "ready"
	case StateAccepted:
		return "accepted"
	case StateDiscarded:
		return "discarded"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// Result holds the completed speculation output.
type Result struct {
	Summary     string    `json:"summary"`    // human-readable prediction
	ToolCalls   []string  `json:"tool_calls"` // predicted tool calls
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// Engine manages the speculation lifecycle.
type Engine struct {
	mu     sync.Mutex
	state  State
	result *Result

	// gen is a monotonically increasing generation counter.
	// Incremented on Start() and Reset(). The run() goroutine captures
	// the current generation at launch and refuses to commit results
	// if the generation has changed — preventing stale writes after
	// Reset (bug #39).
	gen int64

	// cancelFn cancels the running speculation context. Set when
	// speculation starts; cleared when it completes or is discarded.
	cancelFn context.CancelFunc

	// Callbacks set by the agent wiring.
	// OnSpeculate is called to start a speculative fork.
	// The callback receives a context that cancels on discard.
	OnSpeculate func(ctx context.Context) (*Result, error)

	// OnStateChange fires when state transitions.
	OnStateChange func(old, new State)

	// MaxTurns limits how long speculation runs.
	MaxTurns int
	// MaxDuration caps wall-clock time.
	MaxDuration time.Duration
}

// NewEngine creates a speculation engine with defaults.
func NewEngine() *Engine {
	return &Engine{
		state:       StateIdle,
		MaxTurns:    20,
		MaxDuration: 30 * time.Second,
	}
}

// State returns the current speculation state.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// Result returns the completed result, if ready.
func (e *Engine) Result() *Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.result
}

// Start begins a speculation cycle. No-op if already running.
// Called after the agent completes a turn with no pending user input.
func (e *Engine) Start() {
	e.mu.Lock()
	if e.state != StateIdle {
		e.mu.Unlock()
		return
	}
	if e.OnSpeculate == nil {
		e.mu.Unlock()
		return
	}
	e.state = StateRunning
	e.result = nil
	e.gen++ // bump generation for this speculation cycle
	gen := e.gen
	// Create the cancellation context under the lock and store cancelFn
	// immediately so Discard() can cancel the goroutine. Previously
	// cancelFn was stored inside run() which races with Discard().
	ctx, cancel := context.WithTimeout(context.Background(), e.MaxDuration)
	e.cancelFn = cancel
	onStateChange := e.OnStateChange
	e.mu.Unlock()

	if onStateChange != nil {
		onStateChange(StateIdle, StateRunning)
	}

	go e.run(ctx, cancel, gen)
}

// Discard cancels any running speculation. Called when user starts typing.
func (e *Engine) Discard() {
	e.mu.Lock()
	prev := e.state
	e.state = StateDiscarded
	e.result = nil
	cancelFn := e.cancelFn
	e.cancelFn = nil
	onStateChange := e.OnStateChange
	e.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}

	if prev != StateIdle && prev != StateDiscarded && onStateChange != nil {
		onStateChange(prev, StateDiscarded)
	}
}

// Accept applies the predicted result. No-op if not ready.
func (e *Engine) Accept() *Result {
	e.mu.Lock()
	if e.state != StateReady {
		e.mu.Unlock()
		return nil
	}
	e.state = StateAccepted
	r := e.result
	onStateChange := e.OnStateChange
	e.mu.Unlock()

	if onStateChange != nil {
		onStateChange(StateReady, StateAccepted)
	}
	return r
}

// Reset transitions the engine back to Idle from any terminal state
// (Accepted, Discarded). Allows the engine to be reused across multiple
// speculation cycles without creating a new Engine instance.
// Cancels any running speculation goroutine.
// Bumps the generation counter to invalidate any in-flight speculation
// results (bug #39 fix).
func (e *Engine) Reset() {
	e.mu.Lock()
	cancelFn := e.cancelFn
	e.cancelFn = nil
	if e.state == StateIdle {
		e.mu.Unlock()
		return
	}
	prev := e.state
	e.state = StateIdle
	e.result = nil
	e.gen++ // invalidate in-flight speculation results
	onStateChange := e.OnStateChange
	e.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}
	if onStateChange != nil {
		onStateChange(prev, StateIdle)
	}
}

// run executes the speculation in a goroutine.
// gen is the generation counter captured at Start() time. Results are
// only committed if the engine's gen still matches — preventing stale
// writes after Reset() (bug #39).
func (e *Engine) run(ctx context.Context, cancel context.CancelFunc, gen int64) {
	// ctx already created with timeout and cancelFn stored by Start().
	// We defer cancel to clean up on exit.
	defer cancel()

	// Watch for discard — the agent calls Discard() when user types.
	// We use a polling approach: check state between speculative turns.
	resultCh := make(chan struct {
		r   *Result
		err error
	}, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("speculation: OnSpeculate goroutine panic: %v\n%s", r, debug.Stack())
			}
		}()
		r, err := e.OnSpeculate(ctx)
		resultCh <- struct {
			r   *Result
			err error
		}{r, err}
	}()

	select {
	case <-ctx.Done():
		e.mu.Lock()
		if e.state == StateRunning {
			e.state = StateDiscarded
		}
		e.mu.Unlock()
	case res := <-resultCh:
		if res.err != nil {
			e.mu.Lock()
			if e.state == StateRunning {
				e.state = StateDiscarded
			}
			e.mu.Unlock()
			return
		}
		e.mu.Lock()
		// Reject results if the generation has changed (Reset called)
		// or if the state was discarded while speculation was running.
		if e.gen != gen || e.state == StateDiscarded {
			e.mu.Unlock()
			return
		}
		e.state = StateReady
		e.result = res.r
		onStateChange := e.OnStateChange
		e.mu.Unlock()

		if onStateChange != nil {
			onStateChange(StateRunning, StateReady)
		}
	}
}

// ReadOnlyTools is the set of tool names safe for speculative execution.
var ReadOnlyTools = map[string]bool{
	"read": true, "read_file": true, "file_read": true, "FileRead": true,
	"grep": true, "search": true, "file_search": true, "Grep": true,
	"glob": true, "file_glob": true, "Glob": true,
	"lsp": true, "LSP": true,
	"list": true, "ls": true,
}
