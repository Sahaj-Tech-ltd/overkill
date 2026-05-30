package session

import (
	"context"
	"time"
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
	Folder   string
	Status   string
	ParentID string
	Limit    int
	Offset   int
	After    time.Time
}

// Brancher is the optional store capability that supports tree-structured
// sessions. PostgresStore satisfies this.
type Brancher interface {
	Branch(ctx context.Context, parentID string, atTurn int) (*Session, error)
	Clone(ctx context.Context, parentID string) (*Session, error)
	Merge(ctx context.Context, childID string) (*Session, error)
}
