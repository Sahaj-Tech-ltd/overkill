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

// AvailableTool is an optional dynamic catalog gate. Native tools without it
// retain their existing behavior; unavailable adapters never resolve or advertise.
type AvailableTool interface{ Available() bool }

func toolAvailable(tool Tool) bool {
	if dynamic, ok := tool.(AvailableTool); ok {
		return dynamic.Available()
	}
	return tool != nil
}

// ConcurrencySafeTool is an optional interface. Tools that implement it
// can execute in parallel with other concurrency-safe tools. Tools that
// don't implement it default to exclusive execution (one at a time).
type ConcurrencySafeTool interface {
	Tool
	// IsConcurrencySafe reports whether this invocation of the tool
	// can run in parallel with other concurrent-safe tools. Tool
	// implementations should base this on the input — e.g., a Bash
	// tool returns true for "ls" but false for "npm install".
	IsConcurrencySafe(input json.RawMessage) bool
}

// InterruptBehavior describes how a tool reacts to user interruption.
type InterruptBehavior int

const (
	// InterruptCancel means the tool is killed immediately when the
	// user sends a new message. Suitable for read-only tools and
	// idempotent operations.
	InterruptCancel InterruptBehavior = iota
	// InterruptBlock means the tool finishes before the agent responds
	// to the user. Suitable for writes that must not leave half a file.
	InterruptBlock
)

// InterruptibleTool is an optional interface. Tools that implement it
// declare their interruption policy. Tools that don't default to
// InterruptBlock (let them finish).
type InterruptibleTool interface {
	Tool
	InterruptBehavior() InterruptBehavior
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
	guard ExecutionGuard
}

// ExecutionGuard is installed by the application when constructing a session.
// The supplied tool is the original adapter; invoke it only after admission.
type ExecutionGuard func(context.Context, Tool, json.RawMessage) (json.RawMessage, error)

func (r *Registry) SetExecutionGuard(guard ExecutionGuard) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.guard = guard
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

// GetConcurrency checks whether the named tool with the given input
// is safe to run concurrently with other tools. Returns true for
// read-only operations (Read, Grep, Glob, LSP) and false for
// everything else (Bash, Write, Edit). Tools can implement the
// optional ConcurrencySafeTool interface for per-input decisions.
func (r *Registry) GetConcurrency(name string, input json.RawMessage) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, false
	}
	if !toolAvailable(t) {
		return nil, false
	}
	// Backward compat: tools without the interface are never concurrent-safe.
	cst, ok := t.(ConcurrencySafeTool)
	if !ok {
		return guardedInvocation(t, r.guard), false
	}
	return guardedInvocation(t, r.guard), cst.IsConcurrencySafe(input)
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
	if !ok || !toolAvailable(t) {
		return nil, fmt.Errorf("tool: %s not found or unavailable", name)
	}
	return guardedInvocation(t, r.guard), nil
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name, tool := range r.tools {
		if !toolAvailable(tool) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
