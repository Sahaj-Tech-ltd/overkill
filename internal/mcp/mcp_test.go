package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls/mcpshield"
)

// ---------------------------------------------------------------------------
// Manager nil-path tests
// ---------------------------------------------------------------------------

func TestManagerNilStart(t *testing.T) {
	var m *Manager
	if err := m.Start(context.Background()); err != nil {
		t.Errorf("nil Start: %v", err)
	}
}

func TestManagerNilStop(t *testing.T) {
	var m *Manager
	m.Stop() // should not panic
}

func TestManagerNilTools(t *testing.T) {
	var m *Manager
	if tools := m.Tools(); tools != nil {
		t.Errorf("nil Tools: expected nil, got %+v", tools)
	}
}

func TestManagerNilStatus(t *testing.T) {
	var m *Manager
	if s := m.Status(); s != nil {
		t.Errorf("nil Status: expected nil, got %+v", s)
	}
}

func TestManagerNilRescanTools(t *testing.T) {
	var m *Manager
	n := m.RescanTools(nil, nil)
	if n != 0 {
		t.Errorf("nil RescanTools: expected 0, got %d", n)
	}
}

func TestManagerNilCounts(t *testing.T) {
	var m *Manager
	c, f := m.Counts()
	if c != 0 || f != 0 {
		t.Errorf("nil Counts: expected 0/0, got %d/%d", c, f)
	}
}

func TestManagerNilCall(t *testing.T) {
	var m *Manager
	_, err := m.Call(context.Background(), "srv", "tool", nil)
	if err == nil {
		t.Error("nil Call: expected error")
	}
}

func TestManagerNilSetPolicy(t *testing.T) {
	var m *Manager
	m.SetPolicy(nil) // should not panic
}

// ---------------------------------------------------------------------------
// Manager Start / Stop
// ---------------------------------------------------------------------------

func TestManagerStartSkipsEmptyNameOrCommand(t *testing.T) {
	cfg := config.MCPConfig{
		Servers: []config.MCPServer{
			{Name: "", Command: "echo"},           // empty name → skip
			{Name: "hasName", Command: ""},        // empty command → skip
			{Name: "good", Command: "nonexistent"}, // will fail but should attempt
		},
	}
	m := NewManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so supervise exits fast

	// Start should not block even though the server command doesn't exist.
	// The supervisor will try, fail, set error, and exit via ctx cancellation.
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Give supervise goroutines time to observe the cancelled context.
	m.wg.Wait()
	m.Stop()
}

func TestManagerCallUnknownServer(t *testing.T) {
	m := NewManager(config.MCPConfig{})
	_, err := m.Call(context.Background(), "nosuch", "tool", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown server") {
		t.Errorf("expected 'unknown server', got %v", err)
	}
}

func TestManagerCallDisconnectedServer(t *testing.T) {
	m := NewManager(config.MCPConfig{})
	m.mu.Lock()
	m.clients["offline"] = &Client{name: "offline", connected: false}
	m.mu.Unlock()

	_, err := m.Call(context.Background(), "offline", "tool", nil)
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("expected 'not connected', got %v", err)
	}
}

