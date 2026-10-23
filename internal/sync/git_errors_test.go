package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func gitFixture(t *testing.T) *GitBackend {
	t.Helper()
	backend, err := NewGitBackend(config.SyncGitConfig{LocalDir: t.TempDir()})
	require.NoError(t, err)
	require.NoError(t, backend.Push(t.Context(), "fixture", []byte("contents"), SessionMeta{ID: "fixture"}))
	return backend
}

func TestGitDeleteReportsFilesystemCommitAndPushFailures(t *testing.T) {
	t.Run("filesystem", func(t *testing.T) {
		backend := gitFixture(t)
		path := filepath.Join(backend.dir, "fixture.json.gz")
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Mkdir(path, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(path, "preserve"), []byte("data"), 0o600))
		require.ErrorContains(t, backend.Delete(t.Context(), "fixture"), "remove fixture.json.gz")
		_, err := os.Stat(filepath.Join(backend.dir, "fixture.meta.json"))
		require.NoError(t, err)
	})
	t.Run("commit", func(t *testing.T) {
		backend := gitFixture(t)
		before, err := runGit(backend.dir, "rev-parse", "HEAD")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(backend.dir, ".git/hooks/pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o700))
		require.ErrorContains(t, backend.Delete(t.Context(), "fixture"), "commit deletion")
		after, err := runGit(backend.dir, "rev-parse", "HEAD")
		require.NoError(t, err)
		require.Equal(t, before, after)
	})
	t.Run("push", func(t *testing.T) {
		backend := gitFixture(t)
		remote := t.TempDir()
		_, err := runGit(remote, "init", "--bare", "--initial-branch=main")
		require.NoError(t, err)
		_, err = runGit(backend.dir, "remote", "add", "origin", remote)
		require.NoError(t, err)
		_, err = runGit(backend.dir, "push", "-u", "origin", "main")
		require.NoError(t, err)
		before, err := runGit(remote, "rev-parse", "HEAD")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(remote, "hooks/pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0o700))
		backend.remote = remote
		require.ErrorContains(t, backend.Delete(t.Context(), "fixture"), "push deletion")
		after, err := runGit(remote, "rev-parse", "HEAD")
		require.NoError(t, err)
		require.Equal(t, before, after)
		data, err := runGit(remote, "show", "HEAD:fixture.json.gz")
		require.NoError(t, err)
		require.Equal(t, "contents", data)
	})
}

func TestGitDeletePreservesUnrelatedStagedChanges(t *testing.T) {
	backend := gitFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(backend.dir, "unrelated.txt"), []byte("keep staged"), 0o600))
	_, err := runGit(backend.dir, "add", "unrelated.txt")
	require.NoError(t, err)
	require.NoError(t, backend.Delete(t.Context(), "fixture"))
	staged, err := runGit(backend.dir, "diff", "--cached", "--name-only")
	require.NoError(t, err)
	require.Equal(t, "unrelated.txt", staged)
	_, err = runGit(backend.dir, "show", "HEAD:unrelated.txt")
	require.Error(t, err)
}

func TestGitCancelledOperationsDoNotMutateLocalFiles(t *testing.T) {
	backend := gitFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, backend.Push(ctx, "fixture", []byte("overwrite"), SessionMeta{}), context.Canceled)
	require.ErrorIs(t, backend.Delete(ctx, "fixture"), context.Canceled)
	_, _, err := backend.Pull(ctx, "fixture")
	require.ErrorIs(t, err, context.Canceled)
	_, err = backend.List(ctx)
	require.ErrorIs(t, err, context.Canceled)
	data, err := os.ReadFile(filepath.Join(backend.dir, "fixture.json.gz"))
	require.NoError(t, err)
	require.Equal(t, "contents", string(data))
}

func TestGitDeleteRetriesFailedCommitAndPush(t *testing.T) {
	t.Run("commit", func(t *testing.T) {
		backend := gitFixture(t)
		hook := filepath.Join(backend.dir, ".git/hooks/pre-commit")
		require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700))
		require.ErrorContains(t, backend.Delete(t.Context(), "fixture"), "commit deletion")
		require.NoError(t, os.Remove(hook))
		require.NoError(t, backend.Delete(t.Context(), "fixture"))
		_, err := runGit(backend.dir, "show", "HEAD:fixture.json.gz")
		require.Error(t, err)
	})
	t.Run("push", func(t *testing.T) {
		backend := gitFixture(t)
		remote := t.TempDir()
		_, err := runGit(remote, "init", "--bare", "--initial-branch=main")
		require.NoError(t, err)
		_, err = runGit(backend.dir, "remote", "add", "origin", remote)
		require.NoError(t, err)
		_, err = runGit(backend.dir, "push", "-u", "origin", "main")
		require.NoError(t, err)
		backend.remote = remote
		hook := filepath.Join(remote, "hooks/pre-receive")
		require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700))
		require.ErrorContains(t, backend.Delete(t.Context(), "fixture"), "push deletion")
		require.NoError(t, os.Remove(hook))
		require.NoError(t, backend.Delete(t.Context(), "fixture"))
		_, err = runGit(remote, "show", "HEAD:fixture.json.gz")
		require.Error(t, err)
		require.ErrorIs(t, backend.Delete(t.Context(), "unknown"), ErrNotFound)
	})
}

func TestGitPushPreservesUnrelatedStagedChanges(t *testing.T) {
	backend := gitFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(backend.dir, "unrelated.txt"), []byte("keep staged"), 0o600))
	_, err := runGit(backend.dir, "add", "unrelated.txt")
	require.NoError(t, err)
	require.NoError(t, backend.Push(t.Context(), "fixture", []byte("changed"), SessionMeta{ID: "fixture"}))
	staged, err := runGit(backend.dir, "diff", "--cached", "--name-only")
	require.NoError(t, err)
	require.Equal(t, "unrelated.txt", staged)
	_, err = runGit(backend.dir, "show", "HEAD:unrelated.txt")
	require.Error(t, err)
}

func TestGitDeleteCleansUnpublishedFailedPush(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "unborn", true: "existing-head"}[existing], func(t *testing.T) {
			backend, err := NewGitBackend(config.SyncGitConfig{LocalDir: t.TempDir()})
			require.NoError(t, err)
			if existing {
				require.NoError(t, backend.Push(t.Context(), "other", []byte("preserve"), SessionMeta{ID: "other"}))
			}
			hook := filepath.Join(backend.dir, ".git/hooks/pre-commit")
			require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700))
			require.Error(t, backend.Push(t.Context(), "unpublished", []byte("data"), SessionMeta{ID: "unpublished"}))
			require.NoError(t, backend.Delete(t.Context(), "unpublished"))
			staged, err := runGit(backend.dir, "ls-files", "--", "unpublished.json.gz", "unpublished.meta.json")
			require.NoError(t, err)
			require.Empty(t, staged)
			if existing {
				data, _, err := backend.Pull(t.Context(), "other")
				require.NoError(t, err)
				require.Equal(t, "preserve", string(data))
			}
		})
	}
}
