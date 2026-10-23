package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/dbschema"
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return dbschema.GatewayInputHistory(ctx, ih.db)
}

// Append prepends text to the bounded history. Insert the row before locking
// it so simultaneous first appends serialize just like existing-chat updates.
// The transaction preserves existing data if reading or decoding fails.
func (ih *InputHistory) Append(ctx context.Context, chatKey, text string) error {
	tx, err := ih.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("input_history: begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO input_history (chat_key) VALUES ($1)
		ON CONFLICT (chat_key) DO NOTHING
	`, chatKey); err != nil {
		return fmt.Errorf("input_history: ensure chat %s: %w", chatKey, err)
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT messages FROM input_history WHERE chat_key = $1 FOR UPDATE`, chatKey).Scan(&raw); err != nil {
		return fmt.Errorf("input_history: read %s: %w", chatKey, err)
	}
	entries, err := decodeHistory(raw)
	if err != nil {
		return fmt.Errorf("input_history: decode %s: %w", chatKey, err)
	}
	entries = append([]string{text}, entries...)
	if len(entries) > ih.maxEntries {
		entries = entries[:ih.maxEntries]
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("input_history: marshal %s: %w", chatKey, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE input_history SET messages = $2, updated_at = NOW() WHERE chat_key = $1`, chatKey, data); err != nil {
		return fmt.Errorf("input_history: append %s: %w", chatKey, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("input_history: commit %s: %w", chatKey, err)
	}
	return nil
}

// Get returns the entry at the given offset. Offset 0 = latest.
func (ih *InputHistory) Get(ctx context.Context, chatKey string, offset int) (string, error) {
	entries, err := ih.getEntries(ctx, chatKey)
	if err != nil {
		return "", err
	}
	if offset >= 0 && offset < len(entries) {
		return entries[offset], nil
	}
	return "", nil
}

// Len returns the number of stored entries for the given chat.
func (ih *InputHistory) Len(ctx context.Context, chatKey string) (int, error) {
	entries, err := ih.getEntries(ctx, chatKey)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// GetHistory returns up to limit most-recent messages.
func (ih *InputHistory) GetHistory(ctx context.Context, chatKey string, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	entries, err := ih.getEntries(ctx, chatKey)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	n := limit
	if n > len(entries) {
		n = len(entries)
	}
	result := make([]string, n)
	copy(result, entries[:n])
	return result, nil
}

func (ih *InputHistory) getEntries(ctx context.Context, chatKey string) ([]string, error) {
	var raw []byte
	err := ih.db.QueryRowContext(ctx, `SELECT messages FROM input_history WHERE chat_key = $1`, chatKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("input_history: get %s: %w", chatKey, err)
	}
	entries, err := decodeHistory(raw)
	if err != nil {
		return nil, fmt.Errorf("input_history: unmarshal %s: %w", chatKey, err)
	}
	return entries, nil
}

func decodeHistory(raw []byte) ([]string, error) {
	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, fmt.Errorf("expected a JSON array of messages, got null")
	}
	return entries, nil
}
