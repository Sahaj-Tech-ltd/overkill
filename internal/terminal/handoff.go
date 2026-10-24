package terminal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Handoff records ownership, not a promise that the process is still healthy.
// The original lifetime, reservation, task lease and cancellation policy stay
// in force. It is never an instruction to replay or adopt a PID after restart.
type Handoff struct {
	Initiator        string    `json:"initiator"`
	ID               string    `json:"id"`
	TerminalID       string    `json:"terminal_id"`
	From             Owner     `json:"from"`
	To               Owner     `json:"to"`
	PreviousRevision uint64    `json:"previous_revision"`
	ControlRevision  uint64    `json:"control_revision"`
	Cursor           uint64    `json:"cursor"`
	State            string    `json:"state"`
	CreatedAt        time.Time `json:"created_at"`
}
type TransferResult struct {
	Handoff  Handoff `json:"handoff"`
	Terminal Info    `json:"terminal"`
	Success  bool    `json:"success"`
	Error    string  `json:"error,omitempty"`
}
type transferAttempt struct {
	actor    Owner
	human    bool
	revision uint64
	result   TransferResult
}

func (m *Manager) HandoffAvailable(owner Owner) bool {
	target := m.options.ParentOwner
	return target.valid() && owner.Session == target.Session && owner.Worker != target.Worker && m.options.RecordHandoff != nil
}

func (m *Manager) recordHandoff(ctx context.Context, h Handoff) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return m.options.RecordHandoff(ctx, h)
}

// HandoffToParent has exactly one application-configured destination. Model
// input cannot select another worker/session, renew its grant or its lifetime.
func (m *Manager) HandoffToParent(ctx context.Context, actor Owner, id string, human bool, revision uint64) (TransferResult, error) {
	if revision == 0 || !actor.valid() {
		return TransferResult{}, ErrControlChanged
	}
	m.mu.Lock()
	e := m.entries[id]
	m.mu.Unlock()
	if e == nil {
		return TransferResult{}, ErrNotFound
	}
	e.input.Lock()
	defer e.input.Unlock()
	if prior := e.transfer; prior != nil && prior.actor == actor && prior.human == human && prior.revision == revision {
		return prior.result, nil
	}
	if err := e.authorized(actor, human); err != nil {
		return TransferResult{}, err
	}
	if err := e.validateReservation(ctx); err != nil {
		return TransferResult{}, err
	}
	e.mu.Lock()
	info := e.snapshot()
	end := e.base + uint64(len(e.output))
	e.mu.Unlock()
	if !m.HandoffAvailable(info.Owner) {
		return TransferResult{}, fmt.Errorf("terminal: no admitted parent handoff for this owner")
	}
	if info.State != "running" {
		return TransferResult{}, ErrClosed
	}
	if info.ControlRevision != revision {
		return TransferResult{}, ErrControlChanged
	}
	if !human && info.Controller == "human" {
		return TransferResult{}, ErrHumanControl
	}
	initiator := "agent"
	if human {
		initiator = "human"
	}
	h := Handoff{Initiator: initiator, ID: uuid.NewString(), TerminalID: id, From: info.Owner, To: m.options.ParentOwner, PreviousRevision: revision, ControlRevision: revision + 1, Cursor: end, State: "prepared", CreatedAt: time.Now().UTC()}
	if err := m.recordHandoff(ctx, h); err != nil {
		return TransferResult{}, fmt.Errorf("terminal: handoff intent not confirmed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return TransferResult{}, err
	}
	e.mu.Lock()
	if e.info.State != "running" {
		e.mu.Unlock()
		return TransferResult{}, ErrClosed
	}
	e.info.Owner = h.To
	e.info.CanHandoff = false
	e.info.ControlRevision = h.ControlRevision
	e.info.InputSequence = 0
	e.controlFrom = 0
	e.changed()
	info = e.snapshot()
	e.mu.Unlock()
	h.State = "applied"
	result := TransferResult{Handoff: h, Terminal: info, Success: true}
	// Ownership already changed. Record it even if the launching turn was
	// cancelled at this boundary, without extending any original authority.
	if err := m.recordHandoff(context.WithoutCancel(ctx), h); err != nil {
		result.Handoff.State = "unconfirmed"
		result.Success = false
		result.Error = "Ownership moved to the parent, but its durable receipt is unconfirmed; inspect the terminal and handoff journal."
	}
	e.transfer = &transferAttempt{actor: actor, human: human, revision: revision, result: result}
	return result, nil
}

// Child cleanup may race a human handoff. It closes only a terminal that still
// belongs to this exact child after acquiring the input/ownership lock.
func (m *Manager) closeOwned(owner Owner, id string) error {
	m.mu.Lock()
	e := m.entries[id]
	m.mu.Unlock()
	if e == nil {
		return nil
	}
	e.input.Lock()
	defer e.input.Unlock()
	if e.authorized(owner, false) != nil {
		return nil
	}
	e.close()
	select {
	case <-e.done:
		e.mu.Lock()
		message := e.info.CleanupError
		e.mu.Unlock()
		if message != "" {
			return fmt.Errorf("%s", message)
		}
		return nil
	case <-time.After(3 * time.Second):
		return fmt.Errorf("terminal: process termination not confirmed")
	}
}
