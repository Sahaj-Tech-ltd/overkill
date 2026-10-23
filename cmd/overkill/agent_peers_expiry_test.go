package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestPeerCLIExpiringProvisionAndSingleCredentialRetirement(t *testing.T) {
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	workspace := t.TempDir()
	front, back := session.NewSession(workspace), session.NewSession(workspace)
	require.NoError(t, sessions.Create(t.Context(), front))
	require.NoError(t, sessions.Create(t.Context(), back))
	_, err = store.RegisterSessionPeer(t.Context(), front.ID, "frontend", "UI")
	require.NoError(t, err)
	backend, err := store.RegisterSessionPeer(t.Context(), back.ID, "backend", "API")
	require.NoError(t, err)
	var schema string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT current_schema()`).Scan(&schema))
	dsn := os.Getenv("PG_TEST_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	t.Setenv("DATABASE_URL", dsn)
	issue := func(path string, ttlArgs ...string) time.Time {
		command := newAgentPeersCommand()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		args := []string{"configure", "--anchor-session", front.ID, "--session", back.ID, "--endpoint", "http://127.0.0.1:8421", "--credential-out", path}
		args = append(args, ttlArgs...)
		command.SetArgs(args)
		require.NoError(t, command.ExecuteContext(t.Context()))
		var metadata struct {
			Expires time.Time `json:"credential_expires_at"`
		}
		require.NoError(t, json.Unmarshal(output.Bytes(), &metadata))
		require.False(t, metadata.Expires.IsZero())
		secret, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotContains(t, output.String(), string(secret))
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		return metadata.Expires
	}
	firstPath, secondPath := filepath.Join(t.TempDir(), "short-private"), filepath.Join(t.TempDir(), "replacement-private")
	expiry := issue(firstPath, "--ttl", "1m")
	first, err := os.ReadFile(firstPath)
	require.NoError(t, err)
	hash := sha256.Sum256(first)
	var stored time.Time
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT expires_at FROM coordination_credentials WHERE token_hash=$1`, hash[:]).Scan(&stored))
	require.True(t, stored.Equal(expiry))
	issue(secondPath)
	second, err := os.ReadFile(secondPath)
	require.NoError(t, err)
	secondHash := sha256.Sum256(second)
	var seconds float64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT extract(epoch FROM expires_at-clock_timestamp()) FROM coordination_credentials WHERE token_hash=$1`, secondHash[:]).Scan(&seconds))
	require.Greater(t, seconds, float64(23*time.Hour/time.Second))
	require.LessOrEqual(t, seconds, float64(24*time.Hour/time.Second))
	command := newAgentPeersCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"revoke", "--anchor-session", front.ID, "--session", back.ID, "--credential-file", firstPath})
	require.NoError(t, command.ExecuteContext(t.Context()))
	require.NotContains(t, output.String(), string(first))
	require.NotContains(t, output.String(), string(second))
	_, err = store.Authenticate(t.Context(), string(first))
	require.ErrorIs(t, err, coordination.ErrUnauthorized)
	actor, err := store.Authenticate(t.Context(), string(second))
	require.NoError(t, err)
	require.Equal(t, backend.ID, actor.ID)
	require.True(t, actor.Enabled)
	badPath := filepath.Join(t.TempDir(), "must-not-exist")
	bad := newAgentPeersCommand()
	bad.SetOut(&output)
	bad.SetErr(&output)
	bad.SetArgs([]string{"configure", "--anchor-session", front.ID, "--session", back.ID, "--endpoint", "http://127.0.0.1:8421", "--credential-out", badPath, "--ttl", "0"})
	require.Error(t, bad.ExecuteContext(t.Context()))
	_, err = os.Stat(badPath)
	require.True(t, os.IsNotExist(err))
}
