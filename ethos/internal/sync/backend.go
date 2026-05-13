// Package sync ships local Ethos sessions to a remote backend so the user
// can switch machines and resume. Three backends are provided:
//
//   - file: copy to a shared filesystem path (NFS, syncthing, dropbox)
//   - s3:   any S3-compatible bucket (AWS, R2, B2, MinIO)
//   - git:  push session blobs to a git remote
//
// Sessions are exported as gzipped JSON containing the full Session struct
// (messages, settings, ledger). Conflict resolution is last-write-wins on
// UpdatedAt with the loser preserved as `<id>_conflict-<ts>`.
package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// SessionMeta is the lightweight descriptor stored alongside each blob.
type SessionMeta struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	OriginHost   string    `json:"origin_host"`
	Size         int       `json:"size,omitempty"`
}

// Backend is the abstract sync target. All operations should respect the
// supplied context for cancellation/timeout.
type Backend interface {
	// Push uploads a single session blob + metadata.
	Push(ctx context.Context, sessionID string, data []byte, meta SessionMeta) error
	// Pull fetches a single session blob + metadata.
	Pull(ctx context.Context, sessionID string) ([]byte, SessionMeta, error)
	// List enumerates all session metadata records on the backend.
	List(ctx context.Context) ([]SessionMeta, error)
	// Delete removes both blob and metadata for a session.
	Delete(ctx context.Context, sessionID string) error
	// Name identifies the backend type (file/s3/git) for status output.
	Name() string
}

// ErrNotFound is returned by Pull/Delete when a session is absent.
var ErrNotFound = errors.New("sync: session not found on backend")

// NewBackend constructs a Backend from config. Returns (nil, nil) if sync is
// disabled (cfg.Backend == "").
func NewBackend(cfg config.SyncConfig) (Backend, error) {
	switch cfg.Backend {
	case "":
		return nil, nil
	case "file":
		return NewFileBackend(cfg.File)
	case "s3":
		return NewS3Backend(cfg.S3)
	case "git":
		return NewGitBackend(cfg.Git)
	default:
		return nil, fmt.Errorf("sync: unknown backend %q", cfg.Backend)
	}
}
