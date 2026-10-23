package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

// GitBackend stores blobs in a local git repo and pushes to a remote on every
// push. Uses the system `git` binary so we don't pull in go-git.
type GitBackend struct {
	dir    string
	remote string
	branch string
}

func NewGitBackend(cfg config.SyncGitConfig) (*GitBackend, error) {
	dir := cfg.LocalDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("sync/git: home dir: %w", err)
		}
		dir = filepath.Join(home, ".overkill-sync")
	}
	branch := cfg.Branch
	if branch == "" {
		branch = "main"
	}
	// Reject branch names that start with '-' to prevent flag injection
	// when the branch is passed to git push/fetch (e.g. --upload-pack=/evil).
	if strings.HasPrefix(branch, "-") {
		return nil, fmt.Errorf("sync/git: branch name %q is invalid (must not start with '-')", branch)
	}
	// Validate remote URL to prevent RCE via git transport protocols
	// (e.g. "ext::sh -c malicious_cmd%"). Only allow http/https/ssh/git/file.
	if cfg.RemoteURL != "" {
		if blocked := blockedGitURL(cfg.RemoteURL); blocked {
			return nil, fmt.Errorf("sync/git: remote URL %q uses a blocked transport scheme", cfg.RemoteURL)
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("sync/git: mkdir: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if out, err := runGit(dir, "init", "-b", branch); err != nil {
			return nil, fmt.Errorf("sync/git: init: %s: %w", out, err)
		}
		if cfg.RemoteURL != "" {
			if out, err := runGit(dir, "remote", "add", "origin", cfg.RemoteURL); err != nil {
				return nil, fmt.Errorf("sync/git: remote add: %s: %w", out, err)
			}
		}
	}
	return &GitBackend{dir: dir, remote: cfg.RemoteURL, branch: branch}, nil
}

func (g *GitBackend) Name() string { return "git" }

func runGit(dir string, args ...string) (string, error) {
	return runGitContext(context.Background(), dir, args...)
}

