package coordination

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
	"github.com/stretchr/testify/require"
)

func repositoryGit(t *testing.T, root string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	out := processrunner.NewOutputCapture(8192)
	cmd.Stdout = out
	cmd.Stderr = out
	err := processrunner.Run(ctx, cmd)
	value, _, _ := out.Snapshot()
	require.NoError(t, err, value)
}
func repositoryPair(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("Git is required for repository coordination tests")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repositoryGit(t, root, "init")
	repositoryGit(t, root, "-c", "core.hooksPath="+os.DevNull, "commit", "--allow-empty", "-m", "fixture")
	linked := filepath.Join(root, "..", "linked "+filepath.Base(root))
	repositoryGit(t, root, "-c", "core.hooksPath="+os.DevNull, "worktree", "add", "-b", "linked", linked)
	t.Cleanup(func() { _ = os.RemoveAll(linked) })
	return root, linked
}
func TestRepositoryScopeCrossWorktreeRequestsContractsAndRestart(t *testing.T) {
	s, _, _, _ := fixture(t)
	root, linked := repositoryPair(t)
	frontSession := localSessions(t, s, root, 1)[0]
	backSession := localSessions(t, s, linked, 1)[0]
	front, err := s.RegisterRepositorySessionPeer(t.Context(), frontSession.ID, "repo_front", "UI")
	require.NoError(t, err)
	back, err := s.RegisterRepositorySessionPeer(t.Context(), backSession.ID, "repo_back", "API")
	require.NoError(t, err)
	require.Equal(t, front.ProjectID, back.ProjectID)
	require.NotEqual(t, front.Workspace, back.Workspace)
	restarted, err := NewStore(t.Context(), s.db)
	require.NoError(t, err)
	peers, err := restarted.LocalPeersForSession(t.Context(), frontSession.ID)
	require.NoError(t, err)
	require.Len(t, peers, 2)
	for _, p := range peers {
		require.Equal(t, "repository", p.Scope)
		require.NotNil(t, p.Repository)
		require.Equal(t, root+string(filepath.Separator)+".git", p.Repository.CommonDir)
	}
	info, err := restarted.RepositoryInfoForSession(t.Context(), backSession.ID)
	require.NoError(t, err)
	require.Equal(t, "linked", info.Branch)
	require.NotEmpty(t, info.Head)
	// A first dependency must complete, and the recipient must acknowledge the
	// pinned agreement, before the next cross-worktree request starts.
	contract, err := s.CreateContract(t.Context(), front.ID, contractSpec(front, "wire-api"))
	require.NoError(t, err)
	prereq := sendDependency(t, s, front, back.Name, "repo-prereq")
	in := request("repo-dependent")
	in.To = back.Name
	in.DependsOn = []string{prereq.ID}
	in.RequiredContracts = []ContractDependency{{ContractID: contract.ID, Revision: 1}}
	dependent, err := s.Send(t.Context(), front.ID, in)
	require.NoError(t, err)
	got, err := restarted.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, prereq.ID, got.ID)
	finishDependency(t, restarted, back, got)
	_, err = restarted.Claim(t.Context(), back.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.AcknowledgeContract(t.Context(), back.ID, contract.ID, 1)
	require.NoError(t, err)
	got, err = restarted.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, dependent.ID, got.ID)
	finishDependency(t, s, back, got)
	// Reverse-direction correspondence uses the same project despite different roots.
	in = request("reverse")
	in.To = front.Name
	in.Kind = "progress"
	in.Payload = json.RawMessage(`{"text":"API ready"}`)
	_, err = s.Send(t.Context(), back.ID, in)
	require.NoError(t, err)
	got, err = s.Claim(t.Context(), front.ID)
	require.NoError(t, err)
	require.Equal(t, back.ID, got.From)
}
func TestRepositoryScopeIsExplicitImmutableAndCloneIsolated(t *testing.T) {
	s, _, _, _ := fixture(t)
	root, linked := repositoryPair(t)
	sessions := localSessions(t, s, root, 3)
	linkedSession := localSessions(t, s, linked, 1)[0]
	legacy, err := s.RegisterSessionPeer(t.Context(), sessions[0].ID, "same_name", "Legacy")
	require.NoError(t, err)
	repository, err := s.RegisterRepositorySessionPeer(t.Context(), sessions[1].ID, "same_name", "Repository")
	require.NoError(t, err)
	require.NotEqual(t, legacy.ProjectID, repository.ProjectID)
	_, err = s.RegisterRepositorySessionPeer(t.Context(), sessions[0].ID, "same_name", "Legacy")
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.RegisterSessionPeer(t.Context(), sessions[1].ID, "same_name", "Repository")
	require.ErrorIs(t, err, ErrConflict)
	for _, id := range []string{sessions[0].ID, sessions[2].ID} {
		peers, err := s.LocalPeersForSession(t.Context(), id)
		require.NoError(t, err)
		require.Len(t, peers, 1)
		require.Equal(t, legacy.ID, peers[0].ID)
		require.Equal(t, "workspace", peers[0].Scope)
	}
	peers, err := s.LocalPeersForSession(t.Context(), linkedSession.ID)
	require.NoError(t, err)
	require.Empty(t, peers)
	cloneParent, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	other := filepath.Join(cloneParent, "separate clone")
	repositoryGit(t, cloneParent, "clone", "--no-hardlinks", root, other)
	otherSession := localSessions(t, s, other, 1)[0]
	outsider, err := s.RegisterRepositorySessionPeer(t.Context(), otherSession.ID, "outsider", "Isolated")
	require.NoError(t, err)
	require.NotEqual(t, repository.ProjectID, outsider.ProjectID)
	in := request("isolated")
	in.To = repository.Name
	_, err = s.Send(t.Context(), outsider.ID, in)
	require.Error(t, err)
	_, _, err = s.RegisterPeer(t.Context(), Peer{ProjectID: repository.ProjectID, Name: "bypass", Role: "Invalid", Workspace: root})
	require.Error(t, err)
	// Alias of a worktree root resolves to the same real identity, not a second repo.
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(root, alias))
	aliasSession := localSessions(t, s, alias, 1)[0]
	aliasPeer, err := s.RegisterRepositorySessionPeer(t.Context(), aliasSession.ID, "alias", "Canonical")
	require.NoError(t, err)
	require.Equal(t, repository.ProjectID, aliasPeer.ProjectID)
	require.Equal(t, repository.Workspace, aliasPeer.Workspace)
}
func TestRepositoryScopeRejectsInvalidRootsAndAmbientGitOverrides(t *testing.T) {
	s, _, _, _ := fixture(t)
	root, _ := repositoryPair(t)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "foreign"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.bare")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	metadata := localSessions(t, s, root, 1)[0]
	_, err := s.RegisterRepositorySessionPeer(t.Context(), metadata.ID, "valid", "Overrides removed")
	require.NoError(t, err)
	subdir := filepath.Join(root, "sub")
	require.NoError(t, os.Mkdir(subdir, 0700))
	bare := t.TempDir()
	repositoryGit(t, bare, "init", "--bare")
	for _, folder := range []string{subdir, bare, t.TempDir()} {
		ss := localSessions(t, s, folder, 1)[0]
		_, err := s.RegisterRepositorySessionPeer(t.Context(), ss.ID, "invalid", "Reject")
		require.Error(t, err)
	}
	require.NoError(t, os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "git-real")))
	require.NoError(t, os.Symlink(filepath.Join(root, "git-real"), filepath.Join(root, ".git")))
	_, err = s.PeerForSession(t.Context(), metadata.ID)
	require.Error(t, err)
}
func TestRepositoryScopeConcurrentRegistrationAndReplacementFence(t *testing.T) {
	s, _, _, _ := fixture(t)
	root, linked := repositoryPair(t)
	ss := localSessions(t, s, root, 1)[0]
	bs := localSessions(t, s, linked, 1)[0]
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			peer, err := s.RegisterRepositorySessionPeer(t.Context(), ss.ID, "front", "UI")
			ids <- peer.ID
			errs <- err
		})
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var id string
	for got := range ids {
		if id != "" {
			require.Equal(t, id, got)
		}
		id = got
	}
	front, err := s.PeerForSession(t.Context(), ss.ID)
	require.NoError(t, err)
	back, err := s.RegisterRepositorySessionPeer(t.Context(), bs.ID, "back", "API")
	require.NoError(t, err)
	in := request("before-replace")
	in.To = back.Name
	_, err = s.Send(t.Context(), front.ID, in)
	require.NoError(t, err)
	// Replace the common dir at its original pathname. A path-only hash would
	// accidentally preserve authority; the persisted filesystem identity does not.
	common := filepath.Join(root, ".git")
	old := filepath.Join(root, ".git-old")
	require.NoError(t, os.Rename(common, old))
	repositoryGit(t, root, "init")
	defer func() { _ = os.RemoveAll(common); _ = os.Rename(old, common) }()
	fresh, err := NewStore(t.Context(), s.db)
	require.NoError(t, err)
	_, err = fresh.PeerForSession(t.Context(), ss.ID)
	require.Error(t, err)
	_, err = fresh.LocalPeersForSession(t.Context(), bs.ID)
	require.Error(t, err)
	in.IdempotencyKey = "after-replace"
	_, err = fresh.Send(t.Context(), front.ID, in)
	require.Error(t, err)
	_, err = fresh.Claim(t.Context(), back.ID)
	require.Error(t, err)
	_, err = fresh.CreateContract(t.Context(), front.ID, contractSpec(front, "invalidated"))
	require.Error(t, err)
	bindings, bindingErr := fresh.DispatchBindings(t.Context(), "", 100)
	require.NoError(t, bindingErr)
	require.Len(t, bindings, 1)
	require.Error(t, bindings[0].validationErr)
	called := false
	err = fresh.WithOwnedPaths(t.Context(), front.ID, []string{"file.txt"}, func(context.Context) error { called = true; return nil })
	require.Error(t, err)
	require.False(t, called, "cached identity must not execute side effects")
	replacementSession := localSessions(t, fresh, root, 1)[0]
	_, err = fresh.RegisterRepositorySessionPeer(t.Context(), replacementSession.ID, "replacement", "Cannot inherit old authority")
	require.ErrorIs(t, err, ErrConflict)

}

