//go:build unix

package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

// TestLSPProcessFixture is a controlled executable, not an installed LSP.
func TestLSPProcessFixture(t *testing.T) {
	var args []string
	for i, arg := range os.Args {
		if arg == "--lsp-fixture" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) != 2 {
		return
	}
	mode, path := args[0], args[1]
	record := func(event string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprintf(f, "%s %d\n", event, os.Getpid())
		_ = f.Close()
	}
	record("spawn")
	input := bufio.NewReader(os.Stdin)
	for {
		size := 0
		for {
			line, err := input.ReadString('\n')
			if err != nil {
				os.Exit(0)
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				size, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			}
		}
		if size <= 0 || size > 1024*1024 {
			os.Exit(2)
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(input, body); err != nil {
			os.Exit(0)
		}
		var message jsonrpcMessage
		if json.Unmarshal(body, &message) != nil {
			os.Exit(2)
		}
		record(message.Method)
		result := json.RawMessage(`null`)
		switch message.Method {
		case "initialize":
			if mode == "hang" {
				continue
			}
			if mode == "fail" {
				os.Exit(3)
			}
			result = json.RawMessage(`{"capabilities":{}}`)
		case "textDocument/hover":
			result = json.RawMessage(`{"contents":"controlled hover"}`)
		case "exit":
			os.Exit(0)
		}
		if len(message.ID) > 0 {
			send(os.Stdout, jsonrpcMessage{JSONRPC: "2.0", ID: message.ID, Result: result})
		}
	}
}

func fixtureConfig(t *testing.T, mode string) (config.LSPConfig, string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	marker := t.TempDir() + "/lsp.events"
	return config.LSPConfig{Servers: []config.LSPServer{{Language: "fixture", Command: executable, Args: []string{"-test.run=^TestLSPProcessFixture$", "--", "--lsp-fixture", mode, marker}, Filetypes: []string{".fixture"}}}}, marker
}
func waitFixture(t *testing.T, path, event string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		b, _ := os.ReadFile(path)
		for _, line := range strings.Split(string(b), "\n") {
			var name string
			fmt.Sscanf(line, "%s %d", &name, &pid)
			if name == event {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
	return pid
}
func requireReaped(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }, 2*time.Second, 10*time.Millisecond)
}

func TestManagerConcurrentStartStopOwnsSingleProcess(t *testing.T) {
	cfg, marker := fixtureConfig(t, "ready")
	manager := NewManager(cfg, t.TempDir())
	defer manager.Stop()
	// Mutating retained caller configuration must not alter launch/routing.
	cfg.Servers[0].Args[0] = "--wrong-argument"
	cfg.Servers[0].Filetypes[0] = ".wrong"
	var starters sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		starters.Go(func() { results <- manager.Start(t.Context()) })
	}
	starters.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	pid := waitFixture(t, marker, "initialized")
	require.Equal(t, 1, manager.ConnectedCount())
	client := manager.ClientForFile("a.fixture")
	require.NotNil(t, client)
	hover, err := client.Hover(t.Context(), "a.fixture", 0, 0)
	require.NoError(t, err)
	require.Equal(t, "controlled hover", hover.Contents)
	var stoppers sync.WaitGroup
	for range 12 {
		stoppers.Go(manager.Stop)
	}
	stoppers.Wait()
	requireReaped(t, pid)
	b, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(b), "spawn "))
	require.Equal(t, 1, strings.Count(string(b), "shutdown "))
	require.Equal(t, 1, strings.Count(string(b), "exit "))
	require.Nil(t, manager.ClientForFile("a.fixture"))
	require.ErrorContains(t, manager.Start(t.Context()), "stopped")
}

