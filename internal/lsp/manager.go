package lsp

import (
	"context"
	"log"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// Manager holds one Client per language and routes calls by file extension.
type Manager struct {
	mu      sync.RWMutex
	cfg     config.LSPConfig
	rootDir string
	clients map[string]*Client // language -> client
	byExt   map[string]string  // extension (with dot) -> language
}

// NewManager builds a manager. Use defaultLSPConfig() for sensible defaults.
func NewManager(cfg config.LSPConfig, rootDir string) *Manager {
	if rootDir == "" {
		rootDir, _ = filepath.Abs(".")
	}
	return &Manager{
		cfg:     cfg,
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

// Start launches all configured servers in parallel.
func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	var wg sync.WaitGroup
	for _, s := range m.cfg.Servers {
		s := s
		if s.Language == "" || s.Command == "" {
			continue
		}
		// Pre-register the extension mapping so file routing works even if
		// the server is still starting.
		for _, ft := range s.Filetypes {
			m.byExt[strings.ToLower(ft)] = s.Language
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("lsp: start goroutine panic: %v\n%s", r, debug.Stack())
				}
			}()
			c := NewClient(s.Language, s.Command, s.Args, m.cfg.MaxMessageBytes)
			if err := c.Start(ctx, m.rootDir); err != nil {
				c.setError(err)
			}
			m.mu.Lock()
			m.clients[s.Language] = c
			m.mu.Unlock()
		}()
	}
	// Don't block forever — give the servers a bounded window then return.
	wg.Wait()
}

// Stop terminates every running language server.
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		_ = c.Close()
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
	return out
}
