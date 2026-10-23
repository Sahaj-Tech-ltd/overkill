package coordination

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"

	"github.com/google/uuid"
)

// TransportExecution is a private runtime envelope, never a model tool field or
// response. The random capability is stored only as a hash beside the live lease.
type TransportExecution struct {
	MessageID  string `json:"message_id"`
	OwnerID    string `json:"owner_id"`
	Capability string `json:"capability"`
}
type transportExecutionKey struct{}

func ExecutionTransportProof(ctx context.Context) *TransportExecution {
	lineage, ok := ctx.Value(executionContextKey{}).(executionLineage)
	if !ok || lineage.lease.transportCapability == "" {
		return nil
	}
	return &TransportExecution{MessageID: lineage.messageID, OwnerID: lineage.lease.OwnerID, Capability: lineage.lease.transportCapability}
}
func WithTransportExecution(ctx context.Context, peerID string, proof TransportExecution) (context.Context, error) {
	if _, err := uuid.Parse(proof.MessageID); err != nil {
		return nil, ErrUnauthorized
	}
	if _, err := uuid.Parse(proof.OwnerID); err != nil {
		return nil, ErrUnauthorized
	}
	raw, err := hex.DecodeString(proof.Capability)
	if err != nil || len(raw) != 32 {
		return nil, ErrUnauthorized
	}
	ctx = context.WithValue(ctx, transportExecutionKey{}, true)
	return withExecutionLineage(ctx, Message{ID: proof.MessageID}, Lease{PeerID: peerID, OwnerID: proof.OwnerID, transportCapability: proof.Capability}), nil
}
func checkTransportExecution(ctx context.Context, tx *sql.Tx, peerID string) error {
	lineage, ok := ctx.Value(executionContextKey{}).(executionLineage)
	if !ok || lineage.lease.PeerID != peerID {
		return ErrUnauthorized
	}
	sum := sha256.Sum256([]byte(lineage.lease.transportCapability))
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT owner_id=$2 AND expires_at>clock_timestamp() AND transport_hash=$3 FROM coordination_leases WHERE peer_id=$1 FOR SHARE`, peerID, lineage.lease.OwnerID, sum[:]).Scan(&valid)
	if err != nil {
		return ErrLeaseLost
	}
	if !valid {
		return ErrLeaseLost
	}
	return nil
}