func TestManagerCallWithPolicyGate(t *testing.T) {
	m := NewManager(config.MCPConfig{})
	m.mu.Lock()
	m.clients["gated"] = &Client{name: "gated", connected: true}
	m.mu.Unlock()

	policy := mcpshield.NewPolicy()
	// Declare the server but with an allow-list that excludes "tool"
	_ = policy.Set(mcpshield.Capability{
		ServerName:   "gated",
		Trusted:      false,
		AllowedTools: []string{"other_tool"},
	})
	m.SetPolicy(policy)

	_, err := m.Call(context.Background(), "gated", "tool", nil)
	if err == nil || !strings.Contains(err.Error(), "not in allow-list") {
		t.Errorf("expected policy denial, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Manager Status / Counts / Tools
// ---------------------------------------------------------------------------

func TestManagerStatus(t *testing.T) {
	m := NewManager(config.MCPConfig{})
	m.mu.Lock()
	m.clients["alpha"] = &Client{
		name: "alpha", connected: true,
		tools: []Tool{{Name: "a1"}, {Name: "a2"}},
	}
	m.clients["beta"] = &Client{
		name: "beta", connected: false,
		lastErr: io.EOF,
	}
	m.mu.Unlock()

	statuses := m.Status()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
	// Sorted by name
	if statuses[0].Name != "alpha" || statuses[1].Name != "beta" {
		t.Errorf("unexpected order: %+v", statuses)
	}
	if !statuses[0].Connected || statuses[0].ToolsCount != 2 {
		t.Errorf("alpha status wrong: %+v", statuses[0])
	}
	if statuses[1].Connected {
		t.Error("beta should be disconnected")
	}
	if statuses[1].LastError != "EOF" {
		t.Errorf("beta lastErr = %q", statuses[1].LastError)
	}
}

func TestManagerCounts(t *testing.T) {
	m := NewManager(config.MCPConfig{})
	m.mu.Lock()
	m.clients["up1"] = &Client{name: "up1", connected: true}
	m.clients["up2"] = &Client{name: "up2", connected: true}
	m.clients["down"] = &Client{name: "down", connected: false, lastErr: io.EOF}
	m.clients["justStarting"] = &Client{name: "justStarting", connected: false} // no error → not counted as failed
	m.mu.Unlock()

	c, f := m.Counts()
	if c != 2 {
		t.Errorf("connected = %d, want 2", c)
	}
	if f != 1 {
		t.Errorf("failed = %d, want 1 (only 'down' has LastError)", f)
	}
}

func TestManagerToolsSorting(t *testing.T) {
	m := NewManager(config.MCPConfig{})
	m.mu.Lock()
	m.clients["zulu"] = &Client{
		name: "zulu", connected: true,
		tools: []Tool{{Name: "toolA"}, {Name: "toolB"}},
	}
	m.clients["alpha"] = &Client{
		name: "alpha", connected: true,
		tools: []Tool{{Name: "toolX"}},
	}
	m.clients["offline"] = &Client{
		name: "offline", connected: false,
		tools: []Tool{{Name: "hidden"}},
	}
	m.mu.Unlock()

	tools := m.Tools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools (excluding offline), got %d", len(tools))
	}
	// Sorted: alpha/toolX, zulu/toolA, zulu/toolB
	if tools[0].Server != "alpha" || tools[0].Tool.Name != "toolX" {
		t.Errorf("unexpected 0: %s/%s", tools[0].Server, tools[0].Tool.Name)
	}
	if tools[1].Server != "zulu" || tools[1].Tool.Name != "toolA" {
		t.Errorf("unexpected 1: %s/%s", tools[1].Server, tools[1].Tool.Name)
	}
	if tools[2].Server != "zulu" || tools[2].Tool.Name != "toolB" {
		t.Errorf("unexpected 2: %s/%s", tools[2].Server, tools[2].Tool.Name)
	}
}

// ---------------------------------------------------------------------------
// jsonrpcConn tests
// ---------------------------------------------------------------------------

func TestJSONRPCNotify(t *testing.T) {
	cR, cW := io.Pipe()
	conn := newJSONRPCConn(cW, cR)
	go func() { _ = conn.readLoop() }()
	defer conn.close()

	err := conn.Notify("test/notification", map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	// Notification should not wait for response — just verify no error.
}

func TestJSONRPCCallOnClosedConn(t *testing.T) {
	cR, cW := io.Pipe()
	conn := newJSONRPCConn(cW, cR)
	conn.close()

	_, err := conn.Call(context.Background(), "method", nil)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("expected closed error, got %v", err)
	}
}

func TestJSONRPCNotifyOnClosedConn(t *testing.T) {
	cR, cW := io.Pipe()
	conn := newJSONRPCConn(cW, cR)
	conn.close()

	err := conn.Notify("method", nil)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("expected closed error, got %v", err)
	}
}

func TestJSONRPCSetNotifyHandler(t *testing.T) {
	cR, cW := io.Pipe()
	conn := newJSONRPCConn(cW, cR)

	got := make(chan struct{}, 1)
	conn.setNotifyHandler(func(method string, params json.RawMessage) {
		got <- struct{}{}
	})

	go func() { _ = conn.readLoop() }()
	defer conn.close()

	// Write a notification into the pipe that the conn reads from.
	// conn reads from cR, so we write to cW (the other end of the pipe).
	raw := `{"jsonrpc":"2.0","method":"custom/event","params":{"x":42}}` + "\n"
	if _, err := cW.Write([]byte(raw)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Wait for the handler to fire
	select {
	case <-got:
		// notification delivered
	case <-time.After(time.Second):
		t.Error("notification handler was not called")
	}
}

func TestJSONRPCError_Error(t *testing.T) {
	var nilErr *jsonrpcError
	if nilErr.Error() != "" {
		t.Errorf("nil jsonrpcError.Error(): expected '', got %q", nilErr.Error())
	}

	e := &jsonrpcError{Code: -32600, Message: "invalid request"}
	if !strings.Contains(e.Error(), "-32600") {
		t.Errorf("Error(): %q", e.Error())
	}
}

func TestJSONRPCFailAllPending(t *testing.T) {
	cR, cW := io.Pipe()
	conn := newJSONRPCConn(cW, cR)

	// Create a pending call
	id := conn.nextID.Add(1)
	_, _ = json.Marshal(id)
	ch := make(chan *jsonrpcMessage, 1)
	conn.pending.Store(id, ch)

	// Simulate a read error → failAllPending
	conn.failAllPending(io.EOF)

	select {
	case resp := <-ch:
		if resp.Error == nil || resp.Error.Code != -32000 {
			t.Errorf("expected error response, got %+v", resp)
		}
	default:
		t.Error("expected error on pending call channel")
	}
}

// ---------------------------------------------------------------------------
// Client tests
// ---------------------------------------------------------------------------

func TestNewClientWithEnv(t *testing.T) {
	c := NewClient("envtest", "echo", []string{"hello"}, map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	})
	if c.name != "envtest" {
		t.Errorf("name = %q", c.name)
	}
	// Verify env vars are appended
	found := 0
	for _, e := range c.cmd.Env {
		if e == "FOO=bar" || e == "BAZ=qux" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected both env vars in cmd.Env, got %d of 2", found)
	}
}

func TestNewClientWithoutEnv(t *testing.T) {
	c := NewClient("noenv", "echo", []string{"hi"}, nil)
	if c.cmd.Env != nil {
		t.Error("expected nil Env when no env map provided")
	}
}

func TestClientName(t *testing.T) {
	c := &Client{name: "test-name"}
	if c.Name() != "test-name" {
		t.Errorf("Name() = %q", c.Name())
	}
}

func TestClientListToolsBeforeStart(t *testing.T) {
	c := &Client{name: "nostart"}
	_, err := c.ListTools(context.Background())
	if err == nil {
		t.Error("expected error for ListTools before start")
	}
}

func TestClientCallToolBeforeStart(t *testing.T) {
	c := &Client{name: "nostart"}
	_, err := c.CallTool(context.Background(), "t", nil)
	if err == nil {
		t.Error("expected error for CallTool before start")
	}
}

func TestClientListResourcesBeforeStart(t *testing.T) {
	c := &Client{name: "nostart"}
	_, err := c.ListResources(context.Background())
	if err == nil {
		t.Error("expected error for ListResources before start")
	}
}

func TestClientCloseBeforeStart(t *testing.T) {
	c := &Client{name: "nostart"}
	err := c.Close()
	if err != nil {
		t.Errorf("Close before start: %v", err)
	}
}

func TestClientSetError(t *testing.T) {
	c := &Client{name: "err", connected: true}
	c.setError(io.EOF)
	if c.Connected() {
		t.Error("should be disconnected after setError")
	}
	if c.LastError() == nil {
		t.Error("LastError should be non-nil after setError")
	}
}

func TestClientConnectedToolsLastError(t *testing.T) {
	c := &Client{
		name:      "test",
		connected: true,
		tools:     []Tool{{Name: "t1"}, {Name: "t2"}},
		lastErr:   io.EOF,
	}
	if !c.Connected() {
		t.Error("should be connected")
	}
	if len(c.Tools()) != 2 {
		t.Errorf("expected 2 tools, got %d", len(c.Tools()))
	}
	if c.LastError() == nil {
		t.Error("should have last error")
	}

	// Verify tools copy is independent
	copied := c.Tools()
	copied[0] = Tool{Name: "modified"}
	if c.tools[0].Name == "modified" {
		t.Error("Tools() should return a copy, not the internal slice")
	}
}

// ---------------------------------------------------------------------------
// ToolAdapter tests
// ---------------------------------------------------------------------------

func TestToolAdapterName(t *testing.T) {
	a := NewToolAdapter(nil, "myserver", "mytool")
	if a.Name() != "mcp:myserver:mytool" {
		t.Errorf("Name() = %q, want 'mcp:myserver:mytool'", a.Name())
	}
}

func TestToolAdapterExecuteError(t *testing.T) {
	m := NewManager(config.MCPConfig{})
	a := NewToolAdapter(m, "nonexistent", "tool")

	result, err := a.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute should marshal error, not return Go error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if errStr, ok := parsed["error"].(string); !ok || errStr == "" {
		t.Errorf("expected error in result, got %+v", parsed)
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestManagerConcurrentStatusCalls(t *testing.T) {
	m := NewManager(config.MCPConfig{})
	m.mu.Lock()
	m.clients["alpha"] = &Client{name: "alpha", connected: true, tools: []Tool{{Name: "a"}}}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Status()
			_ = m.Tools()
			m.Counts()
		}()
	}
	wg.Wait()
}

func TestClientConcurrentAccessors(t *testing.T) {
	c := &Client{
		name:      "concurrent",
		connected: true,
		tools:     []Tool{{Name: "t"}},
	}

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Connected()
			_ = c.Tools()
			_ = c.LastError()
		}()
	}
	// Simultaneously modify
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.setError(io.EOF)
	}()
	wg.Wait()
}
