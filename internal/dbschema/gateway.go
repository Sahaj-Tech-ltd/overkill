package dbschema

import (
	"context"
	"database/sql"
)

// GatewayBookmarks and GatewayInputHistory share their owner locks and SQL
// with central bootstrap. GatewayDelivery is owned only by its store.
func GatewayBookmarks(ctx context.Context, db *sql.DB) error {
	return apply(ctx, db, migrationGroup{"gateway_bookmarks", bookmarkStatements})
}

func GatewayInputHistory(ctx context.Context, db *sql.DB) error {
	return apply(ctx, db, migrationGroup{"gateway_input_history", inputHistoryStatements})
}

func GatewayDelivery(ctx context.Context, db *sql.DB) error {
	return apply(ctx, db, migrationGroup{"gateway_delivery", deliveryStatements})
}

var bookmarkStatements = []string{
	`CREATE TABLE IF NOT EXISTS bookmarks (
		id SERIAL PRIMARY KEY, session_id TEXT NOT NULL, label TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	`CREATE INDEX IF NOT EXISTS idx_bookmarks_session ON bookmarks (session_id)`,
	`CREATE INDEX IF NOT EXISTS idx_bookmarks_label ON bookmarks (label)`,
}

var inputHistoryStatements = []string{
	`CREATE TABLE IF NOT EXISTS input_history (
		chat_key TEXT PRIMARY KEY, messages JSONB NOT NULL DEFAULT '[]',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
}

var deliveryStatements = []string{
	`CREATE TABLE IF NOT EXISTS message_delivery (
		id SERIAL PRIMARY KEY, session_id TEXT NOT NULL,
		chat_key TEXT NOT NULL DEFAULT '', message TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending', error_msg TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), delivered_at TIMESTAMPTZ)`,
	`CREATE INDEX IF NOT EXISTS idx_md_session_status ON message_delivery (session_id,status)`,
}
