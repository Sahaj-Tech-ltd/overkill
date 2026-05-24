package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// pipePair returns two halves of an in-memory bidirectional pipe so a host
// Conn and a fake plugin Conn can talk over it without a subprocess.
func pipePair() (io.ReadWriteCloser, io.ReadWriteCloser) {
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	return &rwCloser{r: ar, w: aw}, &rwCloser{r: br, w: bw}
}

type rwCloser struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (c *rwCloser) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *rwCloser) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *rwCloser) Close() error                { c.r.Close(); return c.w.Close() }

// fakePlugin runs a minimal in-process plugin against the host conn. It
// auto-registers any tools/commands/events provided and answers tool.call
// with whatever toolFn returns.
type fakePlugin struct {
	manifest Manifest
	tools    []ToolDecl
	commands []CommandDecl
	events   []string
	toolFn   func(name string, args json.RawMessage) (any, error)
	conn     *Conn
}

func (f *fakePlugin) start(ctx context.Context, w io.Writer, r io.Reader) {
	f.conn = NewConn(w, r)
	// initialize handler: auto-register everything and return manifest.
	f.conn.Handle("plugin.initialize", func(_ context.Context, _ json.RawMessage) (any, error) {
		go func() {
			callCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			for _, t := range f.tools {
				_, _ = f.conn.Call(callCtx, "host.register_tool", t)
			}
			for _, c := range f.commands {
				_, _ = f.conn.Call(callCtx, "host.register_command", c)
			}
			for _, ev := range f.events {
				_, _ = f.conn.Call(callCtx, "host.subscribe", map[string]string{"event": ev})
			}
		}()
		return map[string]any{
			"name":     f.manifest.Name,
			"version":  f.manifest.Version,
			"manifest": f.manifest,
		}, nil
	})
	f.conn.Handle("tool.call", func(_ context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		}
		_ = json.Unmarshal(p, &in)
		if f.toolFn == nil {
			return map[string]string{"echo": in.Name}, nil
		}
		return f.toolFn(in.Name, in.Args)
	})
	f.conn.Handle("event.fire", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, nil
	})
	f.conn.Handle("plugin.shutdown", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]bool{"ok": true}, nil
	})
	go func() { _ = f.conn.Serve(ctx) }()
}

// hostFixture wires a Client to a fakePlugin without spawning a process.
type hostFixture struct {
	bridge *recordingBridge
	client *Client
	plug   *fakePlugin
	cancel context.CancelFunc
}

type recordingBridge struct {
	mu     sync.Mutex
	toasts []string
	cfg    map[string]any
}

func (b *recordingBridge) SessionInfo() SessionInfo { return SessionInfo{ID: "s1", Title: "test"} }
func (b *recordingBridge) ConfigValue(k string) (any, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.cfg[k]
	return v, ok
}
func (b *recordingBridge) Toast(kind, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.toasts = append(b.toasts, kind+":"+text)
}

func newFixture(t *testing.T, plug *fakePlugin) *hostFixture {
	t.Helper()
	hostSide, plugSide := pipePair()
	bridge := &recordingBridge{cfg: map[string]any{}}
	c := &Client{
		name:     plug.manifest.Name,
		bridge:   bridge,
		tools:    make(map[string]ToolDecl),
		commands: make(map[string]CommandDecl),
		events:   make(map[string]struct{}),
	}
	// Pre-populate the manifest so permission checks during async registration
	// succeed even before the initialize response is decoded by the test.
	c.manifest = plug.manifest
	c.conn = NewConn(hostSide, hostSide)
	c.installHostHandlers()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = c.conn.Serve(ctx) }()
	plug.start(ctx, plugSide, plugSide)

	// Run the host's "initialize" handshake manually since we bypassed Start().
	initCtx, icancel := context.WithTimeout(ctx, 2*time.Second)
	defer icancel()
	raw, err := c.conn.Call(initCtx, "plugin.initialize", map[string]any{
		"host_version": HostVersion,
	})
	if err != nil {
		cancel()
		t.Fatalf("initialize: %v", err)
	}
	var initResp struct {
		Manifest Manifest `json:"manifest"`
	}
	_ = json.Unmarshal(raw, &initResp)
	c.mu.Lock()
	c.manifest = initResp.Manifest
	c.connected = true
	c.mu.Unlock()
	// Wait briefly for auto-registrations to land.
	deadline := time.After(time.Second)
	for {
		c.mu.RLock()
		ready := len(c.tools) >= len(plug.tools) &&
			len(c.commands) >= len(plug.commands) &&
			len(c.events) >= len(plug.events)
		c.mu.RUnlock()
		if ready {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("registrations didn't complete")
		case <-time.After(10 * time.Millisecond):
		}
	}
	return &hostFixture{bridge: bridge, client: c, plug: plug, cancel: cancel}
}

