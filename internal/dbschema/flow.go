package dbschema

import (
	"context"
	"database/sql"
)

// FlowStates is the independent daemon flow-state store, not agent_flows.
func FlowStates(ctx context.Context, db *sql.DB) error {
	return apply(ctx, db, migrationGroup{"flow_states", []string{
		`CREATE TABLE IF NOT EXISTS flow_states(id TEXT PRIMARY KEY,session_id TEXT NOT NULL DEFAULT '',state JSONB NOT NULL DEFAULT '{}',updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE INDEX IF NOT EXISTS idx_flow_states_session ON flow_states(session_id)`,
	}})
}
