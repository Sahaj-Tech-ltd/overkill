package terminal

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

var (
	ErrControlChanged = errors.New("terminal: control changed; read fresh terminal state before continuing")
	ErrInputOrder     = errors.New("terminal: input sequence is stale or out of order; inspect current state, do not replay old keys")
	ErrInputConflict  = errors.New("terminal: the same input identity was reused with different bytes")
)

// Revisions fence writes across human/agent/owner changes. A sequence counts
// input attempts within one revision. Only the latest receipt is retained;
// older identities are rejected rather than re-executed. Space stays constant.
type InputReceipt struct {
	ControlRevision uint64 `json:"control_revision"`
	Sequence        uint64 `json:"sequence"`
	Written         int    `json:"written"`
	Outcome         string `json:"outcome"`
	Success         bool   `json:"success"`
	Error           string `json:"error,omitempty"`
}
type inputAttempt struct {
	receipt InputReceipt
	hash    [32]byte
	human   bool
	owner   Owner
}

var ErrOwnershipTransferred = fmt.Errorf("terminal: ownership transferred to the parent; inspect worker updates for the handoff receipt: %w", ErrNotFound)

const maximumProtocolCounter = uint64(1<<53 - 1)

func (e *entry) authorized(owner Owner, human bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !owner.valid() || e.info.Owner.Session != owner.Session || (!human && e.info.Owner != owner) {
		if !human && owner == e.originalOwner && owner.Session == e.info.Owner.Session {
			return ErrOwnershipTransferred
		}
		return ErrNotFound
	}
	return nil
}

// WriteInput never automatically retries a PTY write. A retry of the latest
// matching identity returns its receipt; conflicting/stale identities cannot
// write. Unknown or partial writes close the process to prevent late input.
func (m *Manager) WriteInput(ctx context.Context, owner Owner, id string, human bool, revision, sequence uint64, data []byte) (InputReceipt, error) {
	if revision == 0 || sequence == 0 || revision > maximumProtocolCounter || sequence > maximumProtocolCounter {
		return InputReceipt{}, ErrInputOrder
	}
	if len(data) > 16*1024 {
		return InputReceipt{}, fmt.Errorf("terminal: input exceeds 16 KiB")
	}
	e, err := m.get(owner, id, human)
	if err != nil {
		return InputReceipt{}, err
	}
	e.input.Lock()
	defer e.input.Unlock()
	if err := e.authorized(owner, human); err != nil {
		return InputReceipt{}, err
	}
	hash := sha256.Sum256(data)
	if prior := e.lastInput; prior != nil && prior.human == human && prior.owner == owner && prior.receipt.ControlRevision == revision && prior.receipt.Sequence == sequence {
		if prior.hash != hash {
			return InputReceipt{}, ErrInputConflict
		}
		return prior.receipt, nil
	}
	e.mu.Lock()
	current, previous := e.info.ControlRevision, e.info.InputSequence
	e.mu.Unlock()
	if current != revision {
		return InputReceipt{}, ErrControlChanged
	}
	if sequence != previous+1 {
		return InputReceipt{}, ErrInputOrder
	}
	if err := e.inputAdmission(ctx, owner, human, revision); err != nil {
		return InputReceipt{}, err
	}
	e.mu.Lock()
	e.info.InputSequence = sequence
	e.changed()
	e.mu.Unlock()
	n, writeErr := e.writeBytes(ctx, data)
	receipt := InputReceipt{ControlRevision: revision, Sequence: sequence, Written: n, Outcome: "written", Success: true}
	if writeErr != nil || n != len(data) {
		e.close()
		receipt.Outcome = "unknown"
		receipt.Success = false
		receipt.Error = "Input delivery is uncertain; terminal closed. Inspect effects before sending anything again."
		if writeErr != nil {
			receipt.Error += " " + writeErr.Error()
		}
	}
	e.lastInput = &inputAttempt{receipt: receipt, hash: hash, human: human, owner: owner}
	return receipt, nil
}

func (e *entry) inputAdmission(ctx context.Context, owner Owner, human bool, revision uint64) error {
	if err := e.authorized(owner, human); err != nil {
		return err
	}
	if err := e.validateReservation(ctx); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if revision != 0 && revision != e.info.ControlRevision {
		return ErrControlChanged
	}
	if e.info.State != "running" {
		return ErrClosed
	}
	if (human && e.info.Controller != "human") || (!human && e.info.Controller != "agent") {
		return ErrHumanControl
	}
	return ctx.Err()
}

func (e *entry) writeBytes(ctx context.Context, data []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() { n, err := e.session.Write(data); done <- result{n, err} }()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case out := <-done:
		return out.n, out.err
	case <-ctx.Done():
		e.close()
		return 0, ctx.Err()
	case <-timer.C:
		e.close()
		return 0, fmt.Errorf("terminal: input stalled; process closed to prevent late input")
	}
}

// TakeControlInfo is the client protocol; a retry of the immediately preceding
// transition is harmless. Reclaiming the same controller does not reset input
// sequencing. Tool readers receive the new revision and next input sequence.
func (m *Manager) TakeControlInfo(owner Owner, id string, human bool, revision uint64, release bool) (Info, error) {
	if revision == 0 {
		return Info{}, ErrControlChanged
	}
	return m.takeControl(owner, id, human, revision, release)
}
func (m *Manager) takeControl(owner Owner, id string, human bool, revision uint64, release bool) (Info, error) {
	if !human {
		return Info{}, fmt.Errorf("terminal: only the human client may transfer control")
	}
	e, err := m.get(owner, id, true)
	if err != nil {
		return Info{}, err
	}
	e.input.Lock()
	defer e.input.Unlock()
	if err := e.authorized(owner, true); err != nil {
		return Info{}, err
	}
	if err := e.validateReservation(context.Background()); err != nil {
		return Info{}, err
	}
	desired := "human"
	if release {
		desired = "agent"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.info.State != "running" {
		return Info{}, ErrClosed
	}
	if revision != 0 && revision != e.info.ControlRevision {
		if e.controlFrom == revision && e.info.ControlRevision == revision+1 && e.info.Controller == desired {
			return e.snapshot(), nil
		}
		return Info{}, ErrControlChanged
	}
	if e.info.Controller != desired {
		e.controlFrom = e.info.ControlRevision
		e.info.ControlRevision++
		e.info.InputSequence = 0
		e.info.Controller = desired
		e.changed()
	}
	return e.snapshot(), nil
}

func (m *Manager) ResizeAt(owner Owner, id string, human bool, revision uint64, rows, cols uint16) error {
	if revision == 0 {
		return ErrControlChanged
	}
	return m.resize(owner, id, human, revision, rows, cols)
}
func (m *Manager) CloseAt(owner Owner, id string, human bool, revision uint64) error {
	if revision == 0 {
		return ErrControlChanged
	}
	return m.closeTerminal(owner, id, human, revision)
}
