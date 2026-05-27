// Package checkpoint — filesystem checkpoints (master plan §4.8).
//
// Before any destructive write, the agent calls Checkpoint.Snapshot(paths)
// to copy the current contents into ~/.overkill/checkpoints/<id>/. A single
// `overkill rollback <id>` (or the live /rollback slash command) restores the
// snapshot. Checkpoints auto-prune; only the last N are kept per session.
//
// Goals:
//   - tiny — no DB; flat file copies under a content-hashed dir
//   - cheap — only files about to be mutated (<= 1 MiB each)
//   - safe — never overwrites an existing checkpoint id
package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

// Manifest records the contents of one checkpoint.
type Manifest struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Entries   []Entry   `json:"entries"`
}

// Entry is one file captured by the checkpoint. Stored at <root>/<id>/<sha>.
type Entry struct {
	Path    string `json:"path"`   // original absolute path
	Sha256  string `json:"sha256"` // content hash (also the filename in the checkpoint dir)
	Size    int64  `json:"size"`
	Existed bool   `json:"existed"` // false → file did not exist before; rollback removes it
}

// Manager owns the checkpoint root and per-session retention.
type Manager struct {
	mu          sync.Mutex
	root        string
	maxPerSess  int
	maxFileSize int64
}

// NewManager creates a manager rooted at dir (typically ~/.overkill/checkpoints).
// keepPerSession bounds the number of retained checkpoints per session.
func NewManager(dir string, keepPerSession int) (*Manager, error) {
	if dir == "" {
		return nil, errors.New("checkpoint: empty dir")
	}
	if keepPerSession <= 0 {
		keepPerSession = 20
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Manager{
		root:        dir,
		maxPerSess:  keepPerSession,
		maxFileSize: 1 << 20, // 1 MiB
	}, nil
}

// Snapshot copies the current contents of the given paths into a fresh
// checkpoint dir and returns the manifest. Paths that do not exist are
// recorded with Existed=false so Restore can delete them on rollback.
// Files larger than maxFileSize are skipped (recorded but not copied).
func (m *Manager) Snapshot(sessionID, reason string, paths []string) (*Manifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := newID()
	dir := filepath.Join(m.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("checkpoint: mkdir: %w", err)
	}

	man := &Manifest{
		ID:        id,
		SessionID: sessionID,
		Reason:    reason,
		CreatedAt: time.Now().UTC(),
	}

	for _, p := range paths {
		clean := filepath.Clean(p)
		entry := Entry{Path: clean}
		info, err := os.Stat(clean)
		if err != nil {
			if os.IsNotExist(err) {
				man.Entries = append(man.Entries, entry) // Existed=false; nothing to copy
				continue
			}
			return nil, fmt.Errorf("checkpoint: stat %s: %w", clean, err)
		}
		if info.IsDir() {
			continue // skip dirs — checkpoint files only
		}
		if info.Size() > m.maxFileSize {
			entry.Existed = true
			entry.Size = info.Size()
			entry.Sha256 = "" // signal "too large to capture"
			man.Entries = append(man.Entries, entry)
			continue
		}
		raw, err := os.ReadFile(clean)
		if err != nil {
			return nil, fmt.Errorf("checkpoint: read %s: %w", clean, err)
		}
		sum := sha256.Sum256(raw)
		hash := hex.EncodeToString(sum[:])
		out := filepath.Join(dir, hash)
		if err := atomicfile.WriteFile(out, raw, 0o644); err != nil {
			return nil, fmt.Errorf("checkpoint: write %s: %w", out, err)
		}
		entry.Existed = true
		entry.Size = info.Size()
		entry.Sha256 = hash
		man.Entries = append(man.Entries, entry)
	}

	manPath := filepath.Join(dir, "manifest.json")
	mb, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("checkpoint: marshal: %w", err)
	}
	if err := atomicfile.WriteFile(manPath, mb, 0o644); err != nil {
		return nil, fmt.Errorf("checkpoint: write manifest: %w", err)
	}

	if sessionID != "" {
		_ = m.pruneSessionLocked(sessionID)
	}
	return man, nil
}

// Restore loads checkpoint id and writes every captured file back to its
// original path. Files whose Entry.Existed is false are removed (the agent
// created them between the checkpoint and now). Skipped files (Size > limit)
// are noted in the returned skipped slice.
func (m *Manager) Restore(id string) (skipped []string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	man, err := m.loadManifestLocked(id)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(m.root, id)
	for _, e := range man.Entries {
		if !e.Existed {
			// File didn't exist before — remove the post-checkpoint creation.
			if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("checkpoint: rollback remove %s: %w", e.Path, err)
			}
			continue
		}
		if e.Sha256 == "" {
			// Was too large to capture; can't restore precisely.
			skipped = append(skipped, e.Path)
			continue
		}
		src := filepath.Join(dir, e.Sha256)
		buf, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("checkpoint: read snapshot %s: %w", src, err)
		}
		if err := os.MkdirAll(filepath.Dir(e.Path), 0o755); err != nil {
			return nil, fmt.Errorf("checkpoint: mkdir parent for %s: %w", e.Path, err)
		}
		if err := atomicfile.WriteFile(e.Path, buf, 0o644); err != nil {
			return nil, fmt.Errorf("checkpoint: restore %s: %w", e.Path, err)
		}
	}
	return skipped, nil
}

// List returns checkpoints for a session, newest first. Empty session ID
// returns every checkpoint.
func (m *Manager) List(sessionID string) ([]Manifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked(sessionID)
}

func (m *Manager) listLocked(sessionID string) ([]Manifest, error) {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		man, err := m.loadManifestLocked(e.Name())
		if err != nil {
			continue // skip malformed dirs
		}
		if sessionID != "" && man.SessionID != sessionID {
			continue
		}
		out = append(out, *man)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Manager) loadManifestLocked(id string) (*Manifest, error) {
	mp := filepath.Join(m.root, id, "manifest.json")
	raw, err := os.ReadFile(mp)
	if err != nil {
		return nil, err
	}
	var man Manifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return nil, fmt.Errorf("checkpoint: parse manifest: %w", err)
	}
	return &man, nil
}

func (m *Manager) pruneSessionLocked(sessionID string) error {
	mans, err := m.listLocked(sessionID)
	if err != nil {
		return err
	}
	if len(mans) <= m.maxPerSess {
		return nil
	}
	for _, old := range mans[m.maxPerSess:] {
		_ = os.RemoveAll(filepath.Join(m.root, old.ID))
	}
	return nil
}

// newID returns a sortable, URL-safe identifier (timestamp + random suffix).
func newID() string {
	now := time.Now().UTC().Format("20060102-150405.000000")
	now = strings.ReplaceAll(now, ".", "-")
	r := make([]byte, 4)
	_, _ = io.ReadFull(randSource, r)
	return now + "-" + hex.EncodeToString(r)
}

// randSource is overridable for deterministic tests.
var randSource = newRandSource()