func (f *hostFixture) close() {
	f.cancel()
	if f.client != nil && f.client.conn != nil {
		f.client.conn.Close()
	}
}

func TestHandshakeAndToolRegistration(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "test", Version: "0.1.0"},
		tools:    []ToolDecl{{Name: "echo", Description: "echo input"}},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	tools := fx.client.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("expected echo tool, got %+v", tools)
	}
}

func TestToolInvocationRoundTrip(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "test", Version: "0.1.0"},
		tools:    []ToolDecl{{Name: "double"}},
		toolFn: func(name string, args json.RawMessage) (any, error) {
			var in struct {
				N int `json:"n"`
			}
			_ = json.Unmarshal(args, &in)
			return map[string]int{"out": in.N * 2}, nil
		},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := fx.client.CallTool(ctx, "double", json.RawMessage(`{"n": 5}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var out struct {
		Out int `json:"out"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Out != 10 {
		t.Fatalf("want 10, got %d", out.Out)
	}
}

func TestEventSubscriptionAndDelivery(t *testing.T) {
	received := make(chan string, 1)
	plug := &fakePlugin{
		manifest: Manifest{
			Name:    "events",
			Version: "0.1.0",
			Permissions: Permissions{
				Events: []string{EventCompact},
			},
		},
		events: []string{EventCompact},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	plug.conn.Handle("event.fire", func(_ context.Context, p json.RawMessage) (any, error) {
		received <- string(p)
		return nil, nil
	})

	if subs := fx.client.SubscribedEvents(); len(subs) != 1 || subs[0] != EventCompact {
		t.Fatalf("expected compact subscription, got %v", subs)
	}
	if err := fx.client.FireEvent(EventCompact, map[string]string{"reason": "soft"}); err != nil {
		t.Fatalf("fire: %v", err)
	}
	select {
	case got := <-received:
		if got == "" {
			t.Fatalf("empty payload")
		}
	case <-time.After(time.Second):
		t.Fatal("event not received")
	}
}

func TestPermissionDeniedForUndeclaredConfigKey(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{
			Name:        "p",
			Version:     "0.1.0",
			Permissions: Permissions{ConfigKeys: []string{"allowed.key"}},
		},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	// The fake plugin reads a config key it didn't declare.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := plug.conn.Call(ctx, "host.config_get", map[string]string{"key": "denied.key"})
	if err == nil {
		t.Fatal("expected permission error, got nil")
	}
	rerr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("want *RPCError, got %T: %v", err, err)
	}
	if rerr.Code != ErrCodePermissionDenied {
		t.Fatalf("want code %d, got %d", ErrCodePermissionDenied, rerr.Code)
	}
}

func TestPermissionDeniedForUndeclaredEvent(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "p", Version: "0.1.0"},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := plug.conn.Call(ctx, "host.subscribe", map[string]string{"event": "compact"})
	if err == nil {
		t.Fatal("expected permission error, got nil")
	}
	rerr, ok := err.(*RPCError)
	if !ok || rerr.Code != ErrCodePermissionDenied {
		t.Fatalf("want permission denied, got %v", err)
	}
}