func runGitContext(ctx context.Context, dir string, args ...string) (string, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	cmd := exec.Command("git", append([]string{"-c", "protocol.ext.allow=never"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	out, err := processrunner.CombinedOutput(ctx, cmd)
	return strings.TrimSpace(string(out)), err
}

// safeGitID rejects session IDs that could double as git options/flags
// when interpolated into git invocations.  Session IDs should be
// alphanumeric and hyphens (UUID-ish) — anything else is suspicious.
func safeGitID(id string) error {
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("sync/git: invalid session ID %q", id)
	}
	if id == "" {
		return fmt.Errorf("sync/git: empty session ID")
	}
	// Reject IDs that look like flags: starting with "-", containing "..",
	// or that are too long (sanity check).
	if id[0] == '-' || strings.Contains(id, "..") || len(id) > 256 {
		return fmt.Errorf("sync/git: suspicious session ID %q", id)
	}
	return nil
}

func (g *GitBackend) Push(ctx context.Context, id string, data []byte, meta SessionMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Validate ID before passing to git.
	if err := safeGitID(id); err != nil {
		return err
	}
	// Fetch first to reduce conflict risk.
	if g.remote != "" {
		if _, err := runGitContext(ctx, g.dir, "fetch", "origin", g.branch); err != nil {
			return fmt.Errorf("sync/git: fetch: %w", err)
		}
		if _, err := runGitContext(ctx, g.dir, "merge", "--no-edit", "origin/"+g.branch); err != nil {
			return fmt.Errorf("sync/git: merge: %w", err)
		}
	}
	// Write meta first, then blob, both atomically — same pattern as
	// FileBackend.Push so a crash between the two leaves discoverable
	// orphan meta but never a truncated half-file.
	mb, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("sync/git: marshal meta: %w", err)
	}
	metaP, err := security.SafePath(g.dir, id+".meta.json")
	if err != nil {
		return fmt.Errorf("sync/git: meta path: %w", err)
	}
	if err := atomicfile.WriteFile(metaP, mb, 0o600); err != nil {
		return fmt.Errorf("sync/git: write meta: %w", err)
	}
	blobP, err := security.SafePath(g.dir, id+".json.gz")
	if err != nil {
		return fmt.Errorf("sync/git: blob path: %w", err)
	}
	if err := atomicfile.WriteFile(blobP, data, 0o600); err != nil {
		_ = os.Remove(metaP)
		return fmt.Errorf("sync/git: write blob: %w", err)
	}
	if out, err := runGitContext(ctx, g.dir, "add", id+".json.gz", id+".meta.json"); err != nil {
		return fmt.Errorf("sync/git: add: %s: %w", out, err)
	}
	if out, err := runGitContext(ctx, g.dir, "-c", "user.email=overkill@local", "-c", "user.name=overkill",
		"commit", "-m", "sync: "+id, "--allow-empty", "--only", "--", id+".json.gz", id+".meta.json"); err != nil {
		// Ignore "nothing to commit"
		if !strings.Contains(out, "nothing to commit") && !strings.Contains(out, "nothing added") {
			return fmt.Errorf("sync/git: commit: %s: %w", out, err)
		}
	}
	if g.remote != "" {
		if out, err := runGitContext(ctx, g.dir, "push", "-u", "origin", g.branch); err != nil {
			return fmt.Errorf("sync/git: push: %s: %w", out, err)
		}
	}
	return nil
}

func (g *GitBackend) Pull(ctx context.Context, id string) ([]byte, SessionMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, SessionMeta{}, err
	}
	// Validate ID before path construction.
	if err := safeGitID(id); err != nil {
		return nil, SessionMeta{}, err
	}
	if g.remote != "" {
		if _, err := runGitContext(ctx, g.dir, "fetch", "origin", g.branch); err != nil {
			return nil, SessionMeta{}, fmt.Errorf("sync/git: fetch: %w", err)
		}
		if _, err := runGitContext(ctx, g.dir, "merge", "--no-edit", "origin/"+g.branch); err != nil {
			return nil, SessionMeta{}, fmt.Errorf("sync/git: merge: %w", err)
		}
	}
	blobP, err := security.SafePath(g.dir, id+".json.gz")
	if err != nil {
		return nil, SessionMeta{}, fmt.Errorf("sync/git: blob path: %w", err)
	}
	data, err := os.ReadFile(blobP)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, SessionMeta{}, ErrNotFound
		}
		return nil, SessionMeta{}, fmt.Errorf("sync/git: read blob: %w", err)
	}
	var meta SessionMeta
	metaP, err := security.SafePath(g.dir, id+".meta.json")
	if err == nil {
		if mb, err := os.ReadFile(metaP); err == nil {
			_ = json.Unmarshal(mb, &meta)
		}
	}
	if meta.ID == "" {
		meta.ID = id
	}
	return data, meta, nil
}

func (g *GitBackend) List(ctx context.Context) ([]SessionMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g.remote != "" {
		if _, err := runGitContext(ctx, g.dir, "fetch", "origin", g.branch); err != nil {
			return nil, fmt.Errorf("sync/git: fetch: %w", err)
		}
		if _, err := runGitContext(ctx, g.dir, "merge", "--no-edit", "origin/"+g.branch); err != nil {
			return nil, fmt.Errorf("sync/git: merge: %w", err)
		}
	}
	entries, err := os.ReadDir(g.dir)
	if err != nil {
		return nil, fmt.Errorf("sync/git: list: %w", err)
	}
	var out []SessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta.json") {
			continue
		}
		mb, err := os.ReadFile(filepath.Join(g.dir, e.Name()))
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

// blockedGitURL returns true when url uses a git transport that could
// execute arbitrary commands (ext::, remote-ext::, etc.).
// Only http, https, ssh, git, and file transports are allowed.
func blockedGitURL(url string) bool {
	// Check for blocked transport prefix before the first ":" that isn't
	// part of "://".
	if idx := strings.Index(url, "://"); idx >= 0 {
		scheme := strings.ToLower(url[:idx])
		switch scheme {
		case "http", "https", "ssh", "git", "file":
			return false
		default:
			return true
		}
	}
	// scp-like syntax (user@host:path) — allowed.
	return false
}

