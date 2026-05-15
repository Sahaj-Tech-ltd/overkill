package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MaxRestarts is how many times a crashed plugin will be respawned before
// the manager gives up until the next overkill launch.
const MaxRestarts = 3

// Manager owns N Clients keyed by plugin name. It performs discovery,
// supervises restarts with exponential backoff, and routes RPCs.
type Manager struct {
	root     string
	bridge   HostBridge
	disabled map[string]bool

	mu       sync.RWMutex
	clients  map[string]*Client
	restarts map[string]int
	stopped  bool
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewManager constructs a Manager rooted at the given directory. `disabled`
// is the set of plugin names to skip (typically cfg.Plugins.Disabled).
func NewManager(root string, bridge HostBridge, disabled []string) *Manager {
	dis := make(map[string]bool, len(disabled))
	for _, d := range disabled {
		dis[d] = true
	}
	return &Manager{
		root:     root,
		bridge:   bridge,
		disabled: dis,
		clients:  make(map[string]*Client),
		restarts: make(map[string]int),
		stopCh:   make(chan struct{}),
	}
}

// Start runs discovery and spawns a supervisor for each plugin. Returns
// immediately; readiness is observable via Status.
func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	found, err := Discover(m.root)
	if err != nil {
		return err
	}
	for _, d := range found {
		if m.disabled[d.Name] {
			continue
		}
		d := d
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.supervise(ctx, d)
		}()
	}
	return nil
}

func (m *Manager) supervise(ctx context.Context, d Discovered) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		default:
		}
		if m.restartsFor(d.Name) >= MaxRestarts {
			return
		}
		client := NewClient(d.Name, d.EntryPath, d.EntryArgs, d.Env, m.bridge)
		if d.StaticManifest != nil {
			client.SetStaticManifest(*d.StaticManifest)
		}
		err := client.Start(ctx)
		m.installClient(d.Name, client)
		if err != nil {
			m.bumpRestart(d.Name)
			select {
			case <-ctx.Done():
				return
			case <-m.stopCh:
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		// Success — wait for the process to exit, then decide whether to
		// restart.
		_ = client.Wait()
		client.setError(fmt.Errorf("plugin process exited"))
		m.bumpRestart(d.Name)
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (m *Manager) installClient(name string, c *Client) {
	m.mu.Lock()
	if old, ok := m.clients[name]; ok && old != c {
		_ = old.Shutdown(context.Background())
	}
	m.clients[name] = c
	m.mu.Unlock()
}

func (m *Manager) bumpRestart(name string) {
	m.mu.Lock()
	m.restarts[name]++
	m.mu.Unlock()
}

func (m *Manager) restartsFor(name string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.restarts[name]
}

// Stop sends shutdown to every client and waits briefly.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	close(m.stopCh)
	clients := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, c := range clients {
		_ = c.Shutdown(ctx)
	}
	// Wait for supervisor goroutines to observe the stop signal +
	// exit before returning. Old code returned with goroutines still
	// alive (sleeping in backoff), which could write to m.clients
	// after the caller freed the Manager.
	m.wg.Wait()
}

// Tools returns every connected plugin's tools, flattened.
func (m *Manager) Tools() []ToolWithPlugin {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ToolWithPlugin
	for name, c := range m.clients {
		if !c.Connected() {
			continue
		}
		for _, t := range c.Tools() {
			out = append(out, ToolWithPlugin{Plugin: name, Tool: t})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Plugin != out[j].Plugin {
			return out[i].Plugin < out[j].Plugin
		}
		return out[i].Tool.Name < out[j].Tool.Name
	})
	return out
}

// Commands returns every connected plugin's slash commands.
func (m *Manager) Commands() []CommandWithPlugin {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []CommandWithPlugin
	for name, c := range m.clients {
		if !c.Connected() {
			continue
		}
		for _, d := range c.Commands() {
			out = append(out, CommandWithPlugin{Plugin: name, Command: d})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Plugin != out[j].Plugin {
			return out[i].Plugin < out[j].Plugin
		}
		return out[i].Command.ID < out[j].Command.ID
	})
	return out
}

// Subscribers returns the plugins that subscribed to the given event.
func (m *Manager) Subscribers(event string) []*Client {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Client
	for _, c := range m.clients {
		if !c.Connected() {
			continue
		}
		for _, ev := range c.SubscribedEvents() {
			if ev == event {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// ContextProviders returns connected plugins that registered as providers.
func (m *Manager) ContextProviders() []*Client {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Client
	for _, c := range m.clients {
		if !c.Connected() {
			continue
		}
		if c.HasContextProvider() {
			out = append(out, c)
		}
	}
	return out
}

// Status returns a snapshot for the /plugins dialog.
func (m *Manager) Status() []Status {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.clients))
	for name, c := range m.clients {
		s := Status{
			Name:     name,
			Version:  c.Manifest().Version,
			Running:  c.Connected(),
			Tools:    len(c.Tools()),
			Commands: len(c.Commands()),
			Restarts: m.restarts[name],
			Disabled: m.disabled[name],
		}
		if e := c.LastError(); e != nil {
			s.LastError = e.Error()
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Counts returns (running, failed) for the status bar.
func (m *Manager) Counts() (int, int) {
	if m == nil {
		return 0, 0
	}
	r, f := 0, 0
	for _, s := range m.Status() {
		if s.Running {
			r++
		} else if s.LastError != "" {
			f++
		}
	}
	return r, f
}

// CallTool routes a tool invocation to the owning plugin.
func (m *Manager) CallTool(ctx context.Context, plugin, name string, args json.RawMessage) (json.RawMessage, error) {
	if m == nil {
		return nil, fmt.Errorf("plugin: manager not initialized")
	}
	m.mu.RLock()
	c, ok := m.clients[plugin]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin: unknown plugin %q", plugin)
	}
	if !c.Connected() {
		return nil, fmt.Errorf("plugin: %q not connected", plugin)
	}
	return c.CallTool(ctx, name, args)
}

// InvokeCommand routes a slash-command selection to the owning plugin.
func (m *Manager) InvokeCommand(ctx context.Context, plugin, id, args string) error {
	if m == nil {
		return fmt.Errorf("plugin: manager not initialized")
	}
	m.mu.RLock()
	c, ok := m.clients[plugin]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("plugin: unknown plugin %q", plugin)
	}
	if !c.Connected() {
		return fmt.Errorf("plugin: %q not connected", plugin)
	}
	return c.InvokeCommand(ctx, id, args)
}

// FireEvent broadcasts an event to every subscribed plugin.
func (m *Manager) FireEvent(event string, payload any) {
	if m == nil {
		return
	}
	for _, c := range m.Subscribers(event) {
		_ = c.FireEvent(event, payload)
	}
}

// Provide queries every context provider in parallel with a 2s timeout per
// plugin. Plugins that error or time out are silently skipped.
func (m *Manager) Provide(ctx context.Context, prompt, sessionID string) []ContextSnippet {
	providers := m.ContextProviders()
	if len(providers) == 0 {
		return nil
	}
	type item struct {
		plugin   string
		snippets []ContextSnippet
	}
	results := make(chan item, len(providers))
	for _, c := range providers {
		c := c
		go func() {
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			snippets, err := c.Provide(callCtx, prompt, sessionID)
			if err != nil {
				results <- item{plugin: c.Name()}
				return
			}
			results <- item{plugin: c.Name(), snippets: snippets}
		}()
	}
	var out []ContextSnippet
	for range providers {
		it := <-results
		out = append(out, it.snippets...)
	}
	return out
}