func TestAllowedConfigKeyReturnsValue(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{
			Name:        "p",
			Version:     "0.1.0",
			Permissions: Permissions{ConfigKeys: []string{"foo"}},
		},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	fx.bridge.cfg["foo"] = "bar"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	raw, err := plug.conn.Call(ctx, "host.config_get", map[string]string{"key": "foo"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var resp struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Value != "bar" {
		t.Fatalf("want bar, got %q", resp.Value)
	}
}

func TestParseCommandID(t *testing.T) {
	cases := map[string]struct {
		in     string
		plugin string
		id     string
		ok     bool
	}{
		"happy":      {"plugin:notes:note", "notes", "note", true},
		"with colon": {"plugin:notes:sub:cmd", "notes", "sub:cmd", true},
		"missing":    {"notes:note", "", "", false},
		"no id":      {"plugin:notes", "", "", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p, id, ok := ParseCommandID(c.in)
			if p != c.plugin || id != c.id || ok != c.ok {
				t.Fatalf("got (%q,%q,%v) want (%q,%q,%v)", p, id, ok, c.plugin, c.id, c.ok)
			}
		})
	}
}

func TestAssembleSnippets(t *testing.T) {
	got := AssembleSnippets([]ContextSnippet{
		{Title: "a", Content: "alpha"},
		{Title: "b", Content: "beta\n"},
	})
	if got == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(got, "alpha") || !contains(got, "beta") || !contains(got, "## a") || !contains(got, "## b") {
		t.Fatalf("unexpected output:\n%s", got)
	}
	if AssembleSnippets(nil) != "" {
		t.Fatal("nil input should produce empty string")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestShutdownIsClean(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	fx := newFixture(t, plug)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := fx.client.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if fx.client.Connected() {
		t.Fatal("expected disconnected after shutdown")
	}
}

// ---------------------------------------------------------------------------
// Client methods not covered by the fixture-based tests above
// ---------------------------------------------------------------------------

func TestClientNameFallsBackToDiscoveryName(t *testing.T) {
	plug := &fakePlugin{
		// manifest has a name — the fixture will set it, so Name() returns it.
		manifest: Manifest{Name: "from-manifest", Version: "0.1.0"},
	}
	fx := newFixture(t, plug)
	defer fx.close()
	if got := fx.client.Name(); got != "from-manifest" {
		t.Fatalf("want from-manifest, got %q", got)
	}
}

func TestClientNameUsesConfiguredNameWhenManifestEmpty(t *testing.T) {
	// Bypass fixture to test fallback path.
	c := &Client{
		name:     "fallback",
		tools:    make(map[string]ToolDecl),
		commands: make(map[string]CommandDecl),
		events:   make(map[string]struct{}),
	}
	if got := c.Name(); got != "fallback" {
		t.Fatalf("want fallback, got %q", got)
	}
}

func TestClientSetStaticManifest(t *testing.T) {
	c := &Client{}
	m := Manifest{Name: "static", Version: "1.0"}
	c.SetStaticManifest(m)
	if c.staticManifest == nil || c.staticManifest.Name != "static" {
		t.Fatal("staticManifest not set")
	}
}

func TestClientManifest(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "m", Version: "0.1.0"}}
	fx := newFixture(t, plug)
	defer fx.close()
	m := fx.client.Manifest()
	if m.Name != "m" {
		t.Fatalf("manifest mismatch: %+v", m)
	}
}

func TestClientCommands(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "cmds", Version: "0.1.0"},
		commands: []CommandDecl{{ID: "run", Title: "Run it"}},
	}
	fx := newFixture(t, plug)
	defer fx.close()
	cmds := fx.client.Commands()
	if len(cmds) != 1 || cmds[0].ID != "run" {
		t.Fatalf("expected run command, got %+v", cmds)
	}
}

func TestClientHasContextProvider(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	fx := newFixture(t, plug)
	defer fx.close()
	if fx.client.HasContextProvider() {
		t.Fatal("should not have context provider")
	}
}

func TestClientLastErrorAndSetError(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	fx := newFixture(t, plug)
	defer fx.close()
	if err := fx.client.LastError(); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	fx.client.setError(fmt.Errorf("boom"))
	if err := fx.client.LastError(); err == nil || err.Error() != "boom" {
		t.Fatalf("expected boom, got %v", err)
	}
	if fx.client.Connected() {
		t.Fatal("setError should mark disconnected")
	}
}

func TestClientIsShuttingDown(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	fx := newFixture(t, plug)
	defer fx.close()
	if fx.client.IsShuttingDown() {
		t.Fatal("should not be shutting down yet")
	}
}

