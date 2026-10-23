package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/mcp/testfixture"
	"github.com/stretchr/testify/require"
)

func TestControlledMCPFixture(t *testing.T) {
	if testfixture.Run() {
		os.Exit(0)
	}
}
func policyFixture(t *testing.T, edit func(*config.MCPServer)) (*Manager, string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	effects := filepath.Join(t.TempDir(), "effects")
	declaration := config.MCPServer{Name: "fixture", Command: executable, Args: []string{"-test.run=^TestControlledMCPFixture$"}, Env: map[string]string{"OVERKILL_MCP_CONTROLLED_FIXTURE": "1", "OVERKILL_MCP_FIXTURE_EFFECTS": effects}}
	if edit != nil {
		edit(&declaration)
	}
	manager := NewManager(config.MCPConfig{Servers: []config.MCPServer{declaration}})
	require.NoError(t, manager.Start(t.Context()))
	t.Cleanup(manager.Stop)
	require.Eventually(t, func() bool { statuses := manager.Status(); return len(statuses) == 1 && statuses[0].Connected }, 3*time.Second, 10*time.Millisecond)
	return manager, effects
}
func TestConfiguredMCPPolicyDeniesBeforeRemoteEffect(t *testing.T) {
	for _, mode := range []string{"allowlist_trusted", "nontrusted", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			manager, effects := policyFixture(t, func(server *config.MCPServer) {
				switch mode {
				case "allowlist_trusted":
					trusted := true
					server.Trusted = &trusted
					server.AllowedTools = []string{"read"}
				case "nontrusted":
					trusted := false
					server.Trusted = &trusted
				case "bytes":
					server.MaxBytesPerCall = 4
				}
			})
			_, err := manager.Call(t.Context(), "fixture", "forbidden", json.RawMessage(`{"payload":"too large"}`))
			require.Error(t, err)
			_, err = os.Stat(effects)
			require.True(t, os.IsNotExist(err), "denied call reached the controlled remote")
		})
	}
	manager, effects := policyFixture(t, nil)
	_, err := manager.Call(t.Context(), "fixture", "read", json.RawMessage(`{}`))
	require.NoError(t, err)
	_, err = os.Stat(effects)
	require.NoError(t, err)
}
func TestConfiguredMCPPathsCanonicalizeAndGateLiveArguments(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "allowed")
	outside := t.TempDir()
	require.NoError(t, os.Mkdir(root, 0700))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))
	manager, effects := policyFixture(t, func(server *config.MCPServer) {
		server.PathWorkspace = workspace
		server.AllowedPathPrefixes = []string{root}
		server.PathArgumentBindings = map[string][]string{"read": {"/path", "/paths", "/nested/a~1b"}, "nop": {}}
	})
	good := json.RawMessage(`{"path":"allowed/new/file.txt","paths":["allowed/one","allowed/two"],"nested":{"a/b":"allowed/nested"}}`)
	result, err := manager.Call(t.Context(), "fixture", "read", good)
	require.NoError(t, err)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.Contains(t, result.Text, filepath.Join(canonicalRoot, "new", "file.txt"))
	before, err := os.ReadFile(effects)
	require.NoError(t, err)
	for _, path := range []string{filepath.Join(outside, "file"), "allowed/escape/new/file", "allowed/../../outside", ""} {
		arguments := map[string]any{"path": path, "paths": []string{"allowed/one"}, "nested": map[string]any{"a/b": "allowed/nested"}}
		encoded, err := json.Marshal(arguments)
		require.NoError(t, err)
		_, err = manager.Call(t.Context(), "fixture", "read", encoded)
		require.Error(t, err)
	}
	_, err = manager.Call(t.Context(), "fixture", "read", json.RawMessage(`{"path":"allowed/one","paths":[2],"nested":{"a/b":"allowed/two"}}`))
	require.Error(t, err)
	_, err = manager.Call(t.Context(), "fixture", "forbidden", json.RawMessage(`{"path":"allowed/one"}`))
	require.ErrorContains(t, err, "path_argument_bindings")
	after, err := os.ReadFile(effects)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "out-of-root/unsupported calls reached remote")
	_, err = manager.Call(t.Context(), "fixture", "nop", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.NotContains(t, strings.Join(func() []string {
		var names []string
		for _, entry := range manager.Tools() {
			names = append(names, entry.Tool.Name)
		}
		return names
	}(), ","), "forbidden")
}
func TestMCPInvalidPathDeclarationFailsBeforeProcessLaunch(t *testing.T) {
	manager := NewManager(config.MCPConfig{Servers: []config.MCPServer{{Name: "restricted", Command: "must-not-launch", AllowedPathPrefixes: []string{t.TempDir()}}}})
	require.ErrorContains(t, manager.Start(t.Context()), "path_workspace")
	manager.Stop()
}

