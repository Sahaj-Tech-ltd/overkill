package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
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
	mu      sync.RWMutex
	cfg     config.MCPConfig
	clients map[string]*Client
	stop    chan struct{}
	wg      sync.WaitGroup
}

// NewManager builds a Manager from configuration. Servers aren't started
// until Start is called.
func NewManager(cfg config.MCPConfig) *Manager {
	return &Manager{
		cfg:     cfg,
		clients: make(map[string]*Client),
		stop:    make(chan struct{}),
	}
}

// Start spawns each configured server in its own goroutine with retry
// backoff. Returns immediately — readiness can be observed via Status().
func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	for _, s := range m.cfg.Servers {
		if s.Name == "" || s.Command == "" {
			continue
		}
		// Default-on: explicitly disabled servers are skipped.
		s := s
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
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

		client := NewClient(s.Name, s.Command, s.Args, s.Env)
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

		// Block until the underlying process exits.
		_ = client.cmd.Wait()
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

// Stop tears down all running clients.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	m.mu.Lock()
	for _, c := range m.clients {
		_ = c.Close()
	}
	m.mu.Unlock()
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
	m.mu.RUnlock()
	if !ok {
		return Result{}, fmt.Errorf("mcp: unknown server %q", server)
	}
	if !c.Connected() {
		return Result{}, fmt.Errorf("mcp: server %q not connected", server)
	}
	return c.CallTool(ctx, tool, args)
}
