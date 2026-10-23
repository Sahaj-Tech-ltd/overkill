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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

// Manifest records the contents of one checkpoint.
type Manifest struct {
	Version    int       `json:"version,omitempty"`
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Entries    []Entry   `json:"entries"`
	Scope      string    `json:"scope,omitempty"`
	RecoveryOf string    `json:"recovery_of,omitempty"`
}

// Entry is one file captured by the checkpoint. Stored at <root>/<id>/<sha>.
type Entry struct {
	Path    string `json:"path"`   // original absolute path
	Sha256  string `json:"sha256"` // content hash (also the filename in the checkpoint dir)
	Size    int64  `json:"size"`
	Mode    uint32 `json:"mode,omitempty"`
	Existed bool   `json:"existed"` // false → file did not exist before; rollback removes it
}

// Manager owns the checkpoint root and per-session retention.
type Manager struct {
	mu          sync.Mutex
	root        string
	projectRoot string
	sessionID   string
	maxPerSess  int
	maxFileSize int64
}

// NewManager creates a manager rooted at dir (typically ~/.overkill/checkpoints).
// keepPerSession bounds the number of retained checkpoints per session.
// projectRoot, when non-empty, restricts snapshots and restores to files
// under that path.
func NewManager(dir string, keepPerSession int) (*Manager, error) {
	return NewManagerWithRoot(dir, keepPerSession, "")
}

// NewManagerWithRoot creates a manager with an optional projectRoot.
// When projectRoot is set, all snapshotted and restored paths must be
// within it.
func NewManagerWithRoot(dir string, keepPerSession int, projectRoot string) (*Manager, error) {
	if dir == "" {
		return nil, errors.New("checkpoint: empty dir")
	}
	if keepPerSession <= 0 {
		keepPerSession = 20
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	if projectRoot != "" {
		projectRoot, err = filepath.Abs(projectRoot)
		if err != nil {
			return nil, err
		}
	}
	return &Manager{
		root:        dir,
		projectRoot: projectRoot,
		maxPerSess:  keepPerSession,
		maxFileSize: 1 << 20, // 1 MiB
	}, nil
}

// NewSessionManager preserves the common checkpoint layout while restricting
// snapshots, listing and restore to one owning session and workspace.
func NewSessionManager(dir string, keep int, workspace, sessionID string) (*Manager, error) {
	if sessionID == "" {
		return nil, errors.New("checkpoint: session ID required")
	}
	manager, err := NewManagerWithRoot(dir, keep, workspace)
	if err != nil {
		return nil, err
	}
	manager.sessionID = sessionID
	return manager, nil
}

// Snapshot copies the current contents of the given paths into a fresh
// checkpoint dir and returns the manifest. Paths that do not exist are
// recorded with Existed=false so Restore can delete them on rollback.
// Any unsupported path or size limit fails without publishing a checkpoint.
func (m *Manager) Snapshot(sessionID, reason string, paths []string) (*Manifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked(sessionID, reason, paths, "")
}

// snapshotLocked publishes a manifest only after every requested byte is captured.
// Quotas fail the entire snapshot; callers must never mistake skipped data for recovery.
func (m *Manager) snapshotLocked(sessionID, reason string, paths []string, recoveryOf string) (*Manifest, error) {
	if m.sessionID != "" && sessionID != m.sessionID {
		return nil, errors.New("checkpoint: session mismatch")
	}
	scope := ""
	if len(paths) == 0 {
		var err error
		paths, scope, err = m.workspacePaths("")
		if err != nil {
			return nil, err
		}
	}
	if len(paths) > 4096 {
		return nil, errors.New("checkpoint: workspace exceeds 4096-file recovery limit")
	}
	id := newID()
	dir := filepath.Join(m.root, id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("checkpoint: mkdir: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(dir)
		}
	}()
	man := &Manifest{Version: 2, ID: id, SessionID: sessionID, Reason: reason, CreatedAt: time.Now().UTC(), Scope: scope, RecoveryOf: recoveryOf}
	seen := make(map[string]bool)
	var total int64
	for _, p := range paths {
		root, name, absolute, err := m.openPath(p)
		if err != nil {
			return nil, err
		}
		entry, raw, err := readEntry(root, name, absolute, m.maxFileSize)
		root.Close()
		if err != nil {
			return nil, err
		}
		if seen[absolute] {
			continue
		}
		seen[absolute] = true
		total += int64(len(raw))
		if total > 64<<20 {
			return nil, errors.New("checkpoint: workspace exceeds 64 MiB recovery limit")
		}
		if entry.Existed {
			if err := atomicfile.WriteFile(filepath.Join(dir, entry.Sha256), raw, 0o600); err != nil {
				return nil, err
			}
		}
		man.Entries = append(man.Entries, entry)
	}
	mb, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicfile.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o600); err != nil {
		return nil, err
	}
	if err := syncDirectory(dir); err != nil {
		return nil, err
	}
	if err := syncDirectory(m.root); err != nil {
		return nil, err
	}
	published = true
	if sessionID != "" && recoveryOf == "" {
		_ = m.pruneSessionLocked(sessionID)
	}
	return man, nil
}

