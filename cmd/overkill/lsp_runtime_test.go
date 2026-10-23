//go:build unix

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/lsp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

type lspFixtureEvent struct {
	Method string          `json:"method"`
	PID    int             `json:"pid"`
	CWD    string          `json:"cwd"`
	Params json.RawMessage `json:"params,omitempty"`
}

// The configured subprocess is this controlled test executable. Production
// BuildSession must launch/initialize it; callers never manually start Manager.
func TestRuntimeLSPExecutableFixture(t *testing.T) {
	var args []string
	for i, arg := range os.Args {
		if arg == "--runtime-lsp-fixture" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) != 2 {
		return
	}
	mode, marker := args[0], args[1]
	cwd, _ := os.Getwd()
	record := func(method string, params json.RawMessage) {
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			os.Exit(2)
		}
		b, _ := json.Marshal(lspFixtureEvent{Method: method, PID: os.Getpid(), CWD: cwd, Params: params})
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
	record("spawn", nil)
	if mode == "leaf" {
		record("leaf", nil)
		for {
			f, err := os.OpenFile(marker+".heartbeat", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				os.Exit(2)
			}
			_, _ = f.WriteString("tick\n")
			_ = f.Close()
			time.Sleep(10 * time.Millisecond)
		}
	}
	if mode == "tree" {
		executable, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		child := exec.Command(executable, "-test.run=^TestRuntimeLSPExecutableFixture$", "--", "--runtime-lsp-fixture", "leaf", marker)
		if child.Start() != nil {
			os.Exit(2)
		}
		go func() { _ = child.Wait() }()
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		size := 0
		for {
			line, err := reader.ReadString('\n')
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
		if _, err := io.ReadFull(reader, body); err != nil {
			os.Exit(0)
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &msg) != nil {
			os.Exit(2)
		}
		record(msg.Method, msg.Params)
		result := json.RawMessage(`null`)
		switch msg.Method {
		case "initialize":
			if mode == "hang" {
				continue
			}
			if mode == "fail" {
				os.Exit(3)
			}
			result = json.RawMessage(`{"capabilities":{"hoverProvider":true}}`)
		case "textDocument/hover":
			result = json.RawMessage(`{"contents":"shared-runtime controlled hover"}`)
		case "exit":
			os.Exit(0)
		}
		if len(msg.ID) > 0 {
			response, _ := json.Marshal(struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Result  json.RawMessage `json:"result"`
			}{"2.0", msg.ID, result})
			fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(response), response)
		}
	}
}