func (g *GitBackend) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Validate ID before passing to git.
	if err := safeGitID(id); err != nil {
		return err
	}
	bp, err := security.SafePath(g.dir, id+".json.gz")
	if err != nil {
		return fmt.Errorf("sync/git: blob path: %w", err)
	}
	mp, err := security.SafePath(g.dir, id+".meta.json")
	if err != nil {
		return fmt.Errorf("sync/git: meta path: %w", err)
	}
	tracked, err := runGitContext(ctx, g.dir, "ls-files", "-z", "--", id+".json.gz", id+".meta.json")
	if err != nil {
		return fmt.Errorf("sync/git: inspect tracked deletion paths: %w", err)
	}
	// The index may already contain staged deletions after a failed commit.
	// HEAD remains the durable source of paths that still need committing.
	committed := ""
	head, headErr := runGitContext(ctx, g.dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if headErr != nil {
		var exit *exec.ExitError
		if !errors.As(headErr, &exit) || exit.ExitCode() != 1 {
			return fmt.Errorf("sync/git: inspect HEAD: %w", headErr)
		}
	}
	if head != "" {
		tree, err := runGitContext(ctx, g.dir, "ls-tree", "--name-only", "-z", "HEAD", "--", id+".json.gz", id+".meta.json")
		if err != nil {
			return fmt.Errorf("sync/git: inspect committed deletion paths: %w", err)
		}
		committed = tree
		if tree != "" {
			tracked = tree
		}
	}
	removed := false
	for _, path := range []string{bp, mp} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("sync/git: remove %s: %w", filepath.Base(path), err)
			}
		} else {
			removed = true
		}
	}
	if !removed && tracked == "" {
		// A committed deletion whose remote push failed is safe to retry. Use
		// path-specific history, so unrelated missing IDs still return ErrNotFound.
		receipt, err := runGitContext(ctx, g.dir, "log", "-1", "--format=%s", "--", id+".json.gz", id+".meta.json")
		if err != nil || receipt != "sync: delete "+id {
			return ErrNotFound
		}
	}
	if committed == "" && tracked != "" {
		// A failed first Push can leave only newly staged files, with no HEAD
		// version to delete. Clear just those index entries; no deletion commit
		// is needed for content that was never published.
		paths := strings.Split(strings.TrimRight(tracked, "\x00"), "\x00")
		if out, err := runGitContext(ctx, g.dir, append([]string{"rm", "--cached", "--ignore-unmatch", "--"}, paths...)...); err != nil {
			return fmt.Errorf("sync/git: remove unpublished index paths: %s: %w", out, err)
		}
		return nil
	}
	if tracked != "" {
		paths := strings.Split(strings.TrimRight(tracked, "\x00"), "\x00")
		if head != "" {
			if out, err := runGitContext(ctx, g.dir, append([]string{"reset", "-q", "HEAD", "--"}, paths...)...); err != nil {
				return fmt.Errorf("sync/git: prepare deletion retry: %s: %w", out, err)
			}
		}
		if out, err := runGitContext(ctx, g.dir, append([]string{"add", "-u", "--"}, paths...)...); err != nil {
			return fmt.Errorf("sync/git: stage deletion: %s: %w", out, err)
		}
		args := []string{"-c", "user.email=overkill@local", "-c", "user.name=overkill", "commit", "-m", "sync: delete " + id, "--only", "--"}
		if out, err := runGitContext(ctx, g.dir, append(args, paths...)...); err != nil {
			return fmt.Errorf("sync/git: commit deletion: %s: %w", out, err)
		}
	}
	if g.remote != "" {
		if out, err := runGitContext(ctx, g.dir, "push", "origin", g.branch); err != nil {
			return fmt.Errorf("sync/git: push deletion: %s: %w", out, err)
		}
	}
	return nil
}