func TestManagerStopInterruptsPendingInitialize(t *testing.T) {
	for range 3 {
		cfg, marker := fixtureConfig(t, "hang")
		manager := NewManager(cfg, t.TempDir())
		defer manager.Stop()
		started := make(chan error, 1)
		go func() { started <- manager.Start(context.Background()) }()
		pid := waitFixture(t, marker, "initialize")
		stopped := make(chan struct{})
		go func() { manager.Stop(); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Fatal("Stop left initialization/process running")
		}
		select {
		case err := <-started:
			require.Error(t, err)
		case <-time.After(time.Second):
			t.Fatal("Start not released")
		}
		requireReaped(t, pid)
		require.Zero(t, manager.ConnectedCount())
	}
}

func TestManagerReadinessHasIndependentDeadline(t *testing.T) {
	cfg, marker := fixtureConfig(t, "hang")
	manager := NewManager(cfg, t.TempDir())
	defer manager.Stop()
	start := time.Now()
	err := manager.Start(context.Background())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), InitializeTimeout+3*time.Second)
	require.Zero(t, manager.ConnectedCount())
	pid := waitFixture(t, marker, "initialize")
	requireReaped(t, pid)
}

func TestManagerRejectsDuplicateDeclarationsBeforeLaunching(t *testing.T) {
	for _, duplicateLanguage := range []bool{true, false} {
		cfg, marker := fixtureConfig(t, "ready")
		second := cfg.Servers[0]
		if !duplicateLanguage {
			second.Language = "other"
		}
		cfg.Servers = append(cfg.Servers, second)
		manager := NewManager(cfg, t.TempDir())
		require.Error(t, manager.Start(t.Context()))
		manager.Stop()
		_, err := os.Stat(marker)
		require.True(t, os.IsNotExist(err), "duplicate declaration launched a process")
	}
}

func TestClientCloseBeforeStartIsTerminal(t *testing.T) {
	cfg, marker := fixtureConfig(t, "ready")
	server := cfg.Servers[0]
	client := NewClient(server.Language, server.Command, server.Args, 0)
	require.NoError(t, client.Close())
	require.ErrorContains(t, client.Start(t.Context(), t.TempDir()), "closed")
	_, err := os.Stat(marker)
	require.True(t, os.IsNotExist(err))
}

func TestJSONRPCBlockedWriteCancellationOwnsPipes(t *testing.T) {
	stdinRead, stdinWrite := io.Pipe()
	defer stdinRead.Close()
	stdoutRead, stdoutWrite := io.Pipe()
	defer stdoutWrite.Close()
	conn := newJSONRPCConn(stdinWrite, stdoutRead, 0)
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	_, err := conn.Call(ctx, "blocked-write", map[string]any{"text": strings.Repeat("x", 100000)})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.True(t, conn.closed.Load())
	count := 0
	conn.pending.Range(func(_, _ any) bool { count++; return true })
	require.Zero(t, count)
}

func TestJSONRPCOversizedHeaderIsBounded(t *testing.T) {
	conn := newJSONRPCConn(io.Discard, strings.NewReader(strings.Repeat("x", 128*1024)), 0)
	require.ErrorIs(t, conn.readLoop(), bufio.ErrBufferFull)
	require.True(t, conn.closed.Load())
}

func TestJSONRPCConcurrentCloseAndResponseReleaseEveryWaiter(t *testing.T) {
	conn := newJSONRPCConn(io.Discard, strings.NewReader(""), 0)
	var responders sync.WaitGroup
	for id := int64(1); id <= 2000; id++ {
		conn.pending.Store(id, make(chan *jsonrpcMessage, 1))
		responders.Go(func() {
			conn.dispatch(&jsonrpcMessage{ID: json.RawMessage(strconv.FormatInt(id, 10)), Result: json.RawMessage(`null`)})
		})
		if id%8 == 0 {
			responders.Go(func() { conn.failAllPending(io.ErrClosedPipe) })
		}
	}
	finished := make(chan struct{})
	go func() { responders.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("response dispatch blocked while connection closed")
	}
	conn.close()
	remaining := 0
	conn.pending.Range(func(_, _ any) bool { remaining++; return true })
	require.Zero(t, remaining)
}
