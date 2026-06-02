package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

// FileBackend stores blobs/meta on a shared filesystem path. Each session is
// two files: <id>.json.gz (blob) and <id>.meta.json (metadata).
type FileBackend struct {
	root string
}

func NewFileBackend(cfg config.SyncFileConfig) (*FileBackend, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("sync/file: path is required")
	}
	if err := os.MkdirAll(cfg.Path, 0o750); err != nil {
		return nil, fmt.Errorf("sync/file: mkdir %s: %w", cfg.Path, err)
	}
	return &FileBackend{root: cfg.Path}, nil
}

func (f *FileBackend) Name() string { return "file" }

func (f *FileBackend) blobPath(id string) (string, error) {
	return security.SafePath(f.root, id+".json.gz")
}

func (f *FileBackend) metaPath(id string) (string, error) {
	return security.SafePath(f.root, id+".meta.json")
}

func (f *FileBackend) Push(ctx context.Context, id string, data []byte, meta SessionMeta) error {
	// Write meta FIRST (atomically), then blob (atomically). Pull
	// keys off blob existence, so a crash between the two writes
	// leaves orphan meta but no blob — discoverable via List + GC.
	// The old order (blob then meta) left blobs with no meta, which
	// Pull silently treated as success with an empty SessionMeta —
	// hiding the partial-write entirely.
	//
	// Both writes use atomicfile.WriteFile (temp+rename+fsync) so a
	// crash mid-write never exposes a truncated half-file.
	mb, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("sync/file: marshal meta: %w", err)
	}
	mp, err := f.metaPath(id)
	if err != nil {
		return fmt.Errorf("sync/file: meta path: %w", err)
	}
	if err := atomicfile.WriteFile(mp, mb, 0o600); err != nil {
		return fmt.Errorf("sync/file: write meta: %w", err)
	}
	bp, err := f.blobPath(id)
	if err != nil {
		return fmt.Errorf("sync/file: blob path: %w", err)
	}
	if err := atomicfile.WriteFile(bp, data, 0o600); err != nil {
		// Clean up orphan meta so the next Push retries from a clean
		// state rather than appearing to "already exist".
		_ = os.Remove(mp)
		return fmt.Errorf("sync/file: write blob: %w", err)
	}
	return nil
}

func (f *FileBackend) Pull(ctx context.Context, id string) ([]byte, SessionMeta, error) {
	bp, err := f.blobPath(id)
	if err != nil {
		return nil, SessionMeta{}, fmt.Errorf("sync/file: blob path: %w", err)
	}
	data, err := os.ReadFile(bp)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, SessionMeta{}, ErrNotFound
		}
		return nil, SessionMeta{}, fmt.Errorf("sync/file: read blob: %w", err)
	}
	mp, err := f.metaPath(id)
	if err != nil {
		return data, SessionMeta{ID: id}, nil
	}
	mb, err := os.ReadFile(mp)
	if err != nil {
		return data, SessionMeta{ID: id}, nil
	}
	var meta SessionMeta
	if err := json.Unmarshal(mb, &meta); err != nil {
		return data, SessionMeta{ID: id}, nil
	}
	return data, meta, nil
}

func (f *FileBackend) List(ctx context.Context) ([]SessionMeta, error) {
	entries, err := os.ReadDir(f.root)
	if err != nil {
		return nil, fmt.Errorf("sync/file: list: %w", err)
	}
	var out []SessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		mb, err := os.ReadFile(filepath.Join(f.root, e.Name()))
		if err != nil {
			continue
		}
		var meta SessionMeta
		if err := json.Unmarshal(mb, &meta); err != nil {
			continue
		}
		out = append(out, meta)
	}
	return out, nil
}

func (f *FileBackend) Delete(ctx context.Context, id string) error {
	bp, err := f.blobPath(id)
	if err != nil {
		return fmt.Errorf("sync/file: blob path: %w", err)
	}
	mp, err := f.metaPath(id)
	if err != nil {
		return fmt.Errorf("sync/file: meta path: %w", err)
	}
	bErr := os.Remove(bp)
	mErr := os.Remove(mp)
	if bErr != nil && os.IsNotExist(bErr) && mErr != nil && os.IsNotExist(mErr) {
		return ErrNotFound
	}
	return nil
}
