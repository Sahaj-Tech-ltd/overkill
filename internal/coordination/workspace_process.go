package coordination

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// WorkspaceProcess reserves opaque background effects beyond one tool call.
// Rows deliberately do not expire: a missed heartbeat cannot prove a process
// stopped. Only its owning broker releases after confirmed process cleanup.
// Stale rows after a broker crash remain blocking evidence for reconciliation.
type WorkspaceProcess struct {
	store                 *Store
	peerID, id, workspace string
	lineage               context.Context
}

func workspaceProcessAccess(ctx context.Context, tx *sql.Tx, peer Peer) error {
	rows, err := tx.QueryContext(ctx, `SELECT peer_id,workspace FROM coordination_workspace_processes WHERE project_id=$1`, peer.ProjectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var owner, workspace string
		if err := rows.Scan(&owner, &workspace); err != nil {
			return err
		}
		if owner != peer.ID && workspaceRootsOverlap(peer.Workspace, workspace) {
			return fmt.Errorf("%w: a background terminal reserves this workspace; close it before conflicting work (stale broker reservations require reconciliation)", ErrOwnershipConflict)
		}
	}
	return rows.Err()
}

// ReserveWorkspaceProcess is called by the trusted terminal broker, never by a
// model-facing registration API. Admission and insertion share the same lock
// as synchronous writes and contract changes.
func (s *Store) ReserveWorkspaceProcess(ctx context.Context, peerID, id string) (*WorkspaceProcess, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("coordination: valid process handle required")
	}
	var reserved *WorkspaceProcess
	err := s.WithUnrestrictedWorkspace(ctx, peerID, func(admitted context.Context) error {
		// WithUnrestrictedWorkspace owns a transaction. Use its transaction to
		// avoid releasing admission between validation and persistent insertion.
		tx, ok := admitted.Value(workspaceProcessTxKey{}).(*sql.Tx)
		if !ok {
			return fmt.Errorf("coordination: process admission transaction missing")
		}
		peer, err := enabledPeer(admitted, tx, peerID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(admitted, `INSERT INTO coordination_workspace_processes(id,project_id,peer_id,workspace) VALUES($1,$2,$3,$4)`, id, peer.ProjectID, peer.ID, peer.Workspace)
		if err == nil {
			reserved = &WorkspaceProcess{store: s, peerID: peerID, id: id, workspace: peer.Workspace, lineage: context.WithoutCancel(ctx)}
		}
		return err
	})
	return reserved, err
}

type workspaceProcessTxKey struct{}

func (p *WorkspaceProcess) Check(ctx context.Context) error {
	// Retain the original request's authenticated lineage, with a fresh bounded
	// deadline. A completed/revoked request cannot keep its terminal running.
	ctx = &processCheckContext{Context: ctx, lineage: p.lineage}
	tx, peer, err := p.store.contractTransaction(ctx, p.peerID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateExecutionContracts(ctx, tx, peer); err != nil {
		return err
	}
	if peer.Workspace != p.workspace {
		return ErrUnauthorized
	}
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_workspace_processes WHERE id=$1 AND peer_id=$2 AND workspace=$3)`, p.id, p.peerID, p.workspace).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrOwnershipConflict
	}
	return tx.Commit()
}

type processCheckContext struct {
	context.Context
	lineage context.Context
}

func (c *processCheckContext) Value(key any) any {
	if v := c.lineage.Value(key); v != nil {
		return v
	}
	return c.Context.Value(key)
}

func (p *WorkspaceProcess) Release(ctx context.Context) error {
	// Release must work after peer disable or request cancellation. An opaque
	// instance is held only by the broker that inserted this exact handle.
	_, err := p.store.db.ExecContext(ctx, `DELETE FROM coordination_workspace_processes WHERE id=$1 AND peer_id=$2 AND workspace=$3`, p.id, p.peerID, p.workspace)
	return err
}

func rejectProcessContractMutation(ctx context.Context, tx *sql.Tx, project string) error {
	var held bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_workspace_processes WHERE project_id=$1)`, project).Scan(&held); err != nil {
		return err
	}
	if held {
		return fmt.Errorf("%w: close background terminals before changing project agreements", ErrOwnershipConflict)
	}
	return nil
}
