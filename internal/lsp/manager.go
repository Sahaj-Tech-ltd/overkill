package lsp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// Manager holds one Client per language and routes calls by file extension.
type Manager struct {
	mu        sync.RWMutex
	cfg       config.LSPConfig
	rootDir   string
	clients   map[string]*Client // language -> client
	byExt     map[string]string  // extension (with dot) -> language
	started   bool
	stopped   bool
	startDone chan struct{}
	startErr  error
	cancel    context.CancelFunc
}

// NewManager builds a manager. Use defaultLSPConfig() for sensible defaults.
func NewManager(cfg config.LSPConfig, rootDir string) *Manager {
	if rootDir == "" {
		rootDir, _ = filepath.Abs(".")
	}
	// Retain a snapshot so later caller mutation cannot change launch arguments
	// or race routing/startup.
	snapshot := cfg
	snapshot.Servers = append([]config.LSPServer(nil), cfg.Servers...)
	for i := range snapshot.Servers {
		snapshot.Servers[i].Args = append([]string(nil), cfg.Servers[i].Args...)
		snapshot.Servers[i].Filetypes = append([]string(nil), cfg.Servers[i].Filetypes...)
	}
	return &Manager{
		cfg:     snapshot,
		rootDir: rootDir,
		clients: make(map[string]*Client),
		byExt:   make(map[string]string),
	}
}

// DefaultConfig returns a config that probes PATH for common language servers.
// Only servers whose binary exists are returned.
func DefaultConfig() config.LSPConfig {
	candidates := []config.LSPServer{
		{Language: "go", Command: "gopls", Filetypes: []string{".go"}},
		{Language: "typescript", Command: "typescript-language-server", Args: []string{"--stdio"}, Filetypes: []string{".ts", ".tsx", ".js", ".jsx"}},
		{Language: "python", Command: "pyright-langserver", Args: []string{"--stdio"}, Filetypes: []string{".py"}},
	}
	out := config.LSPConfig{}
	for _, s := range candidates {
		if _, err := exec.LookPath(s.Command); err == nil {
			out.Servers = append(out.Servers, s)
		}
	}
	return out
}

// Start launches configured servers once and waits for bounded initialization.
// ctx owns their session lifetime, not just readiness. Any failed server is
// reported; callers assembling an all-or-nothing session should call Stop.
func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return fmt.Errorf("lsp: manager stopped")
	}
	if m.started {
		done := m.startDone
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
		m.mu.RLock()
		err := m.startErr
		m.mu.RUnlock()
		return err
	}
	m.started = true
	m.startDone = make(chan struct{})
	lifeCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	var validation error
	if len(m.cfg.Servers) > 64 {
		validation = fmt.Errorf("lsp: at most 64 configured servers supported")
	}
	selected := make([]config.LSPServer, 0, len(m.cfg.Servers))
	languages := make(map[string]bool)
	extensions := make(map[string]string)
	for _, server := range m.cfg.Servers {
		if server.Language == "" || server.Command == "" {
			continue
		}
		if len(server.Language) > 128 || languages[server.Language] {
			validation = fmt.Errorf("lsp: duplicate or invalid language declaration")
			break
		}
		languages[server.Language] = true
		for _, ext := range server.Filetypes {
			ext = strings.ToLower(ext)
			if len(ext) < 2 || !strings.HasPrefix(ext, ".") || strings.ContainsAny(ext, "/\\") {
				validation = fmt.Errorf("lsp: invalid file extension declaration")
				break
			}
			if owner, ok := extensions[ext]; ok && owner != server.Language {
				validation = fmt.Errorf("lsp: conflicting extension declaration")
				break
			}
			extensions[ext] = server.Language
		}
		if validation != nil {
			break
		}
		selected = append(selected, server)
	}
	if validation != nil {
		m.startErr = validation
		cancel()
		close(m.startDone)
		m.mu.Unlock()
		return validation
	}
	for _, server := range selected {
		m.clients[server.Language] = NewClient(server.Language, server.Command, server.Args, m.cfg.MaxMessageBytes)
	}
	m.byExt = extensions
	// Publish every client before releasing the lock, so concurrent Stop sees
	// even not-yet-started clients and prevents their later launch.
	clients := make(map[string]*Client, len(m.clients))
	for language, client := range m.clients {
		clients[language] = client
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	failures := make(chan error, len(clients))
	for _, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := client.Start(lifeCtx, m.rootDir); err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	var startupErrors []error
	for err := range failures {
		startupErrors = append(startupErrors, err)
	}
	if err := lifeCtx.Err(); err != nil {
		startupErrors = append(startupErrors, err)
	}
	sort.Slice(startupErrors, func(i, j int) bool { return startupErrors[i].Error() < startupErrors[j].Error() })
	m.mu.Lock()
	if m.stopped && len(startupErrors) == 0 {
		startupErrors = append(startupErrors, fmt.Errorf("lsp: manager stopped"))
	}
	m.startErr = errors.Join(startupErrors...)
	close(m.startDone)
	err := m.startErr
	m.mu.Unlock()
	return err
}

// Stop is terminal. It cancels pending startup and joins all owned subprocesses
// and readers without holding the routing lock. Repeated/concurrent calls wait
// for the same Client.Close operations rather than racing process handles.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.stopped = true
	cancel, done := m.cancel, m.startDone
	clients := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.mu.Unlock()
	// Give initialized servers their bounded shutdown/exit exchange before
	// cancellation. Close also interrupts a server still initializing.
	var wg sync.WaitGroup
	for _, client := range clients {
		if client != nil {
			wg.Add(1)
			go func() { defer wg.Done(); _ = client.Close() }()
		}
	}
	wg.Wait()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// ClientForFile picks the right language client for a given path. Returns
// nil if no server handles that extension.
func (m *Manager) ClientForFile(path string) *Client {
	if m == nil {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.stopped {
		return nil
	}
	lang, ok := m.byExt[ext]
	if !ok {
		return nil
	}
	c := m.clients[lang]
	if c == nil || !c.Connected() {
		return nil
	}
	return c
}

// ConnectedCount returns how many language servers are live.
func (m *Manager) ConnectedCount() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, c := range m.clients {
		if c.Connected() {
			n++
		}
	}
	return n
}

// Languages returns the names of every connected language server.
func (m *Manager) Languages() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for lang, c := range m.clients {
		if c.Connected() {
			out = append(out, lang)
		}
	}
	sort.Strings(out)
	return out
}
