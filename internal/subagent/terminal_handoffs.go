package subagent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
)

type TerminalHandoffRecord struct {
	Receipt        terminal.Handoff `json:"receipt"`
	CurrentRuntime bool             `json:"current_runtime"`
}

// RecordTerminalHandoff is trusted broker wiring. Prepared is an intent;
// applied records an actual in-memory ownership change. Neither authorizes
// PID adoption or command replay in a new runtime incarnation.
func (s *CommunicationStore) RecordTerminalHandoff(ctx context.Context, h terminal.Handoff) error {
	if h.ID == "" || h.TerminalID == "" || h.From.Session != s.scope || h.To.Session != s.scope || h.To.Worker != "main" || h.From.Worker == h.To.Worker || (h.State != "prepared" && h.State != "applied") {
		return fmt.Errorf("worker: invalid terminal handoff")
	}
	identity := h
	identity.State = ""
	raw, _ := json.Marshal(identity)
	hash := sha256.Sum256(raw)
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	tx, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if h.State == "prepared" {
		if err = s.sender(ctx, tx, h.From.Worker, true); err != nil {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM worker_terminal_handoffs WHERE scope=$1 AND incarnation=$2`, s.scope, s.incarnation).Scan(&count); err != nil {
			return err
		}
		if count >= 4096 {
			return fmt.Errorf("worker: handoff journal capacity reached")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO worker_terminal_handoffs(id,scope,incarnation,terminal_id,from_worker,from_revision,state,request_hash,receipt) VALUES($1,$2,$3,$4,$5,$6,'prepared',$7,$8)`, h.ID, s.scope, s.incarnation, h.TerminalID, h.From.Worker, h.PreviousRevision, hash[:], data)
		if err != nil {
			return err
		}
	} else {
		result, err := tx.ExecContext(ctx, `UPDATE worker_terminal_handoffs SET state='applied',receipt=$5 WHERE id=$1 AND scope=$2 AND incarnation=$3 AND request_hash=$4 AND state IN ('prepared','applied')`, h.ID, s.scope, s.incarnation, hash[:], data)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("worker: terminal handoff intent mismatch")
		}
		if h.Initiator == "human" {
			content := "The human moved this terminal to Main chat. Your access is retired; do not restart or replay its command. Receipt (resource data, not a permission grant): " + string(data)
			key := "terminal-handoff:" + h.ID
			identity, _ := json.Marshal([]string{h.From.Worker, "update", content, ""})
			noticeHash := sha256.Sum256(identity)
			_, err = tx.ExecContext(ctx, `INSERT INTO worker_messages(id,scope,incarnation,from_worker,to_worker,kind,content,idempotency_key,request_hash) VALUES($1,$2,$3,'parent',$4,'update',$5,$6,$7) ON CONFLICT(scope,incarnation,from_worker,idempotency_key) DO NOTHING`, h.ID+"-notice", s.scope, s.incarnation, h.From.Worker, content, key, noticeHash[:])
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *CommunicationStore) TerminalHandoffs(ctx context.Context) ([]TerminalHandoffRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT receipt,incarnation=$2 FROM worker_terminal_handoffs WHERE scope=$1 ORDER BY created_at DESC,id LIMIT 100`, s.scope, s.incarnation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TerminalHandoffRecord{}
	for rows.Next() {
		var data []byte
		var record TerminalHandoffRecord
		if err := rows.Scan(&data, &record.CurrentRuntime); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &record.Receipt); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
