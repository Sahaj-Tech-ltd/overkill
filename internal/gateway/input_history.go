package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/lib/pq"
)

const defaultMaxInputHistory = 100

// InputHistory is a per-chat ring buffer of recent user messages stored
// in PostgreSQL. Each chat gets a single row holding a JSON array whose
// element [0] is the most recent message (latest-first ordering).
type InputHistory struct {
	db         *sql.DB
	maxEntries int // ring buffer capacity
}

// NewInputHistory returns an InputHistory backed by db. maxEntries
// defaults to 100. The caller owns the DB lifecycle.
func NewInputHistory(db *sql.DB) (*InputHistory, error) {
	ih := &InputHistory{
		db:         db,
		maxEntries: defaultMaxInputHistory,
	}
	if err := ih.migrate(); err != nil {
		return nil, fmt.Errorf("input_history: migrate: %w", err)
	}
	return ih, nil
}

func (ih *InputHistory) migrate() error {
	_, err := ih.db.Exec(`
		CREATE TABLE IF NOT EXISTS input_history (
			chat_key    TEXT PRIMARY KEY,
			messages    JSONB NOT NULL DEFAULT '[]',
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	return err
}

// Append prepends text to the chat's history ring buffer.
// Uses a DB transaction with SELECT … FOR UPDATE to guard against
// multi-process races — the in-process mutex alone is insufficient.
func (ih *InputHistory) Append(ctx context.Context, chatKey, text string) error {
	tx, err := ih.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("input_history: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Read current entries with row-level lock.
	var entries []string
	err = tx.QueryRow(`SELECT messages FROM input_history WHERE chat_key = $1 FOR UPDATE`, chatKey).Scan(&entries)
	if err == sql.ErrNoRows {
		entries = []string{}
	} else if err != nil {
		var raw []byte
		err2 := tx.QueryRow(`SELECT messages FROM input_history WHERE chat_key = $1 FOR UPDATE`, chatKey).Scan(&raw)
		if err2 != nil {
			entries = []string{}
		} else {
			_ = json.Unmarshal(raw, &entries)
		}
	}

	// Prepend — index 0 is always the latest message
	entries = append([]string{text}, entries...)

	// Ring-buffer trim
	if len(entries) > ih.maxEntries {
		entries = entries[:ih.maxEntries]
	}

	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("input_history: marshal %s: %w", chatKey, err)
	}

	_, err = tx.Exec(`
		INSERT INTO input_history (chat_key, messages, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (chat_key) DO UPDATE SET
			messages = EXCLUDED.messages,
			updated_at = EXCLUDED.updated_at
	`, chatKey, data)
	if err != nil {
		return fmt.Errorf("input_history: append %s: %w", chatKey, err)
	}
	return tx.Commit()
}

// Get returns the entry at the given offset. Offset 0 = latest.
func (ih *InputHistory) Get(_ context.Context, chatKey string, offset int) (string, error) {
	entries, err := ih.getEntries(chatKey)
	if err != nil {
		return "", err
	}
	if offset >= 0 && offset < len(entries) {
		return entries[offset], nil
	}
	return "", nil
}

// Len returns the number of stored entries for the given chat.
func (ih *InputHistory) Len(_ context.Context, chatKey string) (int, error) {
	entries, err := ih.getEntries(chatKey)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// GetHistory returns up to limit most-recent messages.
func (ih *InputHistory) GetHistory(_ context.Context, chatKey string, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	entries, err := ih.getEntries(chatKey)
	if err != nil {
		return nil, err
	}
	n := limit
	if n > len(entries) {
		n = len(entries)
	}
	result := make([]string, n)
	copy(result, entries[:n])
	return result, nil
}

func (ih *InputHistory) getEntries(chatKey string) ([]string, error) {
	var raw []byte
	err := ih.db.QueryRow(`SELECT messages FROM input_history WHERE chat_key = $1`, chatKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("input_history: get %s: %w", chatKey, err)
	}
	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("input_history: unmarshal %s: %w", chatKey, err)
	}
	return entries, nil
}
