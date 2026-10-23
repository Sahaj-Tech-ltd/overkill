// Package worktree manages isolated task checkouts without discarding user work.
package worktree

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ManagedTree struct {
	TaskID    string
	Path      string
	Branch    string
	CreatedAt time.Time
	// GitDir binds the allocation to Git's administrative identity, not merely a path.
	GitDir       string
	AllocationID string
	LockReason   string
}

type Manager struct {
	repoDir, subDir string
	mu              sync.Mutex
	active          map[string]*ManagedTree
}

func NewManager(repoDir, subDir string) *Manager {
	if canonical, err := filepath.EvalSymlinks(repoDir); err == nil {
		repoDir = canonical
	}
	if absolute, err := filepath.Abs(repoDir); err == nil {
		repoDir = absolute
	}
	if subDir == "" {
		subDir = ".overkill-worktrees"
	}
	return &Manager{repoDir: repoDir, subDir: subDir, active: map[string]*ManagedTree{}}
}

type AcquireOptions struct {
	Branch string
	// ForceFresh replaces a clean, verified allocation only; dirty files are preserved.
	ForceFresh bool
	LockReason string
}
type ReleaseOptions struct {
	DeleteBranch bool
	// Force is retained for compatibility. Managed cleanup never discards dirty work.
	Force bool
}

func validateTask(task string) error {
	if strings.TrimSpace(task) == "" || len(task) > 1024 || task == "." || task == ".." {
		return errors.New("worktree manager: invalid task id")
	}
	for _, c := range task {
		if c < 32 || c == 127 {
			return errors.New("worktree manager: task id contains control characters")
		}
	}
	return nil
}
func allocationName(task string) string {
	sum := sha256.Sum256([]byte(task))
	prefix := sanitizeBranch(task)
	if len(prefix) > 48 {
		prefix = prefix[:48]
	}
	return prefix + "-" + hex.EncodeToString(sum[:])
}
func (m *Manager) root() string { return filepath.Join(m.repoDir, m.subDir) }
func (m *Manager) record(task string) string {
	return filepath.Join(m.root(), allocationName(task)+".json")
}

// guard serializes independent managers/processes using a kernel lock released on exit.
// The managed root must be a real immediate directory; repository aliases are canonicalized.
func (m *Manager) guard() (func(), error) {
	if m.repoDir == "" || m.subDir == "." || m.subDir == ".." || strings.EqualFold(m.subDir, ".git") || filepath.Base(m.subDir) != m.subDir || strings.ContainsAny(m.subDir, "/\\") {
		return nil, errors.New("worktree manager: invalid allocation root")
	}
	top, err := runGit(m.repoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(strings.TrimSpace(top))
	if err != nil || canonical != m.repoDir {
		return nil, errors.New("worktree manager: repo must be the checkout root")
	}
	common, err := runGit(m.repoDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(strings.TrimSpace(common), "overkill-worktrees.lock")
	if info, err := os.Lstat(lockPath); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return nil, errors.New("worktree manager: unsafe lock file")
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockManagerFile(f); err != nil {
		f.Close()
		return nil, err
	}
	unlock := func() { _ = unlockManagerFile(f); _ = f.Close() }
	if info, err := os.Lstat(m.root()); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			unlock()
			return nil, errors.New("worktree manager: allocation root must not be a symlink or file")
		}
	} else if !os.IsNotExist(err) {
		unlock()
		return nil, err
	} else if err = os.Mkdir(m.root(), 0750); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}
func clone(mt *ManagedTree) *ManagedTree { c := *mt; return &c }

