// Package events provides the completion-event pipeline (§8.7.2).
// A CompletionEvent is emitted once per agent session when the ReAct loop
// exits. Sinks receive events concurrently; the Emitter returns nil if at
// least one sink succeeds.
package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// CompletionEvent is the structured record emitted when a session ends.
type CompletionEvent struct {
	SessionID  string            `json:"session_id"`
	Intent     string            `json:"intent"`    // first user message of the session
	Outcome    string            `json:"outcome"`   // "success" | "failure" | "cancelled"
	Artefacts  []Artefact        `json:"artefacts"` // files written, PRs opened, etc.
	DurationMs int64             `json:"duration_ms"`
	CostUSD    float64           `json:"cost_usd"`
	Errors     []string          `json:"errors,omitempty"`
	EmittedAt  time.Time         `json:"emitted_at"`
	Meta       map[string]string `json:"meta,omitempty"` // channel, profile, etc.
}

// Artefact records one tangible output produced during the session.
type Artefact struct {
	Kind string `json:"kind"` // "file" | "pr" | "commit" | "message"
	Ref  string `json:"ref"`  // path, URL, SHA, etc.
}

// Sink is anything that can receive a CompletionEvent.
type Sink interface {
	Name() string
	Send(ctx context.Context, evt CompletionEvent) error
}

// Emitter fans CompletionEvents out to a set of Sinks concurrently.
type Emitter struct {
	sinks []Sink
}

// NewEmitter returns an Emitter backed by the given sinks. At least one
// sink is required; the caller must ensure the slice is non-nil.
func NewEmitter(sinks ...Sink) *Emitter {
	return &Emitter{sinks: sinks}
}

// Emit delivers evt to every sink in parallel with a 10-second per-sink
// timeout. It returns nil when at least one sink succeeds, and a combined
// error only when every sink fails.
func (e *Emitter) Emit(ctx context.Context, evt CompletionEvent) error {
	if len(e.sinks) == 0 {
		return nil
	}

	type result struct {
		name string
		err  error
	}
	results := make([]result, len(e.sinks))

	var wg sync.WaitGroup
	wg.Add(len(e.sinks))

	for i, s := range e.sinks {
		i, s := i, s
		go func() {
			defer wg.Done()
			sinkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			results[i] = result{name: s.Name(), err: s.Send(sinkCtx, evt)}
		}()
	}
	wg.Wait()

	var errs []error
	for _, r := range results {
		if r.err == nil {
			return nil
		}
		errs = append(errs, fmt.Errorf("sink %s: %w", r.name, r.err))
	}
	return errors.Join(errs...)
}
