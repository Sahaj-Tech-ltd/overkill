package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls/mcpshield"
)

// ToolWithServer pairs an MCP tool definition with the server name it lives on.
type ToolWithServer struct {
	Server string
	Tool   Tool
}

// ServerStatus is a snapshot of one MCP client for the TUI status panel.
type ServerStatus struct {
	Name       string
	Connected  bool
	ToolsCount int
	LastError  string
}

// Manager owns a pool of MCP clients keyed by server name.
type Manager struct {
	mu          sync.RWMutex
	cfg         config.MCPConfig
	clients     map[string]*Client
	stop        chan struct{}
	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
	cancel      context.CancelFunc
	initErr     error
	wg          sync.WaitGroup
	// policy is the mcpshield capability gate. When non-nil every Call
	// is checked against the declared capability for the target server.
	// nil → legacy behaviour (no shield), preserved for callers that
	// haven't migrated yet.
	policy *mcpshield.Policy
}

// SetPolicy attaches an mcpshield policy. Pass nil to disable.
func (m *Manager) SetPolicy(p *mcpshield.Policy) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = p
}

// NewManager builds a Manager from configuration. Servers aren't started
// until Start is called.
func NewManager(cfg config.MCPConfig) *Manager {
	var policy *mcpshield.Policy
	if len(cfg.Servers) > 0 {
		policy = mcpshield.NewPolicy()
	}
	var configErr error
	seen := make(map[string]bool)
	if len(cfg.Servers) > 64 {
		configErr = fmt.Errorf("mcp: at most 64 servers are supported")
	}
	cfg.Servers = append([]config.MCPServer(nil), cfg.Servers...)
	for index, server := range cfg.Servers {
		server.Args = append([]string(nil), server.Args...)
		server.AllowedTools = append([]string(nil), server.AllowedTools...)
		server.AllowedPathPrefixes = append([]string(nil), server.AllowedPathPrefixes...)
		environment := make(map[string]string, len(server.Env))
		for key, value := range server.Env {
			environment[key] = value
		}
		server.Env = environment
		if server.Enabled != nil {
			enabled := *server.Enabled
			server.Enabled = &enabled
		}
		if server.Trusted != nil {
			trusted := *server.Trusted
			server.Trusted = &trusted
		}
		bindings := make(map[string][]string, len(server.PathArgumentBindings))
		for tool, pointers := range server.PathArgumentBindings {
			bindings[tool] = append([]string{}, pointers...)
		}
		server.PathArgumentBindings = bindings
		cfg.Servers[index] = server
		if server.Enabled != nil && !*server.Enabled {
			continue
		}

		normalized, err := normalizePathPolicy(server)
		if err != nil {
			configErr = err
		} else {
			server = normalized
			cfg.Servers[index] = normalized
		}
		if (server.Name == "" || server.Command == "") && len(server.AllowedTools) == 0 && len(server.AllowedPathPrefixes) == 0 && server.Trusted == nil && server.MaxBytesPerCall == 0 {
			continue
		}
		if seen[server.Name] || server.Name == "" || server.Command == "" || len(server.Name) > 128 || strings.ContainsAny(server.Name, ":\r\n") {
			configErr = fmt.Errorf("mcp: unique server names and commands are required")
			continue
		}
		seen[server.Name] = true
		trusted := true
		if server.Trusted != nil {
			trusted = *server.Trusted
		}
		if err := policy.Set(mcpshield.Capability{ServerName: server.Name, AllowedTools: server.AllowedTools, AllowedPathPrefixes: server.AllowedPathPrefixes, MaxBytesPerCall: server.MaxBytesPerCall, Trusted: trusted}); err != nil {
			configErr = err
		}
	}
	return &Manager{cfg: cfg, clients: make(map[string]*Client), stop: make(chan struct{}), policy: policy, initErr: configErr}
}

// Start spawns each configured server in its own goroutine with retry
// backoff. Returns immediately — readiness can be observed via Status().
func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.stopped {
		return fmt.Errorf("mcp: manager is stopped")
	}
	if m.started {
		return nil
	}
	if m.initErr != nil {
		return m.initErr
	}
	if ctx.Err() != nil {
		return nil
	}
	ctx, m.cancel = context.WithCancel(ctx)
	m.started = true
	for _, s := range m.cfg.Servers {
		if (s.Enabled != nil && !*s.Enabled) || s.Name == "" || s.Command == "" {
			continue
		}
		// Default-on: explicitly disabled servers are skipped.
		s := s
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("mcp: supervise goroutine panic: %v\n%s", r, debug.Stack())
				}
			}()
			m.supervise(ctx, s)
		}()
	}
	return nil
}