// openPath resolves relative names against the immutable selected workspace.
// os.Root enforces containment even if an ancestor is swapped for a symlink.
func (m *Manager) openPath(path string) (*os.Root, string, string, error) {
	clean := filepath.Clean(path)
	base := m.projectRoot
	if base == "" {
		absolute, err := filepath.Abs(clean)
		if err != nil {
			return nil, "", "", err
		}
		clean = absolute
		base = filepath.Dir(absolute)
	} else if !filepath.IsAbs(clean) {
		clean = filepath.Join(base, clean)
	}
	rel, err := filepath.Rel(base, clean)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, "", "", errors.New("checkpoint: path outside selected workspace")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", "", err
	}
	// Reject symlink files and ancestors, including in-workspace aliases: a
	// checkpoint restores the named regular file, never a different alias target.
	part := ""
	for _, element := range strings.Split(rel, string(os.PathSeparator)) {
		part = filepath.Join(part, element)
		info, err := root.Lstat(part)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			root.Close()
			return nil, "", "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, "", "", errors.New("checkpoint: symlink path cannot be protected")
		}
	}
	return root, rel, clean, nil
}

func readEntry(root *os.Root, name, absolute string, limit int64) (Entry, []byte, error) {
	entry := Entry{Path: absolute}
	before, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return entry, nil, nil
	}
	if err != nil {
		return entry, nil, err
	}
	if !before.Mode().IsRegular() {
		return entry, nil, errors.New("checkpoint: only regular files can be protected")
	}
	f, err := root.OpenFile(name, os.O_RDONLY|checkpointOpenFlags, 0)
	if os.IsNotExist(err) {
		return entry, nil, nil
	}
	if err != nil {
		return entry, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return entry, nil, err
	}
	if !info.Mode().IsRegular() {
		return entry, nil, errors.New("checkpoint: only regular files can be protected")
	}
	if info.Size() > limit {
		return entry, nil, errors.New("checkpoint: file exceeds recovery size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return entry, nil, err
	}
	if int64(len(raw)) > limit {
		return entry, nil, errors.New("checkpoint: file exceeds recovery size limit")
	}
	sum := sha256.Sum256(raw)
	entry.Existed = true
	entry.Size = int64(len(raw))
	entry.Sha256 = hex.EncodeToString(sum[:])
	entry.Mode = uint32(info.Mode().Perm())
	return entry, raw, nil
}

// workspacePaths captures Git-visible files (tracked and untracked, excluding
// ignored files) in repositories; nonrepositories capture the regular tree.
// The recorded scope is reused during restore so new files can be removed.
func (m *Manager) workspacePaths(scope string) ([]string, string, error) {
	if m.projectRoot == "" {
		return nil, "", errors.New("checkpoint: whole-workspace snapshot requires selected workspace")
	}
	if scope == "" || scope == "git-visible" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "-C", m.projectRoot, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
		raw, err := cmd.Output()
		if err == nil {
			var paths []string
			for _, name := range strings.Split(string(raw), "\x00") {
				if name != "" {
					path := filepath.Join(m.projectRoot, filepath.FromSlash(name))
					if !isUnderRoot(m.root, path) {
						paths = append(paths, path)
					}
				}
			}
			return paths, "git-visible", nil
		}
		if scope == "git-visible" {
			return nil, "", errors.New("checkpoint: original Git inventory unavailable")
		}
	}
	var paths []string
	err := filepath.WalkDir(m.projectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || path == m.root {
				return filepath.SkipDir
			}
			return nil
		}
		if len(paths) >= 4096 {
			return errors.New("checkpoint: workspace exceeds 4096-file recovery limit")
		}
		paths = append(paths, path)
		return nil
	})
	return paths, "workspace", err
}

