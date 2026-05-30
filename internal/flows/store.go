package flows

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

// Store is the Postgres-backed flow contribution registry. Provider flows
// are dynamic — plugins register themselves at runtime. Channel setup flows
// are hybrid — bundled channels ship with core, installable channels come
// from a catalog.
type Store struct {
	db *sql.DB
}

// NewStore wires the flow store to an existing Postgres connection pool.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Migrate creates the flow_contributions table if it doesn't exist.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS flow_contributions (
			id          TEXT PRIMARY KEY,
			kind        TEXT NOT NULL,
			surface     TEXT NOT NULL,
			value       TEXT NOT NULL,
			label       TEXT NOT NULL,
			hint        TEXT,
			group_id    TEXT,
			group_label TEXT,
			source      TEXT NOT NULL,
			plugin_id   TEXT,
			created_at  TIMESTAMPTZ DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("flows: migrate: %w", err)
	}
	return nil
}

// RegisterProvider upserts a provider flow contribution. Plugins call this
// at init time to make themselves available in setup wizards.
func (s *Store) RegisterProvider(ctx context.Context, c FlowContribution) error {
	r := FromContribution(c)
	r.Kind = "provider"
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO flow_contributions
			(id, kind, surface, value, label, hint, group_id, group_label, source, plugin_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
			value=EXCLUDED.value, label=EXCLUDED.label, hint=EXCLUDED.hint,
			group_id=EXCLUDED.group_id, group_label=EXCLUDED.group_label,
			source=EXCLUDED.source, plugin_id=EXCLUDED.plugin_id
	`, r.ID, r.Kind, r.Surface, r.Value, r.Label, r.Hint,
		r.GroupID, r.GroupLabel, r.Source, r.PluginID)
	if err != nil {
		return fmt.Errorf("flows: register provider: %w", err)
	}
	return nil
}

// RegisterChannel upserts a channel flow contribution.
func (s *Store) RegisterChannel(ctx context.Context, c FlowContribution) error {
	r := FromContribution(c)
	r.Kind = "channel"
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO flow_contributions
			(id, kind, surface, value, label, hint, group_id, group_label, source, plugin_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
			value=EXCLUDED.value, label=EXCLUDED.label, hint=EXCLUDED.hint,
			group_id=EXCLUDED.group_id, group_label=EXCLUDED.group_label,
			source=EXCLUDED.source, plugin_id=EXCLUDED.plugin_id
	`, r.ID, r.Kind, r.Surface, r.Value, r.Label, r.Hint,
		r.GroupID, r.GroupLabel, r.Source, r.PluginID)
	if err != nil {
		return fmt.Errorf("flows: register channel: %w", err)
	}
	return nil
}

// ListContributions returns all contributions for a given kind and surface.
func (s *Store) ListContributions(ctx context.Context, kind FlowKind, surface FlowSurface) ([]FlowContribution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, surface, value, label, hint, group_id, group_label, source, plugin_id, created_at
		FROM flow_contributions
		WHERE kind=$1 AND surface=$2
		ORDER BY label
	`, string(kind), string(surface))
	if err != nil {
		return nil, fmt.Errorf("flows: list: %w", err)
	}
	defer rows.Close()

	var out []FlowContribution
	for rows.Next() {
		var r FlowRecord
		if err := rows.Scan(&r.ID, &r.Kind, &r.Surface, &r.Value, &r.Label,
			&r.Hint, &r.GroupID, &r.GroupLabel, &r.Source, &r.PluginID, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("flows: scan: %w", err)
		}
		out = append(out, r.ToContribution())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("flows: rows: %w", err)
	}
	return out, nil
}

// Remove deletes a flow contribution by ID. Used when a plugin is
// uninstalled to clean up its setup entries.
func (s *Store) Remove(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM flow_contributions WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("flows: remove %s: %w", id, err)
	}
	return nil
}

// ListAll returns every flow contribution, newest first.
func (s *Store) ListAll(ctx context.Context) ([]FlowContribution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, surface, value, label, hint, group_id, group_label, source, plugin_id, created_at
		FROM flow_contributions
		ORDER BY created_at DESC, label
	`)
	if err != nil {
		return nil, fmt.Errorf("flows: list all: %w", err)
	}
	defer rows.Close()

	var out []FlowContribution
	for rows.Next() {
		var r FlowRecord
		if err := rows.Scan(&r.ID, &r.Kind, &r.Surface, &r.Value, &r.Label,
			&r.Hint, &r.GroupID, &r.GroupLabel, &r.Source, &r.PluginID, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("flows: scan: %w", err)
		}
		out = append(out, r.ToContribution())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("flows: rows: %w", err)
	}
	return out, nil
}

// EnsureSchema is a convenience that calls Migrate. Exists so the DB
// migration runner has a single entry point for all flow-related tables.
func (s *Store) EnsureSchema(ctx context.Context) error {
	return s.Migrate(ctx)
}
