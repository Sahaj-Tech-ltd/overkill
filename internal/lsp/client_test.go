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

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// ---------------------------------------------------------------------------
// Fake LSP server helpers
// ---------------------------------------------------------------------------

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
			send(out, jsonrpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(map[string]any{
				"capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fake", "version": "0"},
			})})
		case "textDocument/hover":
			send(out, jsonrpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(map[string]any{"contents": "hover text"})})
		case "textDocument/definition":
			send(out, jsonrpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(Location{
				URI: "file:///tmp/x.go", Range: Range{Start: Position{10, 0}, End: Position{10, 5}},
			})})
		case "textDocument/references":
			send(out, jsonrpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON([]Location{
				{URI: "file:///tmp/x.go", Range: Range{Start: Position{5, 2}, End: Position{5, 8}}},
				{URI: "file:///tmp/y.go", Range: Range{Start: Position{12, 4}, End: Position{12, 10}}},
			})})
		case "textDocument/documentSymbol":
			send(out, jsonrpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON([]SymbolInformation{
				{Name: "main", Kind: 12, Location: Location{URI: "file:///tmp/x.go", Range: Range{Start: Position{1, 0}, End: Position{1, 4}}}},
			})})
		case "workspace/symbol":
			send(out, jsonrpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON([]SymbolInformation{
				{Name: "Foo", Kind: 5, Location: Location{URI: "file:///tmp/a.go", Range: Range{Start: Position{3, 0}, End: Position{3, 3}}}},
			})})
		default:
			if len(msg.ID) > 0 {
				send(out, jsonrpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(nil)})
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

func newConnectedFakeClient(t *testing.T, ctx context.Context) (*Client, func()) {
	t.Helper()
	s2cR, s2cW := io.Pipe()
	c2sR, c2sW := io.Pipe()
	go runFakeLSP(t, c2sR, s2cW)
	c := &Client{language: "fake", rootURI: "file:///tmp"}
	c.conn = newJSONRPCConn(c2sW, s2cR)
	go func() { _ = c.conn.readLoop() }()
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	return c, func() { c.conn.close(); _ = c2sW.Close() }
}

// readFramedMessage consumes one Content-Length framed message.
func readFramedMessage(r *bufio.Reader) {
	var n int
	for {
		line, _ := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			n, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
		}
	}
	if n > 0 {
		body := make([]byte, n)
		_, _ = io.ReadFull(r, body)
	}
}

// nullResponder returns a goroutine that reads one request and replies with null.
func nullResponder(in *io.PipeReader, out *io.PipeWriter) {
	r := bufio.NewReader(in)
	readFramedMessage(r)
	send(out, jsonrpcMessage{JSONRPC: "2.0", ID: json.RawMessage("1"), Result: json.RawMessage("null")})
	_ = out.Close()
}

// ---------------------------------------------------------------------------
// flattenHover / decodeLocations / pathToURI / URIToPath
// ---------------------------------------------------------------------------

func TestFlattenHover(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"string", "hello", "hello"},
		{"map_value", map[string]any{"value": "world"}, "world"},
		{"map_other", map[string]any{"other": "nope"}, ""},
		{"array", []any{"a", map[string]any{"value": "b"}, "c"}, "a\nb\nc"},
		{"nil", nil, ""},
		{"int", 42, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flattenHover(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeLocations(t *testing.T) {
	loc := Location{URI: "file:///a.go", Range: Range{Start: Position{1, 2}, End: Position{3, 4}}}
	null := func() []Location { return nil }
	single := func() []Location { return []Location{loc} }
	multi := func() []Location { return []Location{loc, {URI: "file:///b.go", Range: Range{Start: Position{5, 6}, End: Position{7, 8}}}} }

	tests := []struct {
		name    string
		raw     json.RawMessage
		want    []Location
		wantErr bool
	}{
		{"null", json.RawMessage("null"), null(), false},
		{"empty", json.RawMessage(""), null(), false},
		{"single", mustJSON(loc), single(), false},
		{"array", mustJSON([]Location{loc, {URI: "file:///b.go", Range: Range{Start: Position{5, 6}, End: Position{7, 8}}}}), multi(), false},
		{"unrecognized", json.RawMessage(`{"foo":"bar"}`), nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeLocations(tt.raw)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "unrecognized") {
					t.Fatalf("expected unrecognized error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len=%d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i].URI != tt.want[i].URI {
					t.Fatalf("[%d] URI=%q, want %q", i, got[i].URI, tt.want[i].URI)
				}
			}
		})
	}
}

