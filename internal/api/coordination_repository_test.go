package api

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestCoordinationRepositoryRegistrationRPC(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	coord, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	root := t.TempDir()
	linked := filepath.Join(t.TempDir(), "linked checkout")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init")
	git("-c", "core.hooksPath="+os.DevNull, "commit", "--allow-empty", "-m", "fixture")
	git("-c", "core.hooksPath="+os.DevNull, "worktree", "add", "-b", "reviewer", linked)
	front, back, legacy := session.NewSession(root), session.NewSession(linked), session.NewSession(root)
	for _, s := range []*session.Session{front, back, legacy} {
		require.NoError(t, sessions.Create(t.Context(), s))
	}
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, CoordStore: coord})
	startHTTPServer(t, server)
	for i, s := range []*session.Session{front, back, legacy} {
		p := map[string]any{"session_id": s.ID, "name": []string{"frontend", "backend", "legacy"}[i], "role": "fixture"}
		if i < 2 {
			p["repository_scope"] = true
		}
		response := sidebarRPC(t, server, "coordination.register", p)
		require.Nil(t, response.Error)
	}
	f, err := coord.PeerForSession(t.Context(), front.ID)
	require.NoError(t, err)
	b, err := coord.PeerForSession(t.Context(), back.ID)
	require.NoError(t, err)
	l, err := coord.PeerForSession(t.Context(), legacy.ID)
	require.NoError(t, err)
	require.Equal(t, f.ProjectID, b.ProjectID)
	require.NotEqual(t, f.Workspace, b.Workspace)
	require.NotEqual(t, f.ProjectID, l.ProjectID)
	response := sidebarRPC(t, server, "coordination.peers", map[string]any{"session_id": front.ID})
	require.Nil(t, response.Error)
	data, err := json.Marshal(response.Result)
	require.NoError(t, err)
	require.Contains(t, string(data), `"branch":"reviewer"`)
	require.NotContains(t, string(data), `"name":"legacy"`)
	for _, extra := range []map[string]any{{"repository_scope": "true"}, {"project_id": f.ProjectID}, {"workspace": linked}} {
		p := map[string]any{"session_id": legacy.ID, "name": "legacy", "role": "fixture"}
		for k, v := range extra {
			p[k] = v
		}
		response := sidebarRPC(t, server, "coordination.register", p)
		require.NotNil(t, response.Error)
	}
	response = sidebarRPC(t, server, "coordination.register", map[string]any{"session_id": legacy.ID, "name": "legacy", "role": "fixture", "repository_scope": true})
	require.NotNil(t, response.Error)
}
