package session

import (
	"context"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type Store interface {
	Create(ctx context.Context, session *Session) error
	Load(ctx context.Context, id string) (*Session, error)
	Save(ctx context.Context, session *Session) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, opts ListOptions) ([]*Session, error)
	Close() error
}

type ListOptions struct {
	Folder      string
	Status      string
	ParentID    string
	Limit       int
	Offset      int
	After       time.Time
	OldestFirst bool // Stable updated_at/id ASC instead of DESC; pagination must preserve this order.
}

// Brancher is the optional store capability that supports tree-structured
// sessions. PostgresStore satisfies this.
type Brancher interface {
	Branch(ctx context.Context, parentID string, atTurn int) (*Session, error)
	Clone(ctx context.Context, parentID string) (*Session, error)
	Merge(ctx context.Context, childID string) (*Session, error)
}

// HistoryReader returns an independently owned snapshot of committed messages.
// It must be safe during concurrent turns, writes and deletes, and must not
// expose mutable in-memory agent history or uncommitted database changes.
// Stores without this capability retain exclusive API admission for history.
type HistoryReader interface {
	LoadMessages(ctx context.Context, id string) ([]providers.Message, error)
}