// supervise spawns one server and re-spawns it with backoff if it dies.
func (m *Manager) supervise(ctx context.Context, s config.MCPServer) {
	backoff := 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		default:
		}

		command := s.Command
		if s.PathWorkspace != "" && !filepath.IsAbs(command) && strings.ContainsAny(command, "/\\") {
			command = filepath.Join(s.PathWorkspace, command)
		}
		client := NewClient(s.Name, command, s.Args, s.Env)
		if client.cmd != nil && s.PathWorkspace != "" {
			client.cmd.Dir = s.PathWorkspace
		}
		err := client.Start(ctx)
		if err != nil {
			client.setError(err)
			m.installClient(s.Name, client)
			select {
			case <-ctx.Done():
				return
			case <-m.stop:
				return
			case <-time.After(backoff):
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 2 * time.Second
		m.installClient(s.Name, client)

		ticker := time.NewTicker(time.Second)
		connected := true
		for connected {
			select {
			case <-ctx.Done():
				_ = client.Close()
				ticker.Stop()
				return
			case <-m.stop:
				_ = client.Close()
				ticker.Stop()
				return
			case <-client.runDone:
				connected = false
			case <-client.readDone:
				connected = false
			case <-ticker.C:
				refreshCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				tools, err := client.ListTools(refreshCtx)
				cancel()
				if err != nil {
					client.setError(fmt.Errorf("tool discovery failed"))
					connected = false
				} else {
					client.mu.Lock()
					client.tools = tools
					client.mu.Unlock()
				}
			}
		}
		ticker.Stop()
		_ = client.Close()
		client.setError(fmt.Errorf("server exited"))

		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-time.After(backoff):
		}
	}
}

func (m *Manager) installClient(name string, c *Client) {
	m.mu.Lock()
	if old, ok := m.clients[name]; ok && old != c {
		_ = old.Close()
	}
	m.clients[name] = c
	m.mu.Unlock()
}