// Restore preflights the complete manifest and all content before changing a
// target. A durable checkpoint of the present files is retained first, so even
// a later I/O failure leaves a recovery path for every overwritten/deleted byte.
func (m *Manager) Restore(id string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	man, err := m.loadManifestLocked(id)
	if err != nil {
		return nil, err
	}
	if m.sessionID != "" && man.SessionID != m.sessionID {
		return nil, errors.New("checkpoint: checkpoint belongs to another session")
	}
	type target struct {
		entry Entry
		raw   []byte
	}
	var targets []target
	var total int64
	paths := make([]string, 0, len(man.Entries))
	seen := map[string]bool{}
	for _, e := range man.Entries {
		root, name, absolute, err := m.openPath(e.Path)
		if err != nil {
			return nil, err
		}
		_, _, err = readEntry(root, name, absolute, m.maxFileSize)
		root.Close()
		if err != nil {
			return nil, err
		}
		if seen[absolute] {
			return nil, errors.New("checkpoint: duplicate manifest path")
		}
		seen[absolute] = true
		var raw []byte
		if e.Existed {
			if len(e.Sha256) != 64 {
				return nil, errors.New("checkpoint: uncaptured or invalid snapshot content")
			}
			if _, err := hex.DecodeString(e.Sha256); err != nil {
				return nil, errors.New("checkpoint: invalid content hash")
			}
			blobRoot, err := os.OpenRoot(filepath.Join(m.root, id))
			if err != nil {
				return nil, err
			}
			blob, openErr := blobRoot.OpenFile(e.Sha256, os.O_RDONLY|checkpointOpenFlags, 0)
			if openErr == nil {
				info, statErr := blob.Stat()
				if statErr != nil || !info.Mode().IsRegular() {
					blob.Close()
					blobRoot.Close()
					return nil, errors.New("checkpoint: invalid content file")
				}
				raw, err = io.ReadAll(io.LimitReader(blob, m.maxFileSize+1))
				blob.Close()
			} else {
				err = openErr
			}
			blobRoot.Close()
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(raw)
			if int64(len(raw)) != e.Size || int64(len(raw)) > m.maxFileSize || hex.EncodeToString(sum[:]) != e.Sha256 {
				return nil, errors.New("checkpoint: snapshot content verification failed")
			}
		}
		total += int64(len(raw))
		if total > 64<<20 {
			return nil, errors.New("checkpoint: restore exceeds recovery size limit")
		}
		paths = append(paths, absolute)
		targets = append(targets, target{e, raw})
	}
	if man.Scope != "" {
		current, _, err := m.workspacePaths(man.Scope)
		if err != nil {
			return nil, err
		}
		for _, path := range current {
			if !seen[path] {
				paths = append(paths, path)
				targets = append(targets, target{entry: Entry{Path: path}})
			}
		}
	}
	if len(paths) > 0 {
		recovery, err := m.snapshotLocked(man.SessionID, "before restore "+id, paths, id)
		if err != nil {
			return nil, fmt.Errorf("checkpoint: cannot preserve current files before restore: %w", err)
		}
		// Retain the recovery manifest even when subsequent filesystem writes fail.
		_ = recovery
	}
	for _, t := range targets {
		root, name, _, err := m.openPath(t.entry.Path)
		if err != nil {
			return nil, err
		}
		if !t.entry.Existed {
			err = root.Remove(name)
			if os.IsNotExist(err) {
				err = nil
			}
		} else {
			err = root.MkdirAll(filepath.Dir(name), 0o750)
			if err == nil {
				mode := os.FileMode(t.entry.Mode)
				if man.Version < 2 && mode == 0 {
					mode = 0o600
				}
				err = writeRootFile(root, name, t.raw, mode)
			}
		}
		root.Close()
		if err != nil {
			return nil, fmt.Errorf("checkpoint: restore incomplete; pre-restore recovery retained: %w", err)
		}
	}
	return nil, nil
}

func writeRootFile(root *os.Root, name string, raw []byte, mode os.FileMode) error {
	temp := filepath.Join(filepath.Dir(name), ".overkill-restore-"+newID())
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	if err = f.Chmod(mode.Perm()); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := root.Rename(temp, name); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	parent, err := root.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

// List returns checkpoints for a session, newest first. Empty session ID
// returns every checkpoint.
func (m *Manager) List(sessionID string) ([]Manifest, error) {
	if m.sessionID != "" {
		if sessionID != "" && sessionID != m.sessionID {
			return nil, errors.New("checkpoint: session mismatch")
		}
		sessionID = m.sessionID
	}
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
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return nil, errors.New("checkpoint: invalid ID")
	}
	root, err := os.OpenRoot(m.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(id)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("checkpoint: invalid directory")
	}
	f, err := root.OpenFile(filepath.Join(id, "manifest.json"), os.O_RDONLY|checkpointOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("checkpoint: invalid manifest file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil {
		return nil, err
	}
	var man Manifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return nil, errors.New("checkpoint: invalid manifest")
	}
	if man.ID != id || len(man.Entries) > 4096 || (man.Scope != "" && man.Scope != "workspace" && man.Scope != "git-visible") {
		return nil, errors.New("checkpoint: invalid manifest")
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

// isUnderRoot returns true when path is within root (or equal to it).
func isUnderRoot(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	return strings.HasPrefix(absPath, absRoot+string(os.PathSeparator)) || absPath == absRoot
}

// Directory fsync makes manifest publication durable on supported Unix hosts.
// Windows provides atomic file publication but no directory fsync through os.
func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
