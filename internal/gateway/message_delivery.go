package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// MessageDelivery tracks whether a user message was successfully delivered
// to the LLM provider or not. Failed messages are automatically retried
// on the next Handle() call for the same session — no message is ever lost.
type MessageDelivery struct {
	db *sql.DB
}

// NewMessageDelivery creates the message_delivery table and returns the tracker.
func NewMessageDelivery(db *sql.DB) (*MessageDelivery, error) {
	md := &MessageDelivery{db: db}
	if err := md.migrate(); err != nil {
		return nil, fmt.Errorf("message_delivery: migrate: %w", err)
	}
	return md, nil
}

func (md *MessageDelivery) migrate() error {
	_, err := md.db.Exec(`
		CREATE TABLE IF NOT EXISTS message_delivery (
			id           SERIAL PRIMARY KEY,
			session_id   TEXT NOT NULL,
			chat_key     TEXT NOT NULL DEFAULT '',
			message      TEXT NOT NULL,
			status       TEXT NOT NULL DEFAULT 'pending',
			error_msg    TEXT,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			delivered_at TIMESTAMPTZ
		);
		CREATE INDEX IF NOT EXISTS idx_md_session_status
			ON message_delivery (session_id, status);
	`)
	return err
}

// RecordPending stores a message before the provider call. Returns the row ID.
func (md *MessageDelivery) RecordPending(ctx context.Context, sessionID, chatKey, message string) (int64, error) {
	var id int64
	err := md.db.QueryRowContext(ctx,
		`INSERT INTO message_delivery (session_id, chat_key, message, status)
		 VALUES ($1, $2, $3, 'pending') RETURNING id`,
		sessionID, chatKey, message,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("message_delivery: record pending: %w", err)
	}
	return id, nil
}

// MarkDelivered updates a message to delivered.
func (md *MessageDelivery) MarkDelivered(ctx context.Context, id int64) error {
	_, err := md.db.ExecContext(ctx,
		`UPDATE message_delivery SET status = 'delivered', delivered_at = NOW()
		 WHERE id = $1`, id)
	return err
}

// MarkFailed updates a message to failed with the error reason.
func (md *MessageDelivery) MarkFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := md.db.ExecContext(ctx,
		`UPDATE message_delivery SET status = 'failed', error_msg = $2
		 WHERE id = $1`, id, errMsg)
	return err
}

// UndeliveredMessage is a message that was sent but never received a response.
type UndeliveredMessage struct {
	ID        int64
	SessionID string
	Message   string
	ErrorMsg  string
	CreatedAt time.Time
}

// GetUndelivered returns all failed/pending messages for a session,
// ordered oldest-first so retries happen in the original order.
func (md *MessageDelivery) GetUndelivered(ctx context.Context, sessionID string) ([]UndeliveredMessage, error) {
	rows, err := md.db.QueryContext(ctx,
		`SELECT id, session_id, message, COALESCE(error_msg, ''), created_at
		 FROM message_delivery
		 WHERE session_id = $1 AND status = 'failed'
		 ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []UndeliveredMessage
	for rows.Next() {
		var m UndeliveredMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Message, &m.ErrorMsg, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// RetryUndelivered marks all undelivered messages for a session back to
// 'pending' so they get retried on the next Handle call. Returns the
// list of message texts, oldest first.
func (md *MessageDelivery) RetryUndelivered(ctx context.Context, sessionID string) ([]UndeliveredMessage, error) {
	_, err := md.db.ExecContext(ctx,
		`UPDATE message_delivery SET status = 'pending', error_msg = NULL
		 WHERE session_id = $1 AND status = 'failed'`, sessionID)
	if err != nil {
		return nil, err
	}
	return md.GetUndelivered(ctx, sessionID)
}
