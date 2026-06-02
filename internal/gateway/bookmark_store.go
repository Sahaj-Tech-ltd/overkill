// Package gateway — simple PostgreSQL-backed bookmark store for session bookmarks.
package gateway

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Bookmark is a user-defined label pointing to a session.
type Bookmark struct {
	ID        int       `json:"id"`
	SessionID string    `json:"session_id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// BookmarkStore persists session bookmarks in PostgreSQL.
type BookmarkStore struct {
	db *sql.DB
}

// NewBookmarkStore returns a BookmarkStore backed by db.
func NewBookmarkStore(db *sql.DB) (*BookmarkStore, error) {
	s := &BookmarkStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("bookmark: migrate: %w", err)
	}
	return s, nil
}

func (s *BookmarkStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS bookmarks (
			id          SERIAL PRIMARY KEY,
			session_id  TEXT NOT NULL,
			label       TEXT NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	for _, idx := range []string{
		`CREATE INDEX IF NOT EXISTS idx_bookmarks_session ON bookmarks (session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bookmarks_label ON bookmarks (label)`,
	} {
		if _, err := s.db.Exec(idx); err != nil {
			return err
		}
	}
	return nil
}

// Save creates a new bookmark entry.
func (s *BookmarkStore) Save(sessionID, label string) (*Bookmark, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("bookmark: session_id required")
	}
	if label == "" {
		return nil, fmt.Errorf("bookmark: label required")
	}

	var b Bookmark
	err := s.db.QueryRow(`
		INSERT INTO bookmarks (session_id, label) VALUES ($1, $2)
		RETURNING id, session_id, label, created_at
	`, sessionID, label).Scan(&b.ID, &b.SessionID, &b.Label, &b.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("bookmark: save: %w", err)
	}
	return &b, nil
}

// List returns all bookmarks, newest first.
func (s *BookmarkStore) List() ([]Bookmark, error) {
	rows, err := s.db.Query(`SELECT id, session_id, label, created_at FROM bookmarks ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("bookmark: list: %w", err)
	}
	defer rows.Close()

	var out []Bookmark
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.SessionID, &b.Label, &b.CreatedAt); err != nil {
			continue
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListBySession returns bookmarks for a specific session.
func (s *BookmarkStore) ListBySession(sessionID string) ([]Bookmark, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, label, created_at FROM bookmarks WHERE session_id = $1 ORDER BY created_at DESC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("bookmark: list by session: %w", err)
	}
	defer rows.Close()

	var out []Bookmark
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.SessionID, &b.Label, &b.CreatedAt); err != nil {
			continue
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Delete removes a bookmark by ID.
func (s *BookmarkStore) Delete(id int) error {
	result, err := s.db.Exec(`DELETE FROM bookmarks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("bookmark: delete: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("bookmark: not found")
	}
	return nil
}

// DeleteByLabel removes bookmarks matching a label and session.
func (s *BookmarkStore) DeleteByLabel(sessionID, label string) error {
	_, err := s.db.Exec(`DELETE FROM bookmarks WHERE session_id = $1 AND label = $2`, sessionID, label)
	return err
}
