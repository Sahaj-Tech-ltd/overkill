package coordination

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// ContractReadiness reports protocol agreement, never task execution or shipped
// acceptance. The caller must acknowledge the queried revision; each dependency
// is pinned and acknowledged by its dependent contract owner, recursively.
func (s *Store) ContractReadiness(ctx context.Context, peerID, id string) (ContractReadiness, error) {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return ContractReadiness{}, err
	}
	defer tx.Rollback()
	result, err := contractReadinessTx(ctx, tx, p.ProjectID, p.ID, id)
	if err != nil {
		return ContractReadiness{}, err
	}
	return result, tx.Commit()
}

func contractReadinessTx(ctx context.Context, tx *sql.Tx, project, peerID, id string) (ContractReadiness, error) {
	c, err := contractInProject(ctx, tx, project, id)
	if err != nil {
		return ContractReadiness{}, err
	}
	result := ContractReadiness{ContractID: id, Revision: c.Revision, PeerID: peerID, Ready: true, Blockers: []ContractBlocker{}, Source: "versioned contract acknowledgements and pinned dependency graph; task execution/acceptance unavailable"}
	visited := map[string]bool{}
	var visit func(Contract, string) error
	visit = func(node Contract, ackPeer string) error {
		key := node.ID + ":" + ackPeer
		if visited[key] {
			return nil
		}
		visited[key] = true
		if len(visited) > 2048 {
			return fmt.Errorf("coordination: dependency readiness graph exceeds 2048 nodes")
		}
		var enabled bool
		if err := tx.QueryRowContext(ctx, `SELECT enabled FROM coordination_peers WHERE id=$1`, node.OwnerPeerID).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			result.Blockers = append(result.Blockers, ContractBlocker{ContractID: node.ID, Reason: "owner_disabled"})
		}
		var acknowledged bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_contract_acknowledgements a JOIN coordination_peers p ON p.id=a.peer_id AND p.enabled WHERE a.contract_id=$1 AND a.revision=$2 AND a.peer_id=$3)`, node.ID, node.Revision, ackPeer).Scan(&acknowledged); err != nil {
			return err
		}
		if !acknowledged {
			result.Blockers = append(result.Blockers, ContractBlocker{ContractID: node.ID, Reason: "current_revision_not_acknowledged_by:" + ackPeer, ExpectedRevision: node.Revision})
		}
		for _, dep := range node.Spec.Dependencies {
			upstream, err := contractInProject(ctx, tx, project, dep.ContractID)
			if err != nil {
				return err
			}
			if upstream.Revision != dep.Revision {
				result.Blockers = append(result.Blockers, ContractBlocker{ContractID: upstream.ID, Reason: "dependency_revision_changed", ExpectedRevision: dep.Revision, ActualRevision: upstream.Revision})
			}
			if err := visit(upstream, node.OwnerPeerID); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(c, peerID); err != nil {
		return ContractReadiness{}, err
	}
	result.Ready = len(result.Blockers) == 0
	return result, nil
}

// ContractRevision reads an immutable prior agreement under the same project
// authorization as the current contract, for explicit compatibility review.
func (s *Store) ContractRevision(ctx context.Context, peerID, id string, revision int64) (ContractSpec, error) {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return ContractSpec{}, err
	}
	defer tx.Rollback()
	if _, err := contractInProject(ctx, tx, p.ProjectID, id); err != nil {
		return ContractSpec{}, err
	}
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT spec FROM coordination_contract_revisions WHERE contract_id=$1 AND revision=$2`, id, revision).Scan(&data)
	if err == sql.ErrNoRows {
		return ContractSpec{}, ErrNotFound
	}
	if err != nil {
		return ContractSpec{}, err
	}
	var spec ContractSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return ContractSpec{}, err
	}
	return spec, tx.Commit()
}