func TestClientCallToolNotStarted(t *testing.T) {
	c := &Client{name: "x"}
	_, err := c.CallTool(context.Background(), "t", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClientInvokeCommandNotStarted(t *testing.T) {
	c := &Client{name: "x"}
	err := c.InvokeCommand(context.Background(), "c", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClientFireEventNotStarted(t *testing.T) {
	c := &Client{name: "x"}
	err := c.FireEvent("e", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClientWaitNilCmd(t *testing.T) {
	c := &Client{}
	if err := c.Wait(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// JSON-RPC layer (jsonrpc.go)
// ---------------------------------------------------------------------------

func TestRPCErrorNil(t *testing.T) {
	var e *RPCError
	if s := e.Error(); s != "" {
		t.Fatalf("expected empty, got %q", s)
	}
}

func TestRPCErrorString(t *testing.T) {
	e := &RPCError{Code: -32001, Message: "denied"}
	if e.Error() != "rpc error -32001: denied" {
		t.Fatalf("unexpected: %q", e.Error())
	}
}

func TestConnCallOnClosed(t *testing.T) {
	host, plug := pipePair()
	conn := NewConn(host, host)
	_ = plug
	conn.Close()
	_, err := conn.Call(context.Background(), "m", nil)
	if err == nil {
		t.Fatal("expected error on closed conn")
	}
}

func TestConnNotifyOnClosed(t *testing.T) {
	host, plug := pipePair()
	conn := NewConn(host, host)
	_ = plug
	conn.Close()
	if err := conn.Notify("m", nil); err == nil {
		t.Fatal("expected error on closed conn")
	}
}

func TestConnServeParseError(t *testing.T) {
	hostSide, plugSide := pipePair()
	conn := NewConn(hostSide, hostSide)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = conn.Serve(ctx); close(done) }()
	// Write invalid JSON — Serve should not crash.
	plugSide.Write([]byte("not-json\n"))
	time.Sleep(50 * time.Millisecond)
	plugSide.Close()
	<-done
}

func TestConnServeMethodNotFound(t *testing.T) {
	hostSide, plugSide := pipePair()
	conn := NewConn(hostSide, hostSide)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = conn.Serve(ctx) }()
	// Send a valid request for an unregistered method with an id.
	plugSide.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"nonexistent"}` + "\n"))
	// Read the error response from plugSide (conn writes response to aw -> plugSide's br).
	respCh := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := plugSide.Read(buf)
		respCh <- string(buf[:n])
	}()
	select {
	case got := <-respCh:
		if !contains(got, "method not found") {
			t.Fatalf("expected method-not-found, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for method-not-found response")
	}
	plugSide.Close()
}

func TestConnCallContextCancel(t *testing.T) {
	hostSide, plugSide := pipePair()
	conn := NewConn(hostSide, hostSide)
	// Drain plugSide so writes don't block.
	go func() { io.Copy(io.Discard, plugSide) }()
	// Start serve in background.
	serveCtx, serveCancel := context.WithCancel(context.Background())
	go func() { _ = conn.Serve(serveCtx) }()
	defer serveCancel()
	// Use a pre-cancelled context for the Call.
	callCtx, callCancel := context.WithCancel(context.Background())
	callCancel()
	_, err := conn.Call(callCtx, "plugin.initialize", map[string]any{})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	plugSide.Close()
}

func TestConnFailAllPendingViaClose(t *testing.T) {
	hostSide, plugSide := pipePair()
	conn := NewConn(hostSide, hostSide)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Drain plugSide to prevent write blocks.
	go func() { io.Copy(io.Discard, plugSide) }()
	go func() { _ = conn.Serve(ctx) }()
	// Issue a call and then close the plug side to trigger serve error.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn.Call(ctx, "slow", nil)
	}()
	time.Sleep(20 * time.Millisecond)
	plugSide.Close()
	wg.Wait()
}

func TestConnDispatchResponseWithBadID(t *testing.T) {
	hostSide, plugSide := pipePair()
	conn := NewConn(hostSide, hostSide)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Drain plugSide.
	go func() { io.Copy(io.Discard, plugSide) }()
	go func() { _ = conn.Serve(ctx) }()
	// Send a response with a non-numeric id — should be silently ignored.
	plugSide.Write([]byte(`{"jsonrpc":"2.0","id":"abc","result":"x"}` + "\n"))
	time.Sleep(50 * time.Millisecond)
	plugSide.Close()
}

func TestConnWriteMessageOnClosed(t *testing.T) {
	hostSide, plugSide := pipePair()
	conn := NewConn(hostSide, hostSide)
	conn.Close()
	_ = plugSide
	if err := conn.Notify("x", nil); err == nil {
		t.Fatal("expected closed error")
	}
}

// ---------------------------------------------------------------------------
// Permissions (permissions.go)
// ---------------------------------------------------------------------------

func TestAllowsToolWildcard(t *testing.T) {
	p := Permissions{ToolsCall: []string{"*"}}
	if !p.AllowsTool("anything") {
		t.Fatal("wildcard should allow any tool")
	}
}

func TestAllowsToolExplicit(t *testing.T) {
	p := Permissions{ToolsCall: []string{"grep", "fs"}}
	if !p.AllowsTool("grep") {
		t.Fatal("should allow grep")
	}
	if p.AllowsTool("shell") {
		t.Fatal("should not allow shell")
	}
}

func TestAllowsEventWildcard(t *testing.T) {
	p := Permissions{Events: []string{"*"}}
	if !p.AllowsEvent("any_event") {
		t.Fatal("wildcard should allow any event")
	}
}

func TestValidateManifest(t *testing.T) {
	if err := ValidateManifest(Manifest{}); err == nil {
		t.Fatal("expected error for missing name")
	}
	if err := ValidateManifest(Manifest{Name: "n"}); err == nil {
		t.Fatal("expected error for missing version")
	}
	if err := ValidateManifest(Manifest{Name: "n", Version: "1"}); err != nil {
		t.Fatalf("expected nil for valid manifest, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ToolAdapter (tool_adapter.go)
// ---------------------------------------------------------------------------

func TestToolAdapterName(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "p", Version: "0.1.0"},
		tools:    []ToolDecl{{Name: "t1"}},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	// Create a Manager to test ToolAdapter.
	m := &Manager{root: "/x", bridge: fx.bridge}
	m.mu.Lock()
	m.clients = map[string]*Client{"p": fx.client}
	m.mu.Unlock()

	ta := NewToolAdapter(m, "p", "t1")
	if ta.Name() != "plugin:p:t1" {
		t.Fatalf("unexpected name: %q", ta.Name())
	}
}

func TestToolAdapterExecute(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "p", Version: "0.1.0"},
		tools:    []ToolDecl{{Name: "double"}},
		toolFn: func(name string, args json.RawMessage) (any, error) {
			return map[string]int{"x": 42}, nil
		},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	m := &Manager{root: "/x", bridge: fx.bridge}
	m.mu.Lock()
	m.clients = map[string]*Client{"p": fx.client}
	m.mu.Unlock()

	ta := NewToolAdapter(m, "p", "double")
	ctx := context.Background()
	raw, err := ta.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !contains(string(raw), `"x"`) || !contains(string(raw), "42") {
		t.Fatalf("unexpected result: %s", raw)
	}
}

func TestToolAdapterExecuteUnknownPlugin(t *testing.T) {
	m := &Manager{root: "/x"}
	ta := NewToolAdapter(m, "ghost", "t")
	_, err := ta.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return JSON error wrapped.
}

// ---------------------------------------------------------------------------
// CommandAdapter (command_adapter.go)
// ---------------------------------------------------------------------------

func TestCommandID(t *testing.T) {
	if got := CommandID("notes", "create"); got != "plugin:notes:create" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestManagerDispatch(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "notes", Version: "0.1.0"},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	m := &Manager{root: "/x", bridge: fx.bridge}
	m.mu.Lock()
	m.clients = map[string]*Client{"notes": fx.client}
	m.mu.Unlock()

	ctx := context.Background()
	// Unknown command, should not panic.
	err := m.Dispatch(ctx, "plugin:notes:nonexistent", "hello")
	if err != nil {
		t.Logf("dispatch error (expected for unknown command): %v", err)
	}
	// Non-plugin format should return nil.
	if err := m.Dispatch(ctx, "not-a-plugin-id", ""); err != nil {
		t.Fatalf("expected nil for non-plugin command: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ContextProvider (context_provider.go)
// ---------------------------------------------------------------------------

func TestProvideAndAssembleNilManager(t *testing.T) {
	if got := ProvideAndAssemble(context.Background(), nil, "", ""); got != "" {
		t.Fatal("expected empty for nil manager")
	}
}

func TestAssembleSnippetsEmptyTitle(t *testing.T) {
	got := AssembleSnippets([]ContextSnippet{
		{Title: "", Content: "data"},
	})
	if !contains(got, "(untitled)") {
		t.Fatalf("expected (untitled), got %s", got)
	}
}

// ---------------------------------------------------------------------------
// Discovery (discovery.go)
// ---------------------------------------------------------------------------

func TestDiscoverEmptyRoot(t *testing.T) {
	got, err := Discover("")
	if err != nil || len(got) != 0 {
		t.Fatalf("expected empty, got %v, %v", got, err)
	}
}

func TestDiscoverNonExistentDir(t *testing.T) {
	got, err := Discover("/nonexistent/dir/for/plugins")
	if err != nil || len(got) != 0 {
		t.Fatalf("expected empty, got %v, %v", got, err)
	}
}

func TestDiscoverEmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := Discover(dir)
	if err != nil || len(got) != 0 {
		t.Fatalf("expected empty, got %v, %v", got, err)
	}
}

func TestDiscoverDirWithPluginToml(t *testing.T) {
	dir := t.TempDir()
	pDir := filepath.Join(dir, "myplug")
	if err := os.MkdirAll(pDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tomlContent := `name = "myplug"
version = "1.0"
entry = "run.sh"
[permissions]
config_keys = ["x"]
events = ["compact"]
`
	if err := os.WriteFile(filepath.Join(pDir, "plugin.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create the entry script (needs to exist even if not executable for discoverDir).
	if err := os.WriteFile(filepath.Join(pDir, "run.sh"), []byte("#!/bin/sh\necho ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "myplug" {
		t.Fatalf("expected myplug, got %+v", got)
	}
	if got[0].StaticManifest == nil || got[0].StaticManifest.Name != "myplug" {
		t.Fatalf("expected static manifest, got %+v", got[0].StaticManifest)
	}
	if len(got[0].StaticManifest.Permissions.ConfigKeys) != 1 {
		t.Fatalf("expected config_keys, got %+v", got[0].StaticManifest.Permissions)
	}
}

func TestDiscoverDirWithPluginTomlMissingEntry(t *testing.T) {
	dir := t.TempDir()
	pDir := filepath.Join(dir, "badplug")
	if err := os.MkdirAll(pDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tomlContent := `name = "badplug"
version = "1.0"
` // no entry
	if err := os.WriteFile(filepath.Join(pDir, "plugin.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 plugins for toml with missing entry, got %d", len(got))
	}
}

func TestDiscoverDirInvalidToml(t *testing.T) {
	dir := t.TempDir()
	pDir := filepath.Join(dir, "bad")
	if err := os.MkdirAll(pDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pDir, "plugin.toml"), []byte("[[["), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 for invalid toml")
	}
}

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()
	regFile := filepath.Join(dir, "reg")
	if err := os.WriteFile(regFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isExecutable(regFile) {
		t.Fatal("regular file should not be executable")
	}
	if isExecutable(dir) {
		t.Fatal("directory should not be executable")
	}
	if isExecutable("/nonexistent/path") {
		t.Fatal("nonexistent path should not be executable")
	}
	exeFile := filepath.Join(dir, "exe")
	if err := os.WriteFile(exeFile, []byte("#!/bin/sh\necho ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isExecutable(exeFile) {
		t.Fatal("executable file should be detected")
	}
}

// ---------------------------------------------------------------------------
// Manager (manager.go) — nil-safe methods + basic routing
// ---------------------------------------------------------------------------

func TestNewManager(t *testing.T) {
	m := NewManager("/root", nil, []string{"disabled-a"})
	if m.root != "/root" || !m.disabled["disabled-a"] {
		t.Fatalf("manager not set up: %+v", m)
	}
}

func TestManagerNilSafeMethods(t *testing.T) {
	var m *Manager
	if m.Tools() != nil {
		t.Fatal("nil Tools should return nil")
	}
	if m.Commands() != nil {
		t.Fatal("nil Commands should return nil")
	}
	if m.Subscribers("x") != nil {
		t.Fatal("nil Subscribers should return nil")
	}
	if m.ContextProviders() != nil {
		t.Fatal("nil ContextProviders should return nil")
	}
	if m.Status() != nil {
		t.Fatal("nil Status should return nil")
	}
	if r, f := m.Counts(); r != 0 || f != 0 {
		t.Fatalf("nil Counts should be 0,0")
	}
	m.Stop() // should not panic
	m.Stop() // should not panic
	if err := m.Start(context.Background()); err != nil {
		t.Fatal("nil Start should not error")
	}
	_, err := m.CallTool(context.Background(), "p", "t", nil)
	if err == nil {
		t.Fatal("nil CallTool should error")
	}
	if err := m.InvokeCommand(context.Background(), "p", "c", ""); err == nil {
		t.Fatal("nil InvokeCommand should error")
	}
	m.FireEvent("e", nil) // should not panic
	if s := ProvideAndAssemble(context.Background(), m, "", ""); s != "" {
		t.Fatal("nil ProvideAndAssemble should be empty")
	}
}

func newManagerFixture(t *testing.T, plug *fakePlugin) (*Manager, *hostFixture) {
	t.Helper()
	fx := newFixture(t, plug)
	m := NewManager("/tmp", fx.bridge, nil)
	m.mu.Lock()
	m.clients = map[string]*Client{plug.manifest.Name: fx.client}
	m.mu.Unlock()
	return m, fx
}

func TestManagerTools(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "p", Version: "0.1.0"},
		tools:    []ToolDecl{{Name: "a"}, {Name: "b"}},
	}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	tools := m.Tools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Tool.Name != "a" || tools[1].Tool.Name != "b" {
		t.Fatalf("wrong order or contents: %+v", tools)
	}
}

func TestManagerCommands(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "p", Version: "0.1.0"},
		commands: []CommandDecl{{ID: "c1"}, {ID: "c2"}},
	}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	cmds := m.Commands()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}
}

func TestManagerSubscribers(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{
			Name:        "p",
			Version:     "0.1.0",
			Permissions: Permissions{Events: []string{EventCompact}},
		},
		events: []string{EventCompact},
	}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	subs := m.Subscribers(EventCompact)
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscriber, got %d", len(subs))
	}
	if len(m.Subscribers("nonexistent")) != 0 {
		t.Fatal("expected 0 subscribers for unsubscribed event")
	}
}

func TestManagerContextProviders(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	if len(m.ContextProviders()) != 0 {
		t.Fatal("expected no context providers")
	}
}

func TestManagerStatus(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "p", Version: "0.1.0"},
		tools:    []ToolDecl{{Name: "t1"}},
	}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	st := m.Status()
	if len(st) != 1 || st[0].Name != "p" || st[0].Tools != 1 {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestManagerCounts(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "p", Version: "0.1.0"},
	}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	r, f := m.Counts()
	if r != 1 || f != 0 {
		t.Fatalf("expected (1,0), got (%d,%d)", r, f)
	}
}

func TestManagerCallTool(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "p", Version: "0.1.0"},
		tools:    []ToolDecl{{Name: "echo"}},
		toolFn: func(name string, args json.RawMessage) (any, error) {
			return map[string]string{"echo": name}, nil
		},
	}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	ctx := context.Background()
	raw, err := m.CallTool(ctx, "p", "echo", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !contains(string(raw), "echo") {
		t.Fatalf("unexpected result: %s", raw)
	}
}

func TestManagerCallToolUnknownPlugin(t *testing.T) {
	m := NewManager("/x", nil, nil)
	_, err := m.CallTool(context.Background(), "ghost", "t", nil)
	if err == nil {
		t.Fatal("expected error for unknown plugin")
	}
}

func TestManagerCallToolDisconnected(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	m, fx := newManagerFixture(t, plug)
	fx.client.mu.Lock()
	fx.client.connected = false
	fx.client.mu.Unlock()
	_, err := m.CallTool(context.Background(), "p", "t", nil)
	if err == nil {
		t.Fatal("expected error for disconnected plugin")
	}
	fx.close()
}

func TestManagerInvokeCommand(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{Name: "p", Version: "0.1.0"},
		commands: []CommandDecl{{ID: "run"}},
	}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	err := m.InvokeCommand(context.Background(), "p", "run", "")
	if err != nil {
		t.Logf("invoke error (expected for unimplemented handler): %v", err)
	}
}

func TestManagerInvokeCommandUnknown(t *testing.T) {
	m := NewManager("/x", nil, nil)
	err := m.InvokeCommand(context.Background(), "ghost", "c", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestManagerFireEvent(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{
			Name:        "p",
			Version:     "0.1.0",
			Permissions: Permissions{Events: []string{EventCompact}},
		},
		events: []string{EventCompact},
	}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	m.FireEvent(EventCompact, "data") // should not panic
}

func TestManagerStopDoubleStop(t *testing.T) {
	m := NewManager("/x", nil, nil)
	m.Stop()
	m.Stop() // should not panic
}

func TestManagerProvideEmpty(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	m, fx := newManagerFixture(t, plug)
	defer fx.close()
	snippets := m.Provide(context.Background(), "", "")
	if len(snippets) != 0 {
		t.Fatalf("expected empty, got %d", len(snippets))
	}
}

// ---------------------------------------------------------------------------
// host.toast and host.call_tool handlers (installHostHandlers paths)
// ---------------------------------------------------------------------------

func TestHostToastViaHandler(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	fx := newFixture(t, plug)
	defer fx.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := plug.conn.Call(ctx, "host.toast", map[string]string{
		"kind": "info", "text": "hello",
	})
	if err != nil {
		t.Fatalf("toast: %v", err)
	}
	fx.bridge.mu.Lock()
	toasts := fx.bridge.toasts
	fx.bridge.mu.Unlock()
	if len(toasts) != 1 || toasts[0] != "info:hello" {
		t.Fatalf("unexpected toasts: %v", toasts)
	}
}

func TestHostSessionGetNilBridge(t *testing.T) {
	// Create client with nil bridge to test the fallback path.
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	fx := newFixture(t, plug)
	fx.client.bridge = nil
	defer fx.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	raw, err := plug.conn.Call(ctx, "host.session_get", nil)
	if err != nil {
		t.Fatalf("session_get: %v", err)
	}
	var info SessionInfo
	json.Unmarshal(raw, &info)
	if info.ID != "" {
		t.Fatalf("expected empty session info with nil bridge, got %+v", info)
	}
}

func TestHostCallToolPermissionDenied(t *testing.T) {
	plug := &fakePlugin{manifest: Manifest{Name: "p", Version: "0.1.0"}}
	fx := newFixture(t, plug)
	defer fx.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := plug.conn.Call(ctx, "host.call_tool", map[string]string{"name": "shell"})
	if err == nil {
		t.Fatal("expected permission denied")
	}
	rerr, ok := err.(*RPCError)
	if !ok || rerr.Code != ErrCodePermissionDenied {
		t.Fatalf("want permission denied, got %v", err)
	}
}

func TestHostCallToolDeclared(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{
			Name:        "p",
			Version:     "0.1.0",
			Permissions: Permissions{ToolsCall: []string{"shell"}},
		},
	}
	fx := newFixture(t, plug)
	defer fx.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := plug.conn.Call(ctx, "host.call_tool", map[string]string{"name": "shell"})
	if err == nil {
		t.Fatal("expected 'not yet wired' error")
	}
	rerr, ok := err.(*RPCError)
	if !ok || rerr.Code != ErrCodeInternal {
		t.Fatalf("want internal error (not wired), got %v", err)
	}
}

func TestHostConfigGetNilBridge(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{
			Name:        "p",
			Version:     "0.1.0",
			Permissions: Permissions{ConfigKeys: []string{"k"}},
		},
	}
	fx := newFixture(t, plug)
	fx.client.bridge = nil
	defer fx.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	raw, err := plug.conn.Call(ctx, "host.config_get", map[string]string{"key": "k"})
	if err != nil {
		t.Fatalf("config_get: %v", err)
	}
	var resp struct{ Value any }
	json.Unmarshal(raw, &resp)
	if resp.Value != nil {
		t.Fatalf("expected nil value with nil bridge, got %v", resp.Value)
	}
}

// ---------------------------------------------------------------------------
// Concurrent registration — ensure race-free tool/command/event registration
// ---------------------------------------------------------------------------

func TestConcurrentRegistration(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{
			Name:        "concur",
			Version:     "0.1.0",
			Permissions: Permissions{Events: []string{"*"}},
		},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Concurrently register tools, commands, and events.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = plug.conn.Call(ctx, "host.register_tool", ToolDecl{
				Name: fmt.Sprintf("tool-%d", n),
			})
		}(i)
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = plug.conn.Call(ctx, "host.register_command", CommandDecl{
				ID: fmt.Sprintf("cmd-%d", n),
			})
		}(i)
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = plug.conn.Call(ctx, "host.subscribe", map[string]string{
				"event": fmt.Sprintf("ev-%d", n),
			})
		}(i)
	}
	wg.Wait()

	// Verify registrations landed.
	fx.client.mu.RLock()
	nt := len(fx.client.tools)
	nc := len(fx.client.commands)
	ne := len(fx.client.events)
	fx.client.mu.RUnlock()
	if nt != 20 || nc != 20 || ne != 20 {
		t.Fatalf("expected 20 each, got tools=%d commands=%d events=%d", nt, nc, ne)
	}
}

func TestConcurrentReadsWhileRegistering(t *testing.T) {
	plug := &fakePlugin{
		manifest: Manifest{
			Name:        "cr",
			Version:     "0.1.0",
			Permissions: Permissions{Events: []string{"*"}},
		},
	}
	fx := newFixture(t, plug)
	defer fx.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	var wg sync.WaitGroup

	// Writers: keep registering.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				select {
				case <-done:
					return
				default:
				}
				plug.conn.Call(ctx, "host.register_tool", ToolDecl{
					Name: fmt.Sprintf("t-%d-%d", n, j),
				})
			}
		}(i)
	}

	// Readers: keep reading.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = fx.client.Tools()
				_ = fx.client.Commands()
				_ = fx.client.SubscribedEvents()
				_ = fx.client.Connected()
				_ = fx.client.Manifest()
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(done)
	wg.Wait()
}
