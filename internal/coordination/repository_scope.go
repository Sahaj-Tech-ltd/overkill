package coordination

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
)

// RepositoryInfo describes a local, explicitly opted-in repository binding.
// HEAD and Branch are observations, not immutable identity or an ownership claim.
type RepositoryInfo struct {
	Scope     string `json:"scope"`
	Root      string `json:"root"`
	CommonDir string `json:"common_dir"`
	Head      string `json:"head,omitempty"`
	Branch    string `json:"branch,omitempty"`
}
type repositorySnapshot struct {
	RepositoryInfo
	rootIdentity   string
	commonIdentity string
}

var repositoryDDL = []string{`CREATE TABLE IF NOT EXISTS coordination_repositories (
 peer_id TEXT PRIMARY KEY REFERENCES coordination_peers(id), session_id TEXT NOT NULL UNIQUE,
 root TEXT NOT NULL, common_dir TEXT NOT NULL, root_identity TEXT NOT NULL, common_identity TEXT NOT NULL)`}

func repositoryProjectID(common string) string {
	h := sha256.Sum256([]byte(common))
	return "repository:" + hex.EncodeToString(h[:])
}

// gitIdentityCommand uses only local read-only rev-parse operations. Git-specific
// inherited environment is removed so a caller's GIT_DIR cannot retarget a chat.
func gitIdentityCommand(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=" + root, "-C", root, "rev-parse"}, args...)...)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out := processrunner.NewOutputCapture(32768)
	diagnostics := processrunner.NewOutputCapture(4096)
	cmd.Stdout = out
	cmd.Stderr = diagnostics
	if err := processrunner.Run(ctx, cmd); err != nil {
		return "", fmt.Errorf("coordination: cannot inspect local Git repository: %w", err)
	}
	value, _, truncated := out.Snapshot()
	if truncated {
		return "", fmt.Errorf("coordination: Git identity output too large")
	}
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("coordination: invalid Git identity output")
	}
	return value, nil
}
func inspectRepository(ctx context.Context, folder string) (repositorySnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result repositorySnapshot
	if !filepath.IsAbs(folder) {
		return result, fmt.Errorf("coordination: absolute repository worktree required")
	}
	root, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return result, err
	}
	root = filepath.Clean(root)
	// Directory aliases are canonicalized, but Git's identity paths must themselves
	// resolve exactly: symlinked .git/common directories cannot stand in for a repo.
	bare, err := gitIdentityCommand(ctx, root, "--is-bare-repository")
	if err != nil || bare != "false" {
		return result, fmt.Errorf("coordination: non-bare Git worktree required")
	}
	top, err := gitIdentityCommand(ctx, root, "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return result, err
	}
	top = filepath.Clean(top)
	if top != root {
		return result, fmt.Errorf("coordination: session folder must be the Git worktree root")
	}
	common, err := gitIdentityCommand(ctx, root, "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return result, err
	}
	if !filepath.IsAbs(common) {
		return result, fmt.Errorf("coordination: absolute Git common directory required")
	}
	common = filepath.Clean(common)
	canonical, err := filepath.EvalSymlinks(common)
	if err != nil || canonical != common {
		return result, fmt.Errorf("coordination: Git common directory must not be a symlink identity")
	}
	gitDir, err := gitIdentityCommand(ctx, root, "--path-format=absolute", "--git-dir")
	if err != nil {
		return result, err
	}
	canonical, err = filepath.EvalSymlinks(gitDir)
	if err != nil || canonical != filepath.Clean(gitDir) {
		return result, fmt.Errorf("coordination: Git directory must not be a symlink identity")
	}
	// Reject a symlink .git entry as well as symlinked resolved directories.
	if entry, err := os.Lstat(filepath.Join(root, ".git")); err != nil || entry.Mode()&os.ModeSymlink != 0 {
		return result, fmt.Errorf("coordination: invalid worktree .git entry")
	}
	rootID, err := repositoryDirectoryIdentity(root)
	if err != nil {
		return result, err
	}
	commonID, err := repositoryDirectoryIdentity(common)
	if err != nil {
		return result, err
	}
	result = repositorySnapshot{RepositoryInfo: RepositoryInfo{Scope: "repository", Root: root, CommonDir: common}, rootIdentity: rootID, commonIdentity: commonID}
	// An unborn branch has no HEAD object. That is a valid local worktree.
	result.Head, _ = gitIdentityCommand(ctx, root, "--verify", "HEAD")
	result.Branch, _ = gitIdentityCommand(ctx, root, "--abbrev-ref", "HEAD")
	if err := ctx.Err(); err != nil {
		return repositorySnapshot{}, err
	}
	return result, nil
}

