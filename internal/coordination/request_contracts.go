package coordination

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func requiredContractsJSON(pins []ContractDependency) []byte {
	if len(pins) == 0 {
		return []byte(`[]`)
	}
	data, _ := json.Marshal(pins)
	return data
}
func validateRequiredContracts(in SendInput) error {
	if len(in.RequiredContracts) > 32 || (len(in.RequiredContracts) > 0 && in.Kind != "request") {
		return fmt.Errorf("coordination: only requests may require at most 32 contracts")
	}
	seen := map[string]bool{}
	for _, p := range in.RequiredContracts {
		id, e := uuid.Parse(p.ContractID)
		if e != nil || id.String() != p.ContractID || p.Revision <= 0 || seen[p.ContractID] {
			return fmt.Errorf("coordination: required contracts need distinct canonical IDs and positive revisions")
		}
		seen[p.ContractID] = true
	}
	return nil
}
func lockContractProject(ctx context.Context, tx *sql.Tx, project string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:contracts:' || $1,0))`, project)
	return err
}

// Agreement readiness never asserts tests, execution, or artifact acceptance.
func requiredContractBlockers(ctx context.Context, tx *sql.Tx, project, recipient string, pins []ContractDependency) ([]ContractBlocker, bool, error) {
	blockers := []ContractBlocker{}
	fatal := false
	for _, pin := range pins {
		c, err := contractInProject(ctx, tx, project, pin.ContractID)
		if err == ErrNotFound {
			blockers = append(blockers, ContractBlocker{ContractID: pin.ContractID, Reason: "required_contract_deleted", ExpectedRevision: pin.Revision})
			fatal = true
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if c.Revision != pin.Revision {
			blockers = append(blockers, ContractBlocker{ContractID: pin.ContractID, Reason: "required_contract_revision_changed", ExpectedRevision: pin.Revision, ActualRevision: c.Revision})
			fatal = true
			continue
		}
		readiness, err := contractReadinessTx(ctx, tx, project, recipient, pin.ContractID)
		if err != nil {
			return nil, false, err
		}
		for _, b := range readiness.Blockers {
			if b.Reason == "dependency_revision_changed" {
				fatal = true
			}
			blockers = append(blockers, b)
		}
	}
	return blockers, fatal, nil
}

// Claims take admission, peer, then contract locks. Contract mutation and effect
// guards never acquire admission; the contract lock is released before inference.
func claimContractReady(ctx context.Context, tx *sql.Tx, peer Peer) (Message, int, error) {
	if err := lockContractProject(ctx, tx, peer.ProjectID); err != nil {
		return Message{}, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages m WHERE to_peer=$1 AND project_id=$2 AND state='accepted' AND deadline>NOW() ORDER BY created_at,id LIMIT 128 FOR UPDATE SKIP LOCKED`, peer.ID, peer.ProjectID)
	if err != nil {
		return Message{}, 0, err
	}
	messages := []Message{}
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return Message{}, 0, e
		}
		messages = append(messages, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Message{}, 0, err
	}
	changed := 0
	for _, m := range messages {
		blockers, fatal, err := requiredContractBlockers(ctx, tx, peer.ProjectID, peer.ID, m.RequiredContracts)
		if err != nil {
			return Message{}, changed, err
		}
		if fatal {
			if err := failRequestContracts(ctx, tx, peer, m, blockers); err != nil {
				return Message{}, changed, err
			}
			changed++
			continue
		}
		if len(blockers) == 0 {
			var pending bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jsonb_array_elements_text($1::jsonb) d(id) LEFT JOIN coordination_messages p ON p.id=d.id AND p.project_id=$2 WHERE p.id IS NULL OR p.state<>'completed')`, dependenciesJSON(m.DependsOn), peer.ProjectID).Scan(&pending); err != nil {
				return Message{}, changed, err
			}
			if pending {
				continue
			}
			return m, changed, nil
		}
	}
	return Message{}, changed, ErrNotFound
}
func failRequestContracts(ctx context.Context, tx *sql.Tx, peer Peer, m Message, blockers []ContractBlocker) error {
	m, err := scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='failed',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, m.ID))
	if err != nil {
		return err
	}
	data, _ := json.Marshal(struct {
		Reason   string            `json:"reason"`
		Blockers []ContractBlocker `json:"contract_blockers"`
	}{"required_contract_failed", blockers})
	if err = appendEvent(ctx, tx, m, "failed", data); err != nil {
		return err
	}
	return appendEscalation(ctx, tx, Peer{ID: m.From, ProjectID: m.ProjectID}, peer, m.ID, m.ConversationID, "required_contract_failed")
}
func validateExecutionContracts(ctx context.Context, tx *sql.Tx, p Peer) error {
	lineage, ok := ctx.Value(executionContextKey{}).(executionLineage)
	if !ok {
		return nil
	}
	if lineage.lease.PeerID != p.ID {
		return ErrUnauthorized
	}
	if err := checkLease(ctx, tx, p.ID, lineage.lease.OwnerID); err != nil {
		return err
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1 AND to_peer=$2 AND project_id=$3`, lineage.messageID, p.ID, p.ProjectID))
	if err != nil {
		return err
	}
	if m.State != Started {
		return ErrConflict
	}
	blockers, _, err := requiredContractBlockers(ctx, tx, p.ProjectID, p.ID, m.RequiredContracts)
	if err != nil {
		return err
	}
	if len(blockers) > 0 {
		reasons := []string{}
		for _, b := range blockers {
			reasons = append(reasons, b.ContractID+":"+b.Reason)
		}
		return fmt.Errorf("%w: %s", ErrRequestContracts, strings.Join(reasons, ", "))
	}
	return nil
}

// enforceRequestContracts runs under admission/message locking before protocol
// completion or explicit resume. A stale pin fails; a temporarily missing
// acknowledgement blocks for deliberate recovery, without publishing an answer.
func enforceRequestContracts(ctx context.Context, tx *sql.Tx, m Message) error {
	if len(m.RequiredContracts) == 0 {
		return nil
	}
	if err := lockContractProject(ctx, tx, m.ProjectID); err != nil {
		return err
	}
	blockers, fatal, err := requiredContractBlockers(ctx, tx, m.ProjectID, m.To, m.RequiredContracts)
	if err != nil {
		return err
	}
	if len(blockers) == 0 {
		return nil
	}
	peer := Peer{ID: m.To, ProjectID: m.ProjectID}
	if fatal {
		if err := failRequestContracts(ctx, tx, peer, m, blockers); err != nil {
			return err
		}
	} else {
		m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='blocked',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, m.ID))
		if err != nil {
			return err
		}
		data, _ := json.Marshal(struct {
			Reason   string            `json:"reason"`
			Blockers []ContractBlocker `json:"contract_blockers"`
		}{"required_contract_waiting", blockers})
		if err = appendEvent(ctx, tx, m, "blocked", data); err != nil {
			return err
		}
		if err = appendEscalation(ctx, tx, Peer{ID: m.From, ProjectID: m.ProjectID}, peer, m.ID, m.ConversationID, "required_contract_waiting"); err != nil {
			return err
		}
	}
	return ErrRequestContracts
}

// ReconcileExecutionContracts records agreement invalidation even when a run
// ended with a tool error, cancellation, or another failure rather than output.
// Existing blocked/terminal decisions are preserved; repeated finalization does
// not duplicate their events or review escalations.
func (s *Store) ReconcileExecutionContracts(ctx context.Context, lease Lease, messageID string) error {
	if err := validateLease(lease); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockAdmission(ctx, tx, lease.PeerID); err != nil {
		return err
	}
	if _, err = enabledPeer(ctx, tx, lease.PeerID); err != nil {
		return err
	}
	if err = checkLease(ctx, tx, lease.PeerID, lease.OwnerID); err != nil {
		return err
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1 AND to_peer=$2 FOR UPDATE`, messageID, lease.PeerID))
	if err != nil {
		return err
	}
	if m.State == Blocked && len(m.RequiredContracts) > 0 {
		return ErrRequestContracts
	}
	if m.State != Started {
		return nil
	}
	err = enforceRequestContracts(ctx, tx, m)
	if err != nil && err != ErrRequestContracts {
		return err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return commitErr
	}
	return err
}

