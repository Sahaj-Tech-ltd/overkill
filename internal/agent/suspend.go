package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

const (
	defaultTimeout  = 24 * time.Hour
	highRiskTimeout = 4 * time.Hour
)

// SuspendedCall is the persisted record for a pending approval.
type SuspendedCall struct {
	CallID    string    `json:"call_id"`
	ToolName  string    `json:"tool_name"`
	Args      string    `json:"args"`
	Risk      string    `json:"risk"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
}

func suspendKey(sessionID, callID string) []byte {
	return []byte(fmt.Sprintf("suspend:%s:%s", sessionID, callID))
}

// SuspendedApprover is an ApprovalFunc implementation for remote/async sessions.
// Instead of blocking the terminal, it persists the request, emits a
// needs_approval event, and parks the goroutine until the gateway calls
// ResumeApproval or the timeout fires.
type SuspendedApprover struct {
	db        *badger.DB
	ledger    *security.Ledger
	sessionID string
	emitFn    func(event string, payload map[string]any)

	pending sync.Map // callID -> chan Approval
}

// NewSuspendedApprover wires all required dependencies.
// db and sessionID are required; ledger and emitFn may be nil (degraded mode).
func NewSuspendedApprover(
	db *badger.DB,
	ledger *security.Ledger,
	sessionID string,
	emitFn func(string, map[string]any),
) *SuspendedApprover {
	return &SuspendedApprover{
		db:        db,
		ledger:    ledger,
		sessionID: sessionID,
		emitFn:    emitFn,
	}
}

// Approve is the ApprovalFunc — it is called by the agent before every risky
// tool execution. In a remote context there is no human watching, so we persist
// the request and park until resolved or timed out.
func (s *SuspendedApprover) Approve(toolName, args, risk string) Approval {
	callID := uuid.New().String()

	call := SuspendedCall{
		CallID:    callID,
		ToolName:  toolName,
		Args:      args,
		Risk:      risk,
		SessionID: s.sessionID,
		Timestamp: time.Now().UTC(),
	}

	if err := s.persist(call); err != nil {
		// Fail closed — persist failure means we cannot track this approval.
		return Approval{Allow: false}
	}

	ch := make(chan Approval, 1)
	s.pending.Store(callID, ch)

	s.emit("needs_approval", map[string]any{
		"call_id":    callID,
		"tool_name":  toolName,
		"args":       args,
		"risk":       risk,
		"session_id": s.sessionID,
	})

	timeout := defaultTimeout
	if risk == "high" {
		timeout = highRiskTimeout
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case dec := <-ch:
		s.cleanup(callID)
		return dec
	case <-timer.C:
		s.cleanup(callID)
		return Approval{Allow: false}
	}
}

// ResumeApproval is called by the gateway bridge when the user replies
// "approve <callID>" or "deny <callID>" from Telegram/Discord/WhatsApp.
// It resolves the parked goroutine, cleans up the DB record, and writes
// to the permission ledger.
//
// Returns an error when callID is unknown (already resolved or timed out).
func (s *SuspendedApprover) ResumeApproval(callID string, allow bool, approverID string) error {
	val, ok := s.pending.Load(callID)
	if !ok {
		return fmt.Errorf("suspend: unknown or already-resolved callID %q", callID)
	}
	ch, ok := val.(chan Approval)
	if !ok {
		return fmt.Errorf("suspend: corrupt channel for callID %q", callID)
	}

	// Non-blocking: if the timer beat us here the channel is already drained
	// and the select in Approve has returned. We drop the send silently.
	select {
	case ch <- Approval{Allow: allow}:
	default:
	}

	if s.ledger != nil {
		decision := "deny"
		if allow {
			decision = "allow_once"
		}
		_ = s.ledger.Append(security.LedgerEntry{
			Tool:     callID,
			Args:     fmt.Sprintf("approver=%s", approverID),
			Decision: decision,
		})
	}

	return nil
}

// ListPendingSuspensions returns all persisted pending calls for sessionID.
// Used by the daemon's job-status endpoint.
func ListPendingSuspensions(db *badger.DB, sessionID string) ([]SuspendedCall, error) {
	prefix := []byte(fmt.Sprintf("suspend:%s:", sessionID))
	var out []SuspendedCall

	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			var call SuspendedCall
			readErr := item.Value(func(v []byte) error {
				return json.Unmarshal(v, &call)
			})
			if readErr != nil {
				continue
			}
			out = append(out, call)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("suspend: listing pending for session %q: %w", sessionID, err)
	}
	return out, nil
}

func (s *SuspendedApprover) persist(call SuspendedCall) error {
	data, err := json.Marshal(call)
	if err != nil {
		return fmt.Errorf("suspend: marshal: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(suspendKey(call.SessionID, call.CallID), data)
	})
}

func (s *SuspendedApprover) cleanup(callID string) {
	s.pending.Delete(callID)
	_ = s.db.Update(func(txn *badger.Txn) error {
		err := txn.Delete(suspendKey(s.sessionID, callID))
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil
		}
		return err
	})
}

func (s *SuspendedApprover) emit(event string, payload map[string]any) {
	if s.emitFn != nil {
		s.emitFn(event, payload)
	}
}
