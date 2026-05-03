package plugin

import (
	"context"
	"encoding/json"
	"io"
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
func (c *rwCloser) Close() error                 { c.r.Close(); return c.w.Close() }

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
