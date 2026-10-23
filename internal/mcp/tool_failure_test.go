package mcp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A real child process speaks the same stdio JSON-RPC transport as plugins.
// The fixture runs only when explicitly selected by the parent test process.
func TestMCPToolFailureHelper(t *testing.T) {
	if os.Getenv("OVERKILL_MCP_FAILURE_FIXTURE") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var request jsonrpcMessage
		if err := decoder.Decode(&request); err != nil {
			os.Exit(0)
		}
		if len(request.ID) == 0 {
			continue
		}
		response := jsonrpcMessage{JSONRPC: "2.0", ID: request.ID}
		switch request.Method {
		case "initialize":
			response.Result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"fixture","version":"1"}}`)
		case "tools/list":
			definitions := make([]Tool, 0)
			for _, name := range []string{"fixture", "is_error", "is_error_empty", "protocol_error", "malformed_result", "disconnect", "success", "hang"} {
				definitions = append(definitions, Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)})
			}
			response.Result, _ = json.Marshal(map[string]any{"tools": definitions})
		case "tools/call":
			var params struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				os.Exit(2)
			}
			switch params.Name {
			case "success":
				response.Result = json.RawMessage(`{"content":[{"type":"text","text":"worked"}],"isError":false}`)
			case "is_error":
				response.Result = json.RawMessage(`{"content":[{"type":"text","text":"upstream operation failed"}],"isError":true}`)
			case "is_error_empty":
				response.Result = json.RawMessage(`{"content":[],"isError":true}`)
			case "protocol_error":
				response.Error = &jsonrpcError{Code: -32602, Message: "remote arguments rejected"}
			case "malformed_result":
				response.Result = json.RawMessage(`"not a tool result object"`)
			case "disconnect":
				os.Exit(0)
			case "hang":
				continue
			default:
				response.Error = &jsonrpcError{Code: -32601, Message: "unknown fixture tool"}
			}
		default:
			response.Error = &jsonrpcError{Code: -32601, Message: "unknown fixture method"}
		}
		if err := encoder.Encode(response); err != nil {
			os.Exit(3)
		}
	}
}

func liveToolFailureManager(t *testing.T) *Manager {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	client := NewClient("live-fixture", executable, []string{"-test.run=^TestMCPToolFailureHelper$"}, map[string]string{"OVERKILL_MCP_FAILURE_FIXTURE": "1"})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, client.Start(ctx))
	manager := NewManager(emptyMCPConfig())
	manager.installClient("live-fixture", client)
	t.Cleanup(manager.Stop)
	return manager
}

func TestToolAdapterLiveRemoteAndProtocolFailures(t *testing.T) {
	for _, tt := range []struct{ name, expected string }{
		{"is_error", "upstream operation failed"},
		{"is_error_empty", "without text"},
		{"protocol_error", "remote arguments rejected"},
		{"malformed_result", "decode call result"},
		{"disconnect", "connection closed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := liveToolFailureManager(t)
			adapter := NewToolAdapter(manager, "live-fixture", tt.name)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			output, err := adapter.Execute(ctx, json.RawMessage(`{}`))
			require.ErrorContains(t, err, tt.expected)
			require.Nil(t, output, "failed execution must not look like successful JSON output")
			if tt.name == "is_error" || tt.name == "is_error_empty" {
				var remote *ToolExecutionError
				require.ErrorAs(t, err, &remote)
				require.Equal(t, "live-fixture", remote.Server)
				require.Equal(t, tt.name, remote.Tool)
				require.True(t, remote.Result.IsError)
			}
			if tt.name == "protocol_error" {
				var protocol *jsonrpcError
				require.ErrorAs(t, err, &protocol)
				require.Equal(t, -32602, protocol.Code)
			}
		})
	}
}

func TestClientAndManagerReturnRemoteToolErrors(t *testing.T) {
	manager := liveToolFailureManager(t)
	result, err := manager.Call(t.Context(), "live-fixture", "is_error", json.RawMessage(`{}`))
	require.Error(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "upstream operation failed", result.Text)
	var failure *ToolExecutionError
	require.ErrorAs(t, err, &failure)
}

func TestToolAdapterLiveSuccessAndCancellation(t *testing.T) {
	manager := liveToolFailureManager(t)
	adapter := NewToolAdapter(manager, "live-fixture", "success")
	output, err := adapter.Execute(t.Context(), json.RawMessage(`{}`))
	require.NoError(t, err)
	var result struct {
		Text    string `json:"text"`
		IsError bool   `json:"isError"`
	}
	require.NoError(t, json.Unmarshal(output, &result))
	require.Equal(t, "worked", result.Text)
	require.False(t, result.IsError)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	output, err = NewToolAdapter(manager, "live-fixture", "hang").Execute(ctx, json.RawMessage(`{}`))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, output)
}
