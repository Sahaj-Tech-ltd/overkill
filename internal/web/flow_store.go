package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var errFlowConflict = errors.New("flow revision or state conflict")

// FlowStore persists managed workflow records, separately from suspended
// agent continuation snapshots. Updating a record does not execute an agent.
type FlowStore struct{ db *sql.DB }

func NewFlowStore(db *sql.DB) *FlowStore { return &FlowStore{db: db} }

// Migrate is called after session migration by the web entry point.
func (s *FlowStore) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("web task flows: database required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || 'web:task-flows',0))`); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS web_task_flows (
	 id TEXT PRIMARY KEY, session_key TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	 controller_id TEXT NOT NULL, revision BIGINT NOT NULL CHECK (revision > 0),
	 status TEXT NOT NULL CHECK (status IN ('running','waiting','blocked','finished','failed','cancelled')),
	 goal TEXT NOT NULL, current_step TEXT NOT NULL DEFAULT '',
	 state_json JSONB NOT NULL DEFAULT '{}', wait_json JSONB NOT NULL DEFAULT '{}',
	 blocked_task_id TEXT NOT NULL DEFAULT '', blocked_summary TEXT NOT NULL DEFAULT '',
	 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	if err != nil {
		return fmt.Errorf("web task flows: migrate: %w", err)
	}
	_, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS web_task_flows_session_created ON web_task_flows(session_key, created_at DESC, id DESC)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const flowColumns = `id,session_key,controller_id,revision,status,goal,current_step,state_json::text,wait_json::text,blocked_task_id,blocked_summary,created_at,updated_at`

func scanFlow(row interface{ Scan(...any) error }) (flowRow, error) {
	var f flowRow
	err := row.Scan(&f.ID, &f.SessionKey, &f.ControllerID, &f.Revision, &f.Status, &f.Goal, &f.CurrentStep, &f.StateJSON, &f.WaitJSON, &f.BlockedTaskID, &f.BlockedSummary, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func (s *FlowStore) create(ctx context.Context, f flowRow) (flowRow, error) {
	return scanFlow(s.db.QueryRowContext(ctx, `INSERT INTO web_task_flows(id,session_key,controller_id,revision,status,goal,state_json,wait_json) VALUES($1,$2,$3,1,CASE WHEN $6::jsonb='{}'::jsonb THEN 'running' ELSE 'waiting' END,$4,$5::jsonb,$6::jsonb) RETURNING `+flowColumns, f.ID, f.SessionKey, f.ControllerID, f.Goal, f.StateJSON, f.WaitJSON))
}

func (s *FlowStore) get(ctx context.Context, sid, id string) (flowRow, error) {
	return scanFlow(s.db.QueryRowContext(ctx, `SELECT `+flowColumns+` FROM web_task_flows WHERE session_key=$1 AND id=$2`, sid, id))
}

func (s *FlowStore) list(ctx context.Context, sid string, limit, offset int) ([]flowRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+flowColumns+` FROM web_task_flows WHERE session_key=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, sid, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]flowRow, 0)
	for rows.Next() {
		f, err := scanFlow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

type flowPatch struct {
	ExpectedRevision int64   `json:"expectedRevision"`
	Status           *string `json:"status,omitempty"`
	CurrentStep      *string `json:"currentStep,omitempty"`
	StateJSON        *string `json:"stateJson,omitempty"`
	WaitJSON         *string `json:"waitJson,omitempty"`
	BlockedTaskID    *string `json:"blockedTaskId,omitempty"`
	BlockedSummary   *string `json:"blockedSummary,omitempty"`
}

func (s *FlowStore) apply(ctx context.Context, sid, id, action string, patch flowPatch) (flowRow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return flowRow{}, err
	}
	defer tx.Rollback()
	f, err := scanFlow(tx.QueryRowContext(ctx, `SELECT `+flowColumns+` FROM web_task_flows WHERE session_key=$1 AND id=$2 FOR UPDATE`, sid, id))
	if err != nil {
		return f, err
	}
	if f.Revision != patch.ExpectedRevision || f.Status == "finished" || f.Status == "failed" || f.Status == "cancelled" {
		return f, errFlowConflict
	}
	switch action {
	case "resume":
		if f.Status != "waiting" && f.Status != "blocked" {
			return f, errFlowConflict
		}
		f.Status = "running"
	case "finish":
		f.Status = "finished"
	case "fail":
		f.Status = "failed"
	case "cancel":
		f.Status = "cancelled"
	case "tasks":
		if patch.Status != nil {
			f.Status = *patch.Status
		}
	default:
		return f, fmt.Errorf("unknown flow action")
	}
	if patch.CurrentStep != nil {
		f.CurrentStep = *patch.CurrentStep
	}
	if patch.StateJSON != nil {
		f.StateJSON = *patch.StateJSON
	}
	if patch.WaitJSON != nil {
		f.WaitJSON = *patch.WaitJSON
	}
	if patch.BlockedTaskID != nil {
		f.BlockedTaskID = *patch.BlockedTaskID
	}
	if patch.BlockedSummary != nil {
		f.BlockedSummary = *patch.BlockedSummary
	}
	if action == "tasks" && f.Status == "running" {
		if (patch.WaitJSON != nil && !emptyFlowWait(*patch.WaitJSON)) || (patch.BlockedTaskID != nil && *patch.BlockedTaskID != "") || (patch.BlockedSummary != nil && *patch.BlockedSummary != "") {
			return f, errFlowConflict
		}
		f.WaitJSON, f.BlockedTaskID, f.BlockedSummary = "{}", "", ""
	}
	if action == "tasks" && f.Status == "waiting" {
		if (patch.BlockedTaskID != nil && *patch.BlockedTaskID != "") || (patch.BlockedSummary != nil && *patch.BlockedSummary != "") {
			return f, errFlowConflict
		}
		f.BlockedTaskID, f.BlockedSummary = "", ""
	}
	if action == "resume" || f.Status == "finished" || f.Status == "failed" || f.Status == "cancelled" {
		f.WaitJSON, f.BlockedTaskID, f.BlockedSummary = "{}", "", ""
	}
	if f.Status != "blocked" && (f.BlockedTaskID != "" || f.BlockedSummary != "") {
		return f, errFlowConflict
	}
	f, err = scanFlow(tx.QueryRowContext(ctx, `UPDATE web_task_flows SET revision=revision+1,status=$3,current_step=$4,state_json=$5::jsonb,wait_json=$6::jsonb,blocked_task_id=$7,blocked_summary=$8,updated_at=clock_timestamp() WHERE session_key=$1 AND id=$2 RETURNING `+flowColumns, sid, id, f.Status, f.CurrentStep, f.StateJSON, f.WaitJSON, f.BlockedTaskID, f.BlockedSummary))
	if err != nil {
		return f, err
	}
	if err := tx.Commit(); err != nil {
		return f, err
	}
	return f, nil
}