func TestPathToURI(t *testing.T) {
	if got := pathToURI(""); got != "" {
		t.Errorf("empty: got %q", got)
	}
	if got := pathToURI("file:///x.go"); got != "file:///x.go" {
		t.Errorf("already file: got %q", got)
	}
	got := pathToURI("/tmp/x.go")
	if !strings.HasPrefix(got, "file://") || !strings.HasSuffix(got, "/tmp/x.go") {
		t.Errorf("abs: got %q", got)
	}
}

func TestURIToPath(t *testing.T) {
	if got := URIToPath("http://x"); got != "http://x" {
		t.Errorf("non-file: %q", got)
	}
	if got := URIToPath("file:///x.go"); got != "/x.go" {
		t.Errorf("file: %q", got)
	}
	if got := URIToPath("file://%zz"); got != "file://%zz" {
		t.Errorf("bad: %q", got)
	}
	// Round trip
	uri := pathToURI("/tmp/x.go")
	if got := URIToPath(uri); got != "/tmp/x.go" {
		t.Errorf("roundtrip: %q", got)
	}
}

func TestJSONRPCError(t *testing.T) {
	e := &jsonrpcError{Code: -32601, Message: "Method not found"}
	if got := e.Error(); got != "lsp error -32601: Method not found" {
		t.Fatalf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// jsonrpcConn edge cases
// ---------------------------------------------------------------------------

func TestJSONRPCConn_ClosedOperations(t *testing.T) {
	t.Run("CallOnClosed", func(t *testing.T) {
		c := newJSONRPCConn(io.Discard, strings.NewReader(""))
		c.close()
		_, err := c.Call(context.Background(), "test", nil)
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("NotifyOnClosed", func(t *testing.T) {
		c := newJSONRPCConn(io.Discard, strings.NewReader(""))
		c.close()
		if err := c.Notify("test", nil); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("contextCancel", func(t *testing.T) {
		c := newJSONRPCConn(io.Discard, strings.NewReader(""))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := c.Call(ctx, "test", nil)
		if err != context.Canceled {
			t.Fatalf("got %v, want Canceled", err)
		}
	})
	t.Run("badParams", func(t *testing.T) {
		c := newJSONRPCConn(io.Discard, strings.NewReader(""))
		ch := make(chan int)
		if err := c.Notify("test", ch); err == nil {
			t.Fatal("expected marshal error")
		}
		_, err := c.Call(context.Background(), "test", ch)
		if err == nil {
			t.Fatal("expected marshal error")
		}
	})
}

func TestJSONRPCConn_DispatchNonNumericID(t *testing.T) {
	s2cR, s2cW := io.Pipe()
	msg := jsonrpcMessage{JSONRPC: "2.0", ID: json.RawMessage(`"str"`), Result: json.RawMessage(`{"ok":true}`)}
	go func() { send(s2cW, msg); _ = s2cW.Close() }()
	c := newJSONRPCConn(io.Discard, s2cR)
	done := make(chan struct{})
	go func() { _ = c.readLoop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestJSONRPCConn_FailAllPending(t *testing.T) {
	c := newJSONRPCConn(io.Discard, strings.NewReader(""))
	ch := make(chan *jsonrpcMessage, 1)
	c.pending.Store(int64(1), ch)
	c.failAllPending(fmt.Errorf("boom"))
	msg := <-ch
	if msg.Error == nil || msg.Error.Code != -32000 {
		t.Fatalf("expected error -32000, got %+v", msg.Error)
	}
	if _, ok := c.pending.Load(int64(1)); ok {
		t.Fatal("pending should be deleted")
	}
}

// ---------------------------------------------------------------------------
// readLoop protocol edge cases
// ---------------------------------------------------------------------------

func TestReadLoop_ContentLengthZero(t *testing.T) {
	r, w := io.Pipe()
	c := newJSONRPCConn(io.Discard, r)
	go func() { _, _ = w.Write([]byte("Content-Length: 0\r\n\r\nContent-Length: 3\r\n\r\nnull")); _ = w.Close() }()
	done := make(chan error, 1)
	go func() { done <- c.readLoop() }()
	select {
	case err := <-done:
		if err != nil && err != io.EOF && err != io.ErrClosedPipe {
			t.Logf("readLoop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	_ = r.Close()
	_ = w.Close()
}

func TestReadLoop_Oversized(t *testing.T) {
	r, w := io.Pipe()
	c := newJSONRPCConn(io.Discard, r)
	go func() { _, _ = w.Write([]byte("Content-Length: 999999999\r\n\r\n")); time.Sleep(100 * time.Millisecond); _ = w.Close() }()
	done := make(chan error, 1)
	go func() { done <- c.readLoop() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error")
		}
		if !c.closed.Load() {
			t.Fatal("should be closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	_ = r.Close()
	_ = w.Close()
}

func TestReadLoop_EdgeCases(t *testing.T) {
	t.Run("malformedJSON", func(t *testing.T) {
		r, w := io.Pipe()
		c := newJSONRPCConn(io.Discard, r)
		go func() { _, _ = w.Write([]byte("Content-Length: 4\r\n\r\nnullContent-Length: 5\r\n\r\nXXXXX")); _ = w.Close() }()
		done := make(chan error, 1)
		go func() { done <- c.readLoop() }()
		select {
		case err := <-done:
			if err != nil && err != io.EOF && err != io.ErrClosedPipe {
				t.Logf("readLoop: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout")
		}
		_ = r.Close()
		_ = w.Close()
	})
	t.Run("nonNumericCL", func(t *testing.T) {
		r, w := io.Pipe()
		c := newJSONRPCConn(io.Discard, r)
		go func() { _, _ = w.Write([]byte("Content-Length: abc\r\n\r\nContent-Length: 4\r\n\r\nnull")); _ = w.Close() }()
		done := make(chan error, 1)
		go func() { done <- c.readLoop() }()
		select {
		case err := <-done:
			if err != nil && err != io.EOF && err != io.ErrClosedPipe {
				t.Logf("readLoop: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout")
		}
		_ = r.Close()
		_ = w.Close()
	})
	t.Run("negativeCL", func(t *testing.T) {
		r, w := io.Pipe()
		c := newJSONRPCConn(io.Discard, r)
		go func() { _, _ = w.Write([]byte("Content-Length: -5\r\n\r\nContent-Length: 4\r\n\r\nnull")); _ = w.Close() }()
		done := make(chan error, 1)
		go func() { done <- c.readLoop() }()
		select {
		case err := <-done:
			if err != nil && err != io.EOF && err != io.ErrClosedPipe {
				t.Logf("readLoop: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout")
		}
		_ = r.Close()
		_ = w.Close()
	})
	t.Run("readError", func(t *testing.T) {
		r, w := io.Pipe()
		c := newJSONRPCConn(io.Discard, r)
		_ = r.Close()
		_ = w.Close()
		if err := c.readLoop(); err == nil {
			t.Fatal("expected error")
		}
		if !c.closed.Load() {
			t.Fatal("should be closed")
		}
	})
}

// ---------------------------------------------------------------------------
// Client tests
// ---------------------------------------------------------------------------

func TestNewClient(t *testing.T) {
	c := NewClient("go", "gopls", []string{"serve"})
	if c.Language() != "go" {
		t.Fatalf("got %q", c.Language())
	}
	if c.cmd == nil {
		t.Fatal("cmd nil")
	}
}

func TestClient_State(t *testing.T) {
	c := &Client{}
	if c.Connected() {
		t.Fatal("unexpected connected")
	}
	if err := c.LastError(); err != nil {
		t.Fatal("unexpected error")
	}
	c.setError(fmt.Errorf("boom"))
	if !strings.Contains(c.LastError().Error(), "boom") {
		t.Fatalf("got %v", c.LastError())
	}
	if c.Connected() {
		t.Fatal("should be disconnected")
	}
	// Close nil paths should not panic
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLSPInitializeAndHover(t *testing.T) {
	s2cR, s2cW := io.Pipe()
	c2sR, c2sW := io.Pipe()
	go runFakeLSP(t, c2sR, s2cW)
	c := &Client{language: "fake"}
	c.conn = newJSONRPCConn(c2sW, s2cR)
	go func() { _ = c.conn.readLoop() }()
	defer func() { c.conn.close(); _ = c2sW.Close() }()
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
		t.Fatalf("got %q", hov.Contents)
	}
}

func TestClient_LSPMethods(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cl, cleanup := newConnectedFakeClient(t, ctx)
	defer cleanup()

	t.Run("Definition", func(t *testing.T) {
		locs, err := cl.Definition(ctx, "/tmp/x.go", 10, 0)
		if err != nil || len(locs) != 1 || locs[0].URI != "file:///tmp/x.go" {
			t.Fatalf("got %v, err=%v", locs, err)
		}
	})
	t.Run("References", func(t *testing.T) {
		locs, err := cl.References(ctx, "/tmp/x.go", 5, 2)
		if err != nil || len(locs) != 2 {
			t.Fatalf("got %v, err=%v", locs, err)
		}
	})
	t.Run("DocumentSymbols", func(t *testing.T) {
		syms, err := cl.DocumentSymbols(ctx, "/tmp/x.go")
		if err != nil || len(syms) != 1 || syms[0].Name != "main" {
			t.Fatalf("got %v, err=%v", syms, err)
		}
	})
	t.Run("WorkspaceSymbols", func(t *testing.T) {
		syms, err := cl.WorkspaceSymbols(ctx, "Foo")
		if err != nil || len(syms) != 1 || syms[0].Name != "Foo" {
			t.Fatalf("got %v, err=%v", syms, err)
		}
	})
}

func TestClient_NullResults(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	t.Run("DocumentSymbols_null", func(t *testing.T) {
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		go nullResponder(inR, outW)
		cl := &Client{language: "fake", rootURI: "file:///tmp"}
		cl.conn = newJSONRPCConn(inW, outR)
		go func() { _ = cl.conn.readLoop() }()
		defer cl.conn.close()
		syms, err := cl.DocumentSymbols(ctx, "/tmp/x.go")
		if err != nil || syms != nil {
			t.Fatalf("got %v, err=%v", syms, err)
		}
	})
	t.Run("Hover_null", func(t *testing.T) {
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		go nullResponder(inR, outW)
		cl := &Client{language: "fake", rootURI: "file:///tmp"}
		cl.conn = newJSONRPCConn(inW, outR)
		go func() { _ = cl.conn.readLoop() }()
		defer cl.conn.close()
		hov, err := cl.Hover(ctx, "/tmp/x.go", 0, 0)
		if err != nil || hov.Contents != "" {
			t.Fatalf("got %q, err=%v", hov.Contents, err)
		}
	})
}

// ---------------------------------------------------------------------------
// Manager tests
// ---------------------------------------------------------------------------

func TestManager_NilSafety(t *testing.T) {
	var m *Manager
	m.Start(context.Background())
	m.Stop()
	if m.ClientForFile("x.go") != nil {
		t.Fatal("expected nil")
	}
	if m.ConnectedCount() != 0 {
		t.Fatal("expected 0")
	}
	if m.Languages() != nil {
		t.Fatal("expected nil")
	}
}

func TestManager(t *testing.T) {
	m := NewManager(config.LSPConfig{}, "/tmp")
	if m.rootDir != "/tmp" || len(m.clients) != 0 || len(m.byExt) != 0 {
		t.Fatal("unexpected state")
	}
	// Empty rootDir
	m2 := NewManager(config.LSPConfig{}, "")
	if m2.rootDir == "" {
		t.Fatal("expected non-empty rootDir")
	}
}

func TestManager_ClientForFile(t *testing.T) {
	connected := &Client{language: "go"}
	connected.mu.Lock()
	connected.connected = true
	connected.mu.Unlock()
	m := &Manager{
		rootDir: "/tmp",
		byExt:   map[string]string{".go": "go"},
		clients: map[string]*Client{"go": nil},
	}
	if m.ClientForFile("main.go") != nil {
		t.Fatal("nil client should return nil")
	}
	m.clients["go"] = &Client{language: "go"} // not connected
	if m.ClientForFile("main.go") != nil {
		t.Fatal("disconnected client should return nil")
	}
	m.clients["go"] = connected
	if m.ClientForFile("main.go") == nil {
		t.Fatal("connected client should not be nil")
	}
	if m.ClientForFile("main.py") != nil || m.ClientForFile("Makefile") != nil {
		t.Fatal("unknown extensions should return nil")
	}
}

func TestManager_Stats(t *testing.T) {
	c1, c2 := &Client{language: "go"}, &Client{language: "py"}
	c1.mu.Lock()
	c1.connected = true
	c1.mu.Unlock()
	m := &Manager{rootDir: "/tmp", clients: map[string]*Client{"go": c1, "py": c2}}
	if m.ConnectedCount() != 1 {
		t.Fatalf("got %d", m.ConnectedCount())
	}
	if langs := m.Languages(); len(langs) != 1 || langs[0] != "go" {
		t.Fatalf("got %v", langs)
	}
}

func TestManager_Start(t *testing.T) {
	cfg := config.LSPConfig{Servers: []config.LSPServer{
		{Language: "", Command: "gopls", Filetypes: []string{".go"}},
		{Language: "go", Command: "", Filetypes: []string{".go"}},
		{Language: "python", Command: "nonexistent-lsp-999", Filetypes: []string{".py"}},
	}}
	m := NewManager(cfg, "/tmp")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.Start(ctx)
	if m.byExt[".py"] != "python" {
		t.Fatalf("byExt: got %q", m.byExt[".py"])
	}
	if _, ok := m.clients[""]; ok {
		t.Fatal("empty language should be skipped")
	}
	if _, ok := m.clients["go"]; ok {
		t.Fatal("empty command should be skipped")
	}
	py := m.clients["python"]
	if py == nil || py.Connected() || py.LastError() == nil {
		t.Fatal("python client should have failed")
	}
}

func TestManager_Stop(t *testing.T) {
	c := &Client{language: "go"}
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	m := &Manager{rootDir: "/tmp", clients: map[string]*Client{"go": c}}
	m.Stop()
	if c.Connected() {
		t.Fatal("should be disconnected")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	for _, s := range cfg.Servers {
		if s.Command == "" {
			t.Fatal("empty command")
		}
	}
}
