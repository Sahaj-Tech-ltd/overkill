package acp

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

// The helper is the actual ACP HTTP server in an independent OS process, with
// no provider/runtime executor. Manual authenticated claim represents external
// processing admission; interrupted claims are never automatically replayed.
func TestCoordinationIngressProcessHelper(t *testing.T) {
	if os.Getenv("OVERKILL_COORD_PROCESS_HELPER") != "1" {
		return
	}
	db, err := sql.Open("postgres", os.Getenv("OVERKILL_COORD_PROCESS_DSN"))
	if err != nil {
		os.Exit(2)
	}
	defer db.Close()
	store, err := coordination.NewStore(context.Background(), db)
	if err != nil {
		os.Exit(3)
	}
	server := NewServer(Config{Addr: "127.0.0.1:0", Token: "not-a-peer", Coordination: store})
	if server.Start() != nil {
		os.Exit(4)
	}
	fmt.Println("COORD_READY " + server.Addr())
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	_ = server.Shutdown(context.Background())
}
func TestCoordinationScopedCorrespondenceSurvivesActualProcessRestart(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	_, ft, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "p", Name: "frontend", Role: "UI", Workspace: t.TempDir()})
	require.NoError(t, err)
	_, bt, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "p", Name: "backend", Role: "API", Workspace: t.TempDir()})
	require.NoError(t, err)
	var schema string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT current_schema()`).Scan(&schema))
	dsn := os.Getenv("PG_TEST_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, e := url.Parse(dsn)
		require.NoError(t, e)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	start := func() (*exec.Cmd, string) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCoordinationIngressProcessHelper$", "-test.timeout=45s")
		cmd.Env = append(os.Environ(), "OVERKILL_COORD_PROCESS_HELPER=1", "OVERKILL_COORD_PROCESS_DSN="+dsn)
		stdout, e := cmd.StdoutPipe()
		require.NoError(t, e)
		stdin, e := cmd.StdinPipe()
		require.NoError(t, e)
		require.NoError(t, cmd.Start())
		t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill() })
		ready := make(chan string, 1)
		go func() {
			scan := bufio.NewScanner(stdout)
			for scan.Scan() {
				if strings.HasPrefix(scan.Text(), "COORD_READY ") {
					ready <- strings.TrimPrefix(scan.Text(), "COORD_READY ")
					return
				}
			}
			ready <- ""
		}()
		select {
		case address := <-ready:
			require.NotEmpty(t, address)
			return cmd, "http://" + address
		case <-time.After(10 * time.Second):
			t.Fatal("ACP child did not start")
			return nil, ""
		}
	}
	first, endpoint := start()
	f := NewClient(endpoint, ft)
	b := NewClient(endpoint, bt)
	input := CoordinationRequest{Send: &coordination.SendInput{To: "backend", Kind: "request", TaskID: "durable-users", Payload: json.RawMessage(`{"question":"GET /users schema?"}`), IdempotencyKey: "one-work-effect"}}
	receipt, err := f.Coordination(t.Context(), "send", input)
	require.NoError(t, err)
	admitted, err := b.Coordination(t.Context(), "claim", CoordinationRequest{})
	require.NoError(t, err)
	require.Equal(t, receipt.Message.ID, admitted.Message.ID)
	effectPath := filepath.Join(t.TempDir(), "effects.log")
	require.NoError(t, os.WriteFile(effectPath, []byte(admitted.Message.ID+"\n"), 0600))
	require.NoError(t, first.Process.Kill())
	require.Error(t, first.Wait())
	second, endpoint := start()
	defer func() { _ = second.Process.Kill(); _ = second.Wait() }()
	f = NewClient(endpoint, ft)
	b = NewClient(endpoint, bt)
	retried, err := f.Coordination(t.Context(), "send", input)
	require.NoError(t, err)
	require.Equal(t, receipt.Message.ID, retried.Message.ID)
	require.Equal(t, receipt.Message.TaskID, retried.Message.TaskID)
	require.Equal(t, receipt.Message.ConversationID, retried.Message.ConversationID)
	require.Equal(t, receipt.Message.From, retried.Message.From)
	require.Equal(t, receipt.Message.To, retried.Message.To)
	require.Equal(t, receipt.Message.Kind, retried.Message.Kind)
	require.JSONEq(t, string(receipt.Message.Payload), string(retried.Message.Payload))
	require.True(t, receipt.Message.CreatedAt.Equal(retried.Message.CreatedAt))
	require.True(t, receipt.Message.Deadline.Equal(retried.Message.Deadline))
	require.Equal(t, coordination.Started, retried.Message.State)
	if _, err = b.Coordination(t.Context(), "claim", CoordinationRequest{}); err == nil {
		f, e := os.OpenFile(effectPath, os.O_APPEND|os.O_WRONLY, 0600)
		require.NoError(t, e)
		_, e = f.WriteString(receipt.Message.ID + "\n")
		require.NoError(t, e)
		require.NoError(t, f.Close())
		t.Fatal("interrupted processing was admitted twice")
	}
	effectBytes, err := os.ReadFile(effectPath)
	require.NoError(t, err)
	require.Equal(t, receipt.Message.ID+"\n", string(effectBytes))
	answer, err := b.Coordination(t.Context(), "reply", CoordinationRequest{MessageID: receipt.Message.ID, Payload: json.RawMessage(`{"schema":"users[]"}`), IdempotencyKey: "same-answer"})
	require.NoError(t, err)
	require.Equal(t, receipt.Message.ConversationID, answer.Message.ConversationID)
	require.Equal(t, receipt.Message.ID, answer.Message.InReplyTo)
	require.Equal(t, "durable-users", answer.Message.TaskID)
	replay, err := b.Coordination(t.Context(), "reply", CoordinationRequest{MessageID: receipt.Message.ID, Payload: json.RawMessage(`{"schema":"users[]"}`), IdempotencyKey: "same-answer"})
	require.NoError(t, err)
	require.Equal(t, answer.Message.ID, replay.Message.ID)
	status, err := f.Coordination(t.Context(), "status", CoordinationRequest{MessageID: receipt.Message.ID})
	require.NoError(t, err)
	require.Equal(t, coordination.Completed, status.Message.State)
}
