package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeServer drives a Client by piping into its conn directly. Avoids the
// need to spawn a real subprocess in unit tests.
type fakePipe struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func newFakeStdio() (clientReader io.Reader, clientWriter io.Writer, serverReader io.Reader, serverWriter io.Writer) {
	// Two pipes: server->client and client->server.
	s2cR, s2cW := io.Pipe()
	c2sR, c2sW := io.Pipe()
	return s2cR, c2sW, c2sR, s2cW
}

// newTestClient bypasses subprocess plumbing for unit tests.
func newTestClient(name string, w io.Writer, r io.Reader) *Client {
	c := &Client{name: name}
	c.conn = newJSONRPCConn(w, r)
	go func() { _ = c.conn.readLoop() }()
	return c
}

// runFakeServer reads JSON-RPC requests and emits canned responses. Runs
// until the input pipe is closed.
func runFakeServer(t *testing.T, in io.Reader, out io.Writer) {
	t.Helper()
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		var msg jsonrpcMessage
		if err := dec.Decode(&msg); err != nil {
			return
		}
		switch msg.Method {
		case "initialize":
			_ = enc.Encode(jsonrpcMessage{
				JSONRPC: "2.0", ID: msg.ID,
				Result: jsonRaw(t, map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "fake", "version": "0"},
				}),
			})
		case "tools/list":
			_ = enc.Encode(jsonrpcMessage{
				JSONRPC: "2.0", ID: msg.ID,
				Result: jsonRaw(t, map[string]any{
					"tools": []map[string]any{
						{"name": "echo", "description": "echo input"},
					},
				}),
			})
		case "tools/call":
			_ = enc.Encode(jsonrpcMessage{
				JSONRPC: "2.0", ID: msg.ID,
				Result: jsonRaw(t, map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "hello"},
					},
				}),
			})
		case "resources/list":
			_ = enc.Encode(jsonrpcMessage{
				JSONRPC: "2.0", ID: msg.ID,
				Error: &jsonrpcError{Code: -32601, Message: "not implemented"},
			})
		default:
			// notifications carry no ID — skip; otherwise reply with error.
			if len(msg.ID) > 0 {
				_ = enc.Encode(jsonrpcMessage{
					JSONRPC: "2.0", ID: msg.ID,
					Error: &jsonrpcError{Code: -32601, Message: "unknown method"},
				})
			}
		}
	}
}

func jsonRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestClientHandshakeAndToolCall(t *testing.T) {
	cR, cW, sR, sW := newFakeStdio()
	go runFakeServer(t, sR, sW)

	c := newTestClient("fake", cW, cR)
	defer func() {
		c.conn.close()
		// Closing the pipes triggers the server goroutine to exit.
		if pw, ok := cW.(*io.PipeWriter); ok {
			_ = pw.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// initialize
	if _, err := c.conn.Call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ethos", "version": "0.1"},
	}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := c.conn.Notify("notifications/initialized", map[string]any{}); err != nil {
		t.Fatalf("initialized notify: %v", err)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !strings.Contains(res.Text, "hello") {
		t.Fatalf("expected hello, got %q", res.Text)
	}
}

func TestClientCallErrorPath(t *testing.T) {
	cR, cW, sR, sW := newFakeStdio()
	go runFakeServer(t, sR, sW)

	c := newTestClient("fake", cW, cR)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := c.conn.Call(ctx, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
}

func TestManagerEmptyStartStop(t *testing.T) {
	m := NewManager(emptyMCPConfig())
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if c, f := m.Counts(); c != 0 || f != 0 {
		t.Fatalf("expected empty counts, got %d/%d", c, f)
	}
	m.Stop()
}
