package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runFakeLSP reads framed messages from in and writes canned responses to out.
func runFakeLSP(t *testing.T, in io.Reader, out io.Writer) {
	t.Helper()
	r := bufio.NewReader(in)
	for {
		var contentLength int
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				v := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
				contentLength, _ = strconv.Atoi(v)
			}
		}
		if contentLength <= 0 {
			continue
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var msg jsonrpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			send(out, jsonrpcMessage{
				JSONRPC: "2.0", ID: msg.ID,
				Result: mustJSON(map[string]any{
					"capabilities": map[string]any{},
					"serverInfo":   map[string]any{"name": "fake", "version": "0"},
				}),
			})
		case "textDocument/hover":
			send(out, jsonrpcMessage{
				JSONRPC: "2.0", ID: msg.ID,
				Result: mustJSON(map[string]any{
					"contents": "hover text",
				}),
			})
		default:
			if len(msg.ID) > 0 {
				send(out, jsonrpcMessage{
					JSONRPC: "2.0", ID: msg.ID,
					Result: mustJSON(nil),
				})
			}
		}
	}
}

func send(out io.Writer, msg jsonrpcMessage) {
	body, _ := json.Marshal(msg)
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	_, _ = out.Write([]byte(header))
	_, _ = out.Write(body)
}

func mustJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	b, _ := json.Marshal(v)
	return b
}

func TestLSPInitializeAndHover(t *testing.T) {
	s2cR, s2cW := io.Pipe()
	c2sR, c2sW := io.Pipe()

	go runFakeLSP(t, c2sR, s2cW)

	c := &Client{language: "fake"}
	c.conn = newJSONRPCConn(c2sW, s2cR)
	go func() { _ = c.conn.readLoop() }()
	defer func() {
		c.conn.close()
		_ = c2sW.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := c.conn.Call(ctx, "initialize", map[string]any{"rootUri": "file:///tmp"}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := c.conn.Notify("initialized", map[string]any{}); err != nil {
		t.Fatalf("initialized: %v", err)
	}

	hov, err := c.Hover(ctx, "/tmp/x.go", 0, 0)
	if err != nil {
		t.Fatalf("hover: %v", err)
	}
	if hov.Contents != "hover text" {
		t.Fatalf("expected 'hover text', got %q", hov.Contents)
	}
}

func TestPathToURIRoundTrip(t *testing.T) {
	uri := pathToURI("/tmp/x.go")
	if !strings.HasPrefix(uri, "file://") {
		t.Fatalf("expected file:// uri, got %q", uri)
	}
	if got := URIToPath(uri); got != "/tmp/x.go" {
		t.Fatalf("expected /tmp/x.go, got %q", got)
	}
}
