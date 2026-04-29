package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool: %s already registered", name)
	}
	r.tools[name] = tool
	return nil
}

func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool: %s not found", name)
	}
	return t, nil
}

func (r *Registry) List() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