func (m *Manager) load(task string) (*ManagedTree, error) {
	path := m.record(task)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16*1024 {
		return nil, errors.New("worktree manager: invalid ownership record")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var mt ManagedTree
	if err = json.Unmarshal(data, &mt); err != nil {
		return nil, err
	}
	if mt.TaskID != task || mt.Path != filepath.Join(m.root(), allocationName(task)) || mt.GitDir == "" || len(mt.AllocationID) != 64 || mt.Branch == "" {
		return nil, errors.New("worktree manager: ownership record mismatch")
	}
	return &mt, nil
}
func (m *Manager) verify(mt *ManagedTree) error {
	if mt.Path != filepath.Join(m.root(), allocationName(mt.TaskID)) {
		return errors.New("worktree manager: allocation path mismatch")
	}
	info, err := os.Lstat(mt.Path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("worktree manager: allocation is not a real directory")
	}
	trees, err := List(m.repoDir)
	if err != nil {
		return err
	}
	found := false
	for _, tree := range trees {
		if tree.Path == mt.Path && tree.Branch == "refs/heads/"+mt.Branch && !tree.Bare && !tree.Detached {
			found = true
			break
		}
	}
	if !found {
		return errors.New("worktree manager: Git registration or branch changed; preserving checkout")
	}
	gitDir, err := runGit(mt.Path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	if strings.TrimSpace(gitDir) != mt.GitDir {
		return errors.New("worktree manager: Git administrative identity changed")
	}
	tokenPath := filepath.Join(mt.GitDir, "overkill-owner")
	tokenInfo, err := os.Lstat(tokenPath)
	if err != nil || !tokenInfo.Mode().IsRegular() || tokenInfo.Size() != 64 {
		return errors.New("worktree manager: allocation ownership token missing or invalid")
	}
	token, err := os.ReadFile(tokenPath)
	if err != nil || string(token) != mt.AllocationID {
		return errors.New("worktree manager: allocation ownership changed; preserving checkout")
	}

	return nil
}
func (m *Manager) save(mt *ManagedTree) error {
	data, err := json.Marshal(mt)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(m.root(), ".allocation-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, m.record(mt.TaskID))
}
func (m *Manager) Acquire(task string, opts AcquireOptions) (*ManagedTree, error) {
	if err := validateTask(task); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	unlock, err := m.guard()
	if err != nil {
		return nil, err
	}
	defer unlock()
	mt, err := m.load(task)
	if err == nil {
		if opts.Branch != "" && opts.Branch != mt.Branch {
			return nil, errors.New("worktree manager: task already allocated to another branch")
		}
		if err = m.verify(mt); err != nil {
			return nil, err
		}
		if !opts.ForceFresh {
			m.active[task] = mt
			return clone(mt), nil
		}
		if err = m.remove(mt, ReleaseOptions{}); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	path := filepath.Join(m.root(), allocationName(task))
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		return nil, errors.New("worktree manager: unowned allocation path exists; preserving it")
	}
	branch := opts.Branch
	if branch == "" {
		branch = "overkill/parallel/" + allocationName(task)
	}
	if err = Add(m.repoDir, path, branch); err != nil {
		return nil, err
	}
	gitDir, err := runGit(path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	allocationID := hex.EncodeToString(nonce[:])
	mt = &ManagedTree{TaskID: task, Path: path, Branch: branch, CreatedAt: time.Now().UTC(), GitDir: strings.TrimSpace(gitDir), AllocationID: allocationID, LockReason: opts.LockReason}
	token, err := os.OpenFile(filepath.Join(mt.GitDir, "overkill-owner"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, err = token.WriteString(allocationID)
	closeErr := token.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = m.save(mt); err != nil {
		return nil, fmt.Errorf("worktree manager: persist ownership (checkout retained): %w", err)
	}
	if opts.LockReason != "" {
		if err = Lock(m.repoDir, path, opts.LockReason); err != nil {
			return nil, fmt.Errorf("worktree manager: lock (allocation retained): %w", err)
		}
	}
	m.active[task] = mt
	return clone(mt), nil
}
func (m *Manager) remove(mt *ManagedTree, opts ReleaseOptions) error {
	if err := m.verify(mt); err != nil {
		return err
	}
	status, err := runGit(mt.Path, "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("worktree manager: checkout contains modified, untracked or ignored files; preserving work")
	}
	trees, err := List(m.repoDir)
	if err != nil {
		return err
	}
	locked := false
	for _, tree := range trees {
		if tree.Path == mt.Path {
			locked = tree.Locked
			if locked && (mt.LockReason == "" || tree.LockReason != mt.LockReason) {
				return errors.New("worktree manager: checkout has an external Git lock; preserving it")
			}
		}
	}
	if locked {
		if err = Unlock(m.repoDir, mt.Path); err != nil {
			return err
		}
	}
	if err = Remove(m.repoDir, mt.Path); err != nil {
		if locked {
			_ = Lock(m.repoDir, mt.Path, mt.LockReason)
		}
		return err
	}
	if err = os.Remove(m.record(mt.TaskID)); err != nil {
		return err
	}
	delete(m.active, mt.TaskID)
	if opts.DeleteBranch { // Only delete merged branches; unmerged commits remain recoverable by name.
		if _, err = runGit(m.repoDir, "branch", "-d", "--", mt.Branch); err != nil {
			return fmt.Errorf("worktree removed; branch preserved: %w", err)
		}
	}
	return nil
}
func (m *Manager) Release(task string, opts ReleaseOptions) error {
	if err := validateTask(task); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	unlock, err := m.guard()
	if err != nil {
		return err
	}
	defer unlock()
	mt, err := m.load(task)
	if os.IsNotExist(err) {
		delete(m.active, task)
		return nil
	}
	if err != nil {
		return err
	}
	return m.remove(mt, opts)
}
func (m *Manager) List() []*ManagedTree {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*ManagedTree, 0, len(m.active))
	for _, mt := range m.active {
		out = append(out, clone(mt))
	}
	return out
}
func (m *Manager) Get(task string) *ManagedTree {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mt := m.active[task]; mt != nil {
		return clone(mt)
	}
	return nil
}
func (m *Manager) Reclaim() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	unlock, err := m.guard()
	if err != nil {
		return err
	}
	defer unlock()
	records, err := os.ReadDir(m.root())
	if err != nil {
		return err
	}
	restored := map[string]*ManagedTree{}
	for _, record := range records {
		if record.IsDir() || !strings.HasSuffix(record.Name(), ".json") {
			continue
		}
		info, err := record.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 16*1024 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.root(), record.Name()))
		if err != nil {
			return err
		}
		var hint ManagedTree
		if json.Unmarshal(data, &hint) != nil || validateTask(hint.TaskID) != nil || record.Name() != allocationName(hint.TaskID)+".json" {
			continue
		}
		mt, err := m.load(hint.TaskID)
		if err != nil {
			continue
		}
		if m.verify(mt) != nil {
			continue
		}
		restored[mt.TaskID] = mt
	}
	m.active = restored
	return nil
}
func sanitizeBranch(taskID string) string {
	var b strings.Builder
	for _, r := range taskID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "task"
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
func sanitizePath(taskID string) string { return allocationName(taskID) }
