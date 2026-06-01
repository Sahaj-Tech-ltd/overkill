package agent

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"

	_ "github.com/lib/pq"
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

// SuspendedApprover is an ApprovalFunc implementation for remote/async sessions.
type SuspendedApprover struct {
	db        *sql.DB
	ledger    *security.Ledger
	sessionID string
	emitFn    func(event string, payload map[string]any)

	pending sync.Map // callID -> chan Approval
	closed  chan struct{}
	once    sync.Once
}

// NewSuspendedApprover wires all required dependencies.
// db and sessionID are required; ledger and emitFn may be nil (degraded mode).
func NewSuspendedApprover(
	db *sql.DB,
	ledger *security.Ledger,
	sessionID string,
	emitFn func(string, map[string]any),
) (*SuspendedApprover, error) {
	sa := &SuspendedApprover{
		db:        db,
		ledger:    ledger,
		sessionID: sessionID,
		emitFn:    emitFn,
		closed:    make(chan struct{}),
	}
	if err := sa.migrate(); err != nil {
		return nil, fmt.Errorf("suspend: migrate: %w", err)
	}
	return sa, nil
}

func (s *SuspendedApprover) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS suspensions (
			id           TEXT PRIMARY KEY,
			session_id   TEXT NOT NULL,
			tool_name    TEXT NOT NULL DEFAULT '',
			args         TEXT NOT NULL DEFAULT '',
			risk         TEXT NOT NULL DEFAULT '',
			call_data    JSONB NOT NULL DEFAULT '{}',
			suspended_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_suspensions_session ON suspensions (session_id)`)
	return err
}

// Approve is the ApprovalFunc — it persists the request and parks until resolved or timed out.
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
	case <-s.closed:
		s.cleanup(callID)
		return Approval{Allow: false}
	}
}

// ResumeApproval is called by the gateway bridge when the user replies
// "approve <callID>" or "deny <callID>".
func (s *SuspendedApprover) ResumeApproval(callID string, allow bool, approverID string) error {
	val, ok := s.pending.Load(callID)
	if !ok {
		return fmt.Errorf("suspend: unknown or already-resolved callID %q", callID)
	}
	ch, ok := val.(chan Approval)
	if !ok {
		return fmt.Errorf("suspend: corrupt channel for callID %q", callID)
	}

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
func ListPendingSuspensions(db *sql.DB, sessionID string) ([]SuspendedCall, error) {
	rows, err := db.Query(`
		SELECT id, session_id, tool_name, args, risk, suspended_at
		FROM suspensions WHERE session_id = $1 ORDER BY suspended_at
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("suspend: listing pending for session %q: %w", sessionID, err)
	}
	defer rows.Close()

	var out []SuspendedCall
	for rows.Next() {
		var call SuspendedCall
		if err := rows.Scan(&call.CallID, &call.SessionID, &call.ToolName, &call.Args, &call.Risk, &call.Timestamp); err != nil {
			continue
		}
		out = append(out, call)
	}
	return out, rows.Err()
}

func (s *SuspendedApprover) persist(call SuspendedCall) error {
	_, err := s.db.Exec(`
		INSERT INTO suspensions (id, session_id, tool_name, args, risk, suspended_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING
	`, call.CallID, call.SessionID, call.ToolName, call.Args, call.Risk, call.Timestamp)
	if err != nil {
		return fmt.Errorf("suspend: persist: %w", err)
	}
	return nil
}

func (s *SuspendedApprover) cleanup(callID string) {
	s.pending.Delete(callID)
	_, _ = s.db.Exec(`DELETE FROM suspensions WHERE id = $1`, callID)
}

// Close unblocks all in-flight Approve calls so shutdown doesn't leak
// goroutines. After Close, future Approve calls still work but are not
// cancellable (new sessions get a fresh SuspendedApprover).
func (s *SuspendedApprover) Close() {
	s.once.Do(func() { close(s.closed) })
}

func (s *SuspendedApprover) emit(event string, payload map[string]any) {
	if s.emitFn != nil {
		s.emitFn(event, payload)
	}
}