// Stop tears down all running clients. Blocks until every supervisor
// goroutine has observed the stop signal and exited, so callers can
// safely free the Manager + its clients after Stop returns. The old
// fire-and-forget close-stop-then-return left supervisor goroutines
// alive (sleeping in backoff) that could still write to m.clients
// after the caller had moved on.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.lifecycleMu.Lock()
	if !m.stopped {
		m.stopped = true
		close(m.stop)
		if m.cancel != nil {
			m.cancel()
		}
	}
	m.lifecycleMu.Unlock()
	m.mu.Lock()
	for _, c := range m.clients {
		_ = c.Close()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// Tools returns a flat snapshot of every connected server's tools.
func (m *Manager) Tools() []ToolWithServer {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ToolWithServer
	for name, c := range m.clients {
		if !c.Connected() {
			continue
		}
		for _, t := range c.Tools() {
			if !m.exposesToolLocked(name, t) {
				continue
			}

			out = append(out, ToolWithServer{Server: name, Tool: t})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Server != out[j].Server {
			return out[i].Server < out[j].Server
		}
		return out[i].Tool.Name < out[j].Tool.Name
	})
	return out
}

// exposesToolLocked applies the same catalog gate to bulk and direct lookup.
// The manager read lock must be held by the caller.
func (m *Manager) exposesToolLocked(server string, tool Tool) bool {
	if m.policy == nil {
		return true
	}
	cap, ok := m.policy.Get(server)
	if !ok || !m.policy.CheckCall(server, tool.Name, nil, 0).Allow {
		return false
	}
	if len(cap.AllowedPathPrefixes) > 0 {
		for _, entry := range m.cfg.Servers {
			if entry.Name == server {
				_, bound := entry.PathArgumentBindings[tool.Name]
				return bound
			}
		}
		return false
	}
	return true
}

// lookupTool avoids rebuilding/sorting every server's catalog for each adapter
// metadata/availability read. Tool schemas returned to callers are owned copies.
func (m *Manager) lookupTool(server, name string) (Tool, bool) {
	if m == nil {
		return Tool{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	client := m.clients[server]
	if client == nil {
		return Tool{}, false
	}
	client.mu.RLock()
	defer client.mu.RUnlock()
	if !client.connected {
		return Tool{}, false
	}
	for _, tool := range client.tools {
		if tool.Name == name && m.exposesToolLocked(server, tool) {
			tool.InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
			return tool, true
		}
	}
	return Tool{}, false
}

// Status returns a snapshot of all clients (including failed ones).
func (m *Manager) Status() []ServerStatus {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ServerStatus, 0, len(m.clients))
	for name, c := range m.clients {
		s := ServerStatus{
			Name:       name,
			Connected:  c.Connected(),
			ToolsCount: len(c.Tools()),
		}
		if e := c.LastError(); e != nil {
			s.LastError = e.Error()
			if len(s.LastError) > 512 {
				s.LastError = s.LastError[:512] + " [truncated]"
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RescanTools walks every connected server's tool list and registers any
// tools not already in the registry, returning the count added. The caller
// supplies a `has` predicate (registry name lookup) and a `register`
// callback that registers a freshly-built adapter — this keeps the mcp
// package free of any tools package import.
//
// The adapter naming convention is `mcp:<server>:<tool>`, matching
// NewToolAdapter().
func (m *Manager) RescanTools(has func(name string) bool, register func(adapter *ToolAdapter) error) int {
	if m == nil {
		return 0
	}
	added := 0
	for _, tw := range m.Tools() {
		adapter := NewToolAdapter(m, tw.Server, tw.Tool.Name)
		if has(adapter.Name()) {
			continue
		}
		if err := register(adapter); err == nil {
			added++
		}
	}
	return added
}

// Counts returns (connected, failed). Failed = configured but unable to
// connect at this instant.
func (m *Manager) Counts() (connected int, failed int) {
	if m == nil {
		return 0, 0
	}
	for _, s := range m.Status() {
		if s.Connected {
			connected++
		} else if s.LastError != "" {
			failed++
		}
	}
	return
}

// Call routes a tool call to the right server client.
func (m *Manager) Call(ctx context.Context, server, tool string, args json.RawMessage) (Result, error) {
	if m == nil {
		return Result{}, fmt.Errorf("mcp: manager not initialized")
	}
	m.mu.RLock()
	c, ok := m.clients[server]
	policy := m.policy
	m.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("mcp: unknown server %q", server)
	}
	if !c.Connected() {
		return Result{}, fmt.Errorf("mcp: server %q not connected", server)
	}
	found := false
	var current Tool
	for _, definition := range c.Tools() {
		if definition.Name == tool {
			found = true
			current = definition
			break
		}
	}

	if len(args) > maxMCPArgumentBytes || (len(args) > 0 && !json.Valid(args)) {
		return Result{}, fmt.Errorf("mcp: arguments must be valid JSON within the transport byte limit")
	}
	// No argument paths are guessed from arbitrary JSON. A configured path
	// restriction requires explicit operator bindings and a compatible schema.
	paths := []string(nil)
	if policy != nil {
		cap, ok := policy.Get(server)
		if ok && cap.MaxBytesPerCall > 0 && len(args) > cap.MaxBytesPerCall {
			return Result{}, fmt.Errorf("mcp: arguments exceed declared byte cap")
		}
		if ok && len(cap.AllowedPathPrefixes) > 0 {
			var declaration config.MCPServer
			for _, entry := range m.cfg.Servers {
				if entry.Name == server {
					declaration = entry
					break
				}
			}
			declaration.MaxBytesPerCall = cap.MaxBytesPerCall
			var err error
			args, paths, err = bindCallPaths(declaration, current, args)
			if err != nil {
				return Result{}, err
			}
		}
	}
	if policy != nil {
		decision := policy.CheckCall(server, tool, paths, len(args))
		if !decision.Allow {
			return Result{}, fmt.Errorf("mcp: %s", decision.Reason)
		}
	}
	if !found {
		return Result{}, fmt.Errorf("mcp: tool is not in the current server catalog")
	}
	return c.CallTool(ctx, tool, args)
}
