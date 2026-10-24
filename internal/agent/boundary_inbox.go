package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// BoundaryInbox commits delivery after appending messages to this history.
// It runs before a new model request, never in a parallel tool goroutine.
// A commit failure aborts the turn before a model can act on uncertain input.
// The callback is called at most once and must not access the Agent after
// consume returns: the history lock stays held until delivery commits.
type BoundaryInbox func(context.Context, func([]providers.Message) error) error

func (a *Agent) drainBoundaryInbox(ctx context.Context) error {
	if a.boundaryInbox == nil {
		return nil
	}
	if err := a.admitBoundaryInbox(ctx); err != nil {
		return err
	}
	// Correspondence arrives after the turn's ordinary context-budget check.
	// Recheck its actual bytes before sending the enlarged provider request.
	return a.checkBudget(ctx)
}

func (a *Agent) admitBoundaryInbox(ctx context.Context) error {
	a.mu.RLock()
	priorFailure := a.boundaryInboxFailure
	a.mu.RUnlock()
	if priorFailure != nil {
		return priorFailure
	}
	var historyLocked, committed bool
	var historyStart, memoryStart int
	defer func() {
		if historyLocked {
			if !committed {
				// A failed or uncertain delivery commit must not leave an
				// actionable prefix in a later turn. Concurrent Inject calls
				// wait for this decision, so their messages are never removed.
				a.history = a.history[:historyStart]
				a.autoMemoryMessages = memoryStart
			}
			a.mu.Unlock()
		}
	}()
	err := a.boundaryInbox(ctx, func(messages []providers.Message) error {
		if historyLocked {
			return fmt.Errorf("agent: worker correspondence callback was repeated")
		}
		total := 0
		for _, m := range messages {
			total += len(m.Content)
			if m.Role != "user" || len(m.ToolCalls) > 0 || len(m.Attachments) > 0 || total > 128*1024 {
				return fmt.Errorf("agent: invalid or oversized worker correspondence")
			}
		}
		a.mu.Lock()
		historyLocked = true
		historyStart, memoryStart = len(a.history), a.autoMemoryMessages
		for _, m := range messages {
			a.history = append(a.history, m)
			a.autoMemoryMessages++
		}
		return nil
	})
	if historyLocked && errors.Is(err, subagent.ErrMessageAdmissionUncertain) {
		// A later empty inbox cannot prove what happened to the missing
		// commit. Require a fresh runtime and explicit history inspection.
		a.boundaryInboxFailure = err
	}
	committed = err == nil
	return err
}
