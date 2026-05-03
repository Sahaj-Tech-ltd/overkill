package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
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
		dir = filepath.Join(home, ".ethos-sync")
	}
	branch := cfg.Branch
	if branch == "" {
		branch = "main"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (g *GitBackend) Push(ctx context.Context, id string, data []byte, meta SessionMeta) error {
	// Fetch first to reduce conflict risk.
	if g.remote != "" {
		_, _ = runGit(g.dir, "fetch", "origin", g.branch)
		_, _ = runGit(g.dir, "merge", "--no-edit", "origin/"+g.branch)
	}
	if err := os.WriteFile(filepath.Join(g.dir, id+".json.gz"), data, 0o644); err != nil {
		return fmt.Errorf("sync/git: write blob: %w", err)
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("sync/git: marshal meta: %w", err)
	}
	if err := os.WriteFile(filepath.Join(g.dir, id+".meta.json"), mb, 0o644); err != nil {
		return fmt.Errorf("sync/git: write meta: %w", err)
	}
	if out, err := runGit(g.dir, "add", id+".json.gz", id+".meta.json"); err != nil {
		return fmt.Errorf("sync/git: add: %s: %w", out, err)
	}
	if out, err := runGit(g.dir, "-c", "user.email=ethos@local", "-c", "user.name=ethos",
		"commit", "-m", "sync: "+id, "--allow-empty"); err != nil {
		// Ignore "nothing to commit"
		if !strings.Contains(out, "nothing to commit") && !strings.Contains(out, "nothing added") {
			return fmt.Errorf("sync/git: commit: %s: %w", out, err)
		}
	}
	if g.remote != "" {
		if out, err := runGit(g.dir, "push", "-u", "origin", g.branch); err != nil {
			return fmt.Errorf("sync/git: push: %s: %w", out, err)
		}
	}
	return nil
}

func (g *GitBackend) Pull(ctx context.Context, id string) ([]byte, SessionMeta, error) {
	if g.remote != "" {
		_, _ = runGit(g.dir, "fetch", "origin", g.branch)
		_, _ = runGit(g.dir, "merge", "--no-edit", "origin/"+g.branch)
	}
	data, err := os.ReadFile(filepath.Join(g.dir, id+".json.gz"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, SessionMeta{}, ErrNotFound
		}
		return nil, SessionMeta{}, fmt.Errorf("sync/git: read blob: %w", err)
	}
	var meta SessionMeta
	if mb, err := os.ReadFile(filepath.Join(g.dir, id+".meta.json")); err == nil {
		_ = json.Unmarshal(mb, &meta)
	}
	if meta.ID == "" {
		meta.ID = id
	}
	return data, meta, nil
}

func (g *GitBackend) List(ctx context.Context) ([]SessionMeta, error) {
	if g.remote != "" {
		_, _ = runGit(g.dir, "fetch", "origin", g.branch)
		_, _ = runGit(g.dir, "merge", "--no-edit", "origin/"+g.branch)
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

func (g *GitBackend) Delete(ctx context.Context, id string) error {
	bp := filepath.Join(g.dir, id+".json.gz")
	mp := filepath.Join(g.dir, id+".meta.json")
	bErr := os.Remove(bp)
	mErr := os.Remove(mp)
	if bErr != nil && os.IsNotExist(bErr) && mErr != nil && os.IsNotExist(mErr) {
		return ErrNotFound
	}
	_, _ = runGit(g.dir, "add", "-A")
	_, _ = runGit(g.dir, "-c", "user.email=ethos@local", "-c", "user.name=ethos",
		"commit", "-m", "sync: delete "+id)
	if g.remote != "" {
		_, _ = runGit(g.dir, "push", "origin", g.branch)
	}
	return nil
}