func TestRepositoryScopeConcurrentScopeChoiceAndSessionRetargeting(t *testing.T) {
	s, _, _, _ := fixture(t)
	root, linked := repositoryPair(t)
	ss := localSessions(t, s, root, 1)[0]
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Go(func() {
		_, err := s.RegisterSessionPeer(t.Context(), ss.ID, "choice", "Same immutable identity")
		results <- err
	})
	wg.Go(func() {
		_, err := s.RegisterRepositorySessionPeer(t.Context(), ss.ID, "choice", "Same immutable identity")
		results <- err
	})
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrConflict)
			conflict++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_sessions WHERE session_id=$1`, ss.ID).Scan(&count))
	require.Equal(t, 1, count)
	repoSession := localSessions(t, s, root, 1)[0]
	peer, err := s.RegisterRepositorySessionPeer(t.Context(), repoSession.ID, "retarget", "Immutable root")
	require.NoError(t, err)
	_, err = s.db.ExecContext(t.Context(), `UPDATE sessions SET folder=$2 WHERE id=$1`, repoSession.ID, linked)
	require.NoError(t, err)
	_, err = s.PeerForSession(t.Context(), repoSession.ID)
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.Inbox(t.Context(), peer.ID, 10)
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.Peers(t.Context(), peer.ID)
	require.ErrorIs(t, err, ErrConflict)
}

func TestRepositoryScopeUnbornRootAndNamedAgentCollision(t *testing.T) {
	s, _, _, _ := fixture(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repositoryGit(t, root, "init")
	sessions := localSessions(t, s, root, 2)
	peer, err := s.RegisterRepositorySessionPeer(t.Context(), sessions[0].ID, "fresh", "Unborn repo")
	require.NoError(t, err)
	info, err := s.RepositoryInfoForSession(t.Context(), sessions[0].ID)
	require.NoError(t, err)
	require.Empty(t, info.Head)
	_, err = s.RegisterRepositorySessionPeer(t.Context(), sessions[1].ID, "fresh", "Cannot take over")
	require.ErrorIs(t, err, ErrConflict)
	bound, err := s.PeerForSession(t.Context(), sessions[0].ID)
	require.NoError(t, err)
	require.Equal(t, peer.ID, bound.ID)
	_, err = s.PeerForSession(t.Context(), sessions[1].ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRepositoryScopeInvalidBindingDoesNotStarveOtherProjects(t *testing.T) {
	s, ordinaryFrom, ordinaryBack, _ := fixture(t)
	bindFixture(t, s, ordinaryFrom, ordinaryBack)
	root, linked := repositoryPair(t)
	a := localSessions(t, s, root, 1)[0]
	b := localSessions(t, s, linked, 1)[0]
	front, err := s.RegisterRepositorySessionPeer(t.Context(), a.ID, "repo_front", "UI")
	require.NoError(t, err)
	back, err := s.RegisterRepositorySessionPeer(t.Context(), b.ID, "repo_back", "API")
	require.NoError(t, err)
	in := request("invalid-queued")
	in.To = back.Name
	invalid, err := s.Send(t.Context(), front.ID, in)
	require.NoError(t, err)
	ordinary, err := s.Send(t.Context(), ordinaryFrom.ID, request("ordinary-queued"))
	require.NoError(t, err)
	require.NoError(t, os.Rename(filepath.Join(root, ".git"), filepath.Join(root, ".git-old")))
	repositoryGit(t, root, "init")
	invalidReserved := make(chan struct{}, 4)
	invalidReported := make(chan struct{}, 4)
	executor := testExecutor{reserve: func(ctx context.Context, binding Binding) (Execution, error) {
		if binding.Peer.ProjectID == front.ProjectID {
			invalidReserved <- struct{}{}
		}
		return &testExecution{run: func(context.Context, Message) (json.RawMessage, error) { return json.RawMessage(`{"ok":true}`), nil }}, nil
	}}
	startDispatcher(t, s, executor, func(peer string, err error) {
		if peer == back.ID {
			select {
			case invalidReported <- struct{}{}:
			default:
			}
		}
	})
	waitState(t, s, ordinaryFrom.ID, ordinary.ID, Completed)
	select {
	case <-invalidReported:
	case <-time.After(3 * time.Second):
		t.Fatal("invalid binding was not reported")
	}
	select {
	case <-invalidReserved:
		t.Fatal("invalid cached peer reached executor reservation")
	default:
	}
	var state string
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT state FROM coordination_messages WHERE id=$1`, invalid.ID).Scan(&state))
	require.Equal(t, "accepted", state)
}
