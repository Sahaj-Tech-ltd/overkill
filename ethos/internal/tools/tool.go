package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Tool interface {
	Name() string
	Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

type ToolResult struct {
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
	Success bool   `json:"success"`
}

type ExecutionOptions struct {
	Timeout    time.Duration
	WorkingDir string
	Env        map[string]string
}

type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("tool: cannot register nil tool")
	}
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool: cannot register tool with empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool: %s already registered", name)
	}
	r.tools[name] = tool
	return nil
}

// Has reports whether a tool with the given name is already registered.
// Used by callers that want idempotent registration (e.g. MCP rescans).
func (r *Registry) Has(name string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool: %s not found", name)
	}
	return t, nil
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