func runtimeLSPConfig(t *testing.T, mode string) (*config.Config, string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	marker := filepath.Join(t.TempDir(), "lsp.jsonl")
	cfg := config.Default()
	cfg.LSP = config.LSPConfig{Servers: []config.LSPServer{{Language: "fixture", Command: executable, Args: []string{"-test.run=^TestRuntimeLSPExecutableFixture$", "--", "--runtime-lsp-fixture", mode, marker}, Filetypes: []string{".fixture"}}}}
	return cfg, marker
}
func runtimeLSPEvents(path string) []lspFixtureEvent {
	data, _ := os.ReadFile(path)
	var events []lspFixtureEvent
	for _, line := range strings.Split(string(data), "\n") {
		var event lspFixtureEvent
		if json.Unmarshal([]byte(line), &event) == nil {
			events = append(events, event)
		}
	}
	return events
}
func waitRuntimeLSP(t *testing.T, path, method string) lspFixtureEvent {
	t.Helper()
	var found lspFixtureEvent
	require.Eventually(t, func() bool {
		for _, event := range runtimeLSPEvents(path) {
			if event.Method == method {
				found = event
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
	return found
}
func assertRuntimeLSPGone(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }, 2*time.Second, 10*time.Millisecond)
}

func TestConfiguredLSPSharedRuntimeOwnsInitializeHoverShutdown(t *testing.T) {
	cfg, marker := runtimeLSPConfig(t, "tree")
	services, err := newRuntimeServices(cfg, nil, t.TempDir())
	require.NoError(t, err)
	workspace := filepath.Join(t.TempDir(), "workspace with spaces")
	require.NoError(t, os.Mkdir(workspace, 0700))
	workspace, err = filepath.EvalSymlinks(workspace)
	require.NoError(t, err)
	metadata := session.NewSession(workspace)
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	built, err := services.BuildSession(requestCtx, metadata)
	require.NoError(t, err)
	defer built.Cleanup()
	initialized := waitRuntimeLSP(t, marker, "initialize")
	leaf := waitRuntimeLSP(t, marker, "leaf")
	require.NotEqual(t, initialized.PID, leaf.PID)
	require.Eventually(t, func() bool {
		data, _ := os.ReadFile(marker + ".heartbeat")
		return len(data) > 0
	}, 2*time.Second, 10*time.Millisecond)
	var params struct {
		RootURI string `json:"rootUri"`
	}
	require.NoError(t, json.Unmarshal(initialized.Params, &params))
	require.Equal(t, workspace, lsp.URIToPath(params.RootURI))
	require.Equal(t, workspace, initialized.CWD)
	// Ending the HTTP/assembly request must not kill a session-owned server.
	cancelRequest()
	output, err := runtimeTool(built.Tools, "lsp_hover", map[string]any{"file": "source.fixture", "line": 1, "column": 2})
	require.NoError(t, err)
	require.Contains(t, string(output), "shared-runtime controlled hover")
	hover := waitRuntimeLSP(t, marker, "textDocument/hover")
	var hoverParams struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	require.NoError(t, json.Unmarshal(hover.Params, &hoverParams))
	require.Equal(t, filepath.Join(workspace, "source.fixture"), lsp.URIToPath(hoverParams.TextDocument.URI))
	var cleanup sync.WaitGroup
	for range 8 {
		cleanup.Go(built.Cleanup)
	}
	cleanup.Wait()
	waitRuntimeLSP(t, marker, "shutdown")
	waitRuntimeLSP(t, marker, "exit")
	assertRuntimeLSPGone(t, initialized.PID)
	// The server's subprocess is in the owned process group too. Checking
	// actual controlled work avoids treating an unreaped zombie as live work.
	after, err := os.ReadFile(marker + ".heartbeat")
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	final, err := os.ReadFile(marker + ".heartbeat")
	require.NoError(t, err)
	require.Equal(t, string(after), string(final), "LSP descendant continued work after session cleanup")
	_, err = runtimeTool(built.Tools, "lsp_hover", map[string]any{"file": "source.fixture"})
	require.ErrorContains(t, err, "no language server")
}

func TestConfiguredLSPAssemblyFailureStopsOtherInitializedServer(t *testing.T) {
	cfg, readyMarker := runtimeLSPConfig(t, "ready")
	failing, failedMarker := runtimeLSPConfig(t, "fail")
	failedServer := failing.LSP.Servers[0]
	failedServer.Language = "failing"
	failedServer.Filetypes = []string{".failing"}
	cfg.LSP.Servers = append(cfg.LSP.Servers, failedServer)
	services, err := newRuntimeServices(cfg, nil, t.TempDir())
	require.NoError(t, err)
	built, err := services.BuildSession(t.Context(), session.NewSession(t.TempDir()))
	require.Nil(t, built)
	require.ErrorContains(t, err, "LSP startup")
	ready := waitRuntimeLSP(t, readyMarker, "initialize")
	failed := waitRuntimeLSP(t, failedMarker, "initialize")
	waitRuntimeLSP(t, readyMarker, "shutdown")
	assertRuntimeLSPGone(t, ready.PID)
	assertRuntimeLSPGone(t, failed.PID)
}

func TestConfiguredLSPAssemblyCancellationReapsInitializingServer(t *testing.T) {
	cfg, marker := runtimeLSPConfig(t, "hang")
	services, err := newRuntimeServices(cfg, nil, t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		built, err := services.BuildSession(ctx, session.NewSession(t.TempDir()))
		if built != nil {
			built.Cleanup()
		}
		done <- err
	}()
	event := waitRuntimeLSP(t, marker, "initialize")
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("assembly cancellation left LSP starting")
	}
	assertRuntimeLSPGone(t, event.PID)
}
