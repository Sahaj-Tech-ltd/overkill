package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
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
	if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
		return nil, fmt.Errorf("sync/file: mkdir %s: %w", cfg.Path, err)
	}
	return &FileBackend{root: cfg.Path}, nil
}

func (f *FileBackend) Name() string { return "file" }

func (f *FileBackend) blobPath(id string) string {
	return filepath.Join(f.root, id+".json.gz")
}

func (f *FileBackend) metaPath(id string) string {
	return filepath.Join(f.root, id+".meta.json")
}

func (f *FileBackend) Push(ctx context.Context, id string, data []byte, meta SessionMeta) error {
	if err := os.WriteFile(f.blobPath(id), data, 0o644); err != nil {
		return fmt.Errorf("sync/file: write blob: %w", err)
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("sync/file: marshal meta: %w", err)
	}
	if err := os.WriteFile(f.metaPath(id), mb, 0o644); err != nil {
		return fmt.Errorf("sync/file: write meta: %w", err)
	}
	return nil
}

func (f *FileBackend) Pull(ctx context.Context, id string) ([]byte, SessionMeta, error) {
	data, err := os.ReadFile(f.blobPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, SessionMeta{}, ErrNotFound
		}
		return nil, SessionMeta{}, fmt.Errorf("sync/file: read blob: %w", err)
	}
	mb, err := os.ReadFile(f.metaPath(id))
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
	bErr := os.Remove(f.blobPath(id))
	mErr := os.Remove(f.metaPath(id))
	if bErr != nil && os.IsNotExist(bErr) && mErr != nil && os.IsNotExist(mErr) {
		return ErrNotFound
	}
	return nil
}
