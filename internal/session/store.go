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