// RegisterRepositorySessionPeer explicitly joins peers in linked worktrees of
// the same canonical Git common directory. Existing bindings never change scope.
func (s *Store) RegisterRepositorySessionPeer(ctx context.Context, sessionID, name, role string) (Peer, error) {
	return s.registerSessionPeer(ctx, sessionID, name, role, true)
}

type repositoryQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) repositoryInfoForPeer(ctx context.Context, peer Peer, sessionID string) (RepositoryInfo, error) {
	return repositoryInfoForPeer(ctx, s.db, peer, sessionID)
}
func repositoryInfoForPeer(ctx context.Context, q repositoryQuerier, peer Peer, sessionID string) (RepositoryInfo, error) {
	var stored repositorySnapshot
	var storedSession, folder string
	err := q.QueryRowContext(ctx, `SELECT r.session_id,r.root,r.common_dir,r.root_identity,r.common_identity,s.folder FROM coordination_repositories r JOIN sessions s ON s.id=r.session_id JOIN coordination_sessions b ON b.peer_id=r.peer_id AND b.session_id=r.session_id WHERE r.peer_id=$1`, peer.ID).Scan(&storedSession, &stored.Root, &stored.CommonDir, &stored.rootIdentity, &stored.commonIdentity, &folder)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryInfo{}, fmt.Errorf("coordination: repository identity snapshot missing: %w", ErrConflict)
	}
	if err != nil {
		return RepositoryInfo{}, err
	}
	if (sessionID != "" && storedSession != sessionID) || stored.Root != peer.Workspace || peer.ProjectID != repositoryProjectID(stored.CommonDir) {
		return RepositoryInfo{}, fmt.Errorf("coordination: repository binding changed: %w", ErrConflict)
	}
	live, err := inspectRepository(ctx, folder)
	if err != nil {
		return RepositoryInfo{}, fmt.Errorf("coordination: repository identity unavailable: %w", err)
	}
	if live.Root != stored.Root || live.CommonDir != stored.CommonDir || live.rootIdentity != stored.rootIdentity || live.commonIdentity != stored.commonIdentity {
		return RepositoryInfo{}, fmt.Errorf("coordination: repository identity changed: %w", ErrConflict)
	}
	return live.RepositoryInfo, nil
}

// RepositoryInfoForSession returns validated trusted local metadata only for an
// explicitly repository-scoped binding. Unregistered/workspace chats are absent.
func (s *Store) RepositoryInfoForSession(ctx context.Context, sessionID string) (RepositoryInfo, error) {
	peer, err := s.PeerForSession(ctx, sessionID)
	if err != nil {
		return RepositoryInfo{}, err
	}
	if !strings.HasPrefix(peer.ProjectID, "repository:") {
		return RepositoryInfo{}, ErrNotFound
	}
	return s.repositoryInfoForPeer(ctx, peer, sessionID)
}

func validateRepositoryPeer(ctx context.Context, q repositoryQuerier, peer Peer) error {
	if !strings.HasPrefix(peer.ProjectID, "repository:") {
		return nil
	}
	_, err := repositoryInfoForPeer(ctx, q, peer, "")
	return err
}

// Reads also revalidate cached peer IDs; absence preserves existing not-found/
// empty-result semantics of each read method for legacy callers.
func (s *Store) validateRepositoryPeerID(ctx context.Context, id string) error {
	peer, err := scanPeer(s.db.QueryRowContext(ctx, `SELECT id,project_id,name,role,workspace,capabilities,endpoint,enabled FROM coordination_peers WHERE id=$1 AND project_id LIKE 'repository:%'`, id))
	if errors.Is(err, ErrUnauthorized) {
		return nil
	}
	if err != nil {
		return err
	}
	return validateRepositoryPeer(ctx, s.db, peer)
}