// Inheritance is application derived after hashing the submitted envelope.
// Model-visible notifications cannot declare pins, but executing notifications
// inherit their causal constraints just like delegated requests.
func inheritRequestContracts(ctx context.Context, tx *sql.Tx, from Peer, parentID string, input *SendInput) error {
	if parentID == "" {
		return nil
	}
	parent, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1 AND project_id=$2`, parentID, from.ProjectID))
	if err != nil {
		return err
	}
	if len(parent.RequiredContracts) == 0 {
		return nil
	}
	if err = lockContractProject(ctx, tx, from.ProjectID); err != nil {
		return err
	}
	if !parent.State.Terminal() {
		if err = enforceRequestContracts(ctx, tx, parent); err != nil {
			return err
		}
	} else {
		blockers, _, err := requiredContractBlockers(ctx, tx, from.ProjectID, parent.To, parent.RequiredContracts)
		if err != nil {
			return err
		}
		if len(blockers) > 0 {
			return ErrRequestContracts
		}
	}
	merged := append([]ContractDependency{}, parent.RequiredContracts...)
	versions := map[string]int64{}
	for _, pin := range merged {
		versions[pin.ContractID] = pin.Revision
	}
	for _, pin := range input.RequiredContracts {
		if version, ok := versions[pin.ContractID]; ok {
			if version != pin.Revision {
				return fmt.Errorf("%w: child cannot replace inherited required contract revision", ErrConflict)
			}
			continue
		}
		versions[pin.ContractID] = pin.Revision
		merged = append(merged, pin)
	}
	if len(merged) > 32 {
		return fmt.Errorf("coordination: inherited and explicit requirements exceed 32 contracts")
	}
	input.RequiredContracts = merged
	return nil
}