func TestMCPStopCancelsDelayedInitializationAndCannotRestart(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	marker := filepath.Join(t.TempDir(), "launched")
	manager := NewManager(config.MCPConfig{Servers: []config.MCPServer{{Name: "slow", Command: executable, Args: []string{"-test.run=^TestControlledMCPFixture$"}, Env: map[string]string{"OVERKILL_MCP_CONTROLLED_FIXTURE": "1", "OVERKILL_MCP_FIXTURE_LAUNCHED": marker, "OVERKILL_MCP_FIXTURE_DELAY_MS": "10000"}}}})
	require.NoError(t, manager.Start(t.Context()))
	require.NoError(t, manager.Start(t.Context()))
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, time.Second, 10*time.Millisecond)
	done := make(chan struct{})
	go func() { manager.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("initializing MCP process outlived manager shutdown")
	}
	require.Empty(t, manager.Tools())
	require.ErrorContains(t, manager.Start(t.Context()), "stopped")
	manager.Stop()
}
func TestPathBindingsRejectUnknownSchemaAndDanglingSymlinks(t *testing.T) {
	workspace := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(workspace, "dangling")))
	_, err := canonicalLocalPath(filepath.Join(workspace, "dangling", "new"))
	require.Error(t, err)
	declaration := config.MCPServer{PathWorkspace: workspace, PathArgumentBindings: map[string][]string{"read": {"/path"}}}
	_, _, err = bindCallPaths(declaration, Tool{Name: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"oneOf":[{"type":"string"},{"type":"array"}]}}}`)}, json.RawMessage(`{"path":"file"}`))
	require.ErrorContains(t, err, "schema")
	for _, pointer := range []string{"path", "/bad~escape", "/bad~2", ""} {
		_, err := pointerParts(pointer)
		require.Error(t, err)
	}
}

func TestWorkspaceRelativeMCPCommandAndImmutableConfigSnapshot(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	workspace := t.TempDir()
	localBinary := filepath.Join(workspace, "controlled-server")
	data, err := os.ReadFile(executable)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(localBinary, data, 0700))
	effects := filepath.Join(workspace, "effects")
	trusted := false
	source := config.MCPConfig{Servers: []config.MCPServer{{Name: "fixture", Command: "./controlled-server", Args: []string{"-test.run=^TestControlledMCPFixture$"}, Env: map[string]string{"OVERKILL_MCP_CONTROLLED_FIXTURE": "1", "OVERKILL_MCP_FIXTURE_EFFECTS": effects}, Trusted: &trusted, AllowedTools: []string{"read"}, PathWorkspace: workspace, AllowedPathPrefixes: []string{workspace}, PathArgumentBindings: map[string][]string{"read": {"/path"}}}}}
	manager := NewManager(source)
	t.Cleanup(manager.Stop)
	// Mutation after construction must not alter commands, launch environment,
	// allowed tools, trust default, canonical roots or argument bindings.
	source.Servers[0].Args[0] = "-test.run=^DoesNotExist$"
	source.Servers[0].Env["OVERKILL_MCP_CONTROLLED_FIXTURE"] = "0"
	source.Servers[0].AllowedTools[0] = "forbidden"
	trusted = true
	source.Servers[0].AllowedPathPrefixes[0] = t.TempDir()
	source.Servers[0].PathArgumentBindings["read"][0] = "/missing"
	source.Servers[0].Command = "must-not-execute"
	require.NoError(t, manager.Start(t.Context()))
	require.Eventually(t, func() bool { status := manager.Status(); return len(status) == 1 && status[0].Connected }, 3*time.Second, 10*time.Millisecond)
	_, err = manager.Call(t.Context(), "fixture", "read", json.RawMessage(`{"path":"new-file"}`))
	require.NoError(t, err)
	before, err := os.ReadFile(effects)
	require.NoError(t, err)
	_, err = manager.Call(t.Context(), "fixture", "forbidden", json.RawMessage(`{}`))
	require.Error(t, err)
	after, err := os.ReadFile(effects)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}
func TestNegativeMCPByteLimitRejectsBeforeLaunch(t *testing.T) {
	manager := NewManager(config.MCPConfig{Servers: []config.MCPServer{{Name: "fixture", Command: "must-not-launch", MaxBytesPerCall: -1}}})
	require.ErrorContains(t, manager.Start(t.Context()), "negative")
	manager.Stop()
}

func TestTOMLMCPEnabledPresenceControlsActualLaunch(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, enabled := range []string{"", "enabled = true\n", "enabled = false\n"} {
		t.Run(strings.TrimSpace(enabled), func(t *testing.T) {
			directory := t.TempDir()
			marker := filepath.Join(directory, "launched")
			path := filepath.Join(directory, "config.toml")
			source := fmt.Sprintf("version = 1\n[[mcp.servers]]\nname = 'fixture'\ncommand = %q\nargs = ['-test.run=^TestControlledMCPFixture$']\n%s[mcp.servers.env]\nOVERKILL_MCP_CONTROLLED_FIXTURE = '1'\nOVERKILL_MCP_FIXTURE_LAUNCHED = %q\n", executable, enabled, marker)
			require.NoError(t, os.WriteFile(path, []byte(source), 0600))
			cfg, err := config.Load(path)
			require.NoError(t, err)
			if enabled == "" {
				require.Nil(t, cfg.MCP.Servers[0].Enabled)
			} else {
				require.NotNil(t, cfg.MCP.Servers[0].Enabled)
			}
			manager := NewManager(cfg.MCP)
			t.Cleanup(manager.Stop)
			if enabled == "enabled = false\n" {
				// Even changing the caller's flag cannot enable the manager's snapshot.
				*cfg.MCP.Servers[0].Enabled = true
				require.NoError(t, manager.Start(t.Context()))
				time.Sleep(100 * time.Millisecond)
				_, err = os.Stat(marker)
				require.True(t, os.IsNotExist(err))
				require.Empty(t, manager.Status())
				require.Empty(t, manager.Tools())
				return
			}
			require.NoError(t, manager.Start(t.Context()))
			require.Eventually(t, func() bool { status := manager.Status(); return len(status) == 1 && status[0].Connected }, 3*time.Second, 10*time.Millisecond)
			_, err = os.Stat(marker)
			require.NoError(t, err)
		})
	}
}

func TestMCPIntegerPrecisionThroughLegacyAndBoundPathCalls(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			workspace := t.TempDir()
			manager, effects := policyFixture(t, func(server *config.MCPServer) {
				if bound {
					server.PathWorkspace = workspace
					server.AllowedPathPrefixes = []string{workspace}
					server.PathArgumentBindings = map[string][]string{"read": {"/path"}}
				}
			})
			result, err := manager.Call(t.Context(), "fixture", "read", json.RawMessage(`{"path":"new-file","big":9007199254740993,"negative":-9007199254740993,"exponent":1.234567890123456789e+100}`))
			require.NoError(t, err)
			require.Contains(t, result.Text, `9007199254740993`)
			require.NotContains(t, result.Text, `9007199254740992`)
			require.Contains(t, result.Text, `1.234567890123456789e+100`)
			remote, err := os.ReadFile(effects)
			require.NoError(t, err)
			require.Contains(t, string(remote), `"big":9007199254740993`)
			require.Contains(t, string(remote), `"negative":-9007199254740993`)
		})
	}
}
func TestMCPExpandedPathArrayDeniedWithinCumulativeBudgetBeforeRemoteEffect(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), strings.Repeat("w", 180))
	require.NoError(t, os.Mkdir(workspace, 0700))
	manager, effects := policyFixture(t, func(server *config.MCPServer) {
		server.PathWorkspace = workspace
		server.AllowedPathPrefixes = []string{workspace}
		server.PathArgumentBindings = map[string][]string{"read": {"/paths"}}
		server.MaxBytesPerCall = 2048
	})
	paths := make([]string, 40)
	for index := range paths {
		paths[index] = "x"
	}
	arguments, err := json.Marshal(map[string]any{"paths": paths})
	require.NoError(t, err)
	require.Less(t, len(arguments), 2048)
	_, err = manager.Call(t.Context(), "fixture", "read", arguments)
	require.ErrorContains(t, err, "transformed arguments exceed byte limit")
	_, err = os.Stat(effects)
	require.True(t, os.IsNotExist(err), "expanded call reached the remote process")
	// A smaller array still works under the same limit, proving the restriction
	// does not disable ordinary permitted paths.
	_, err = manager.Call(t.Context(), "fixture", "read", json.RawMessage(`{"paths":["x"]}`))
	require.NoError(t, err)
}
