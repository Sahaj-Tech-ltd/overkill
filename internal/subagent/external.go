package subagent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Protocol determines how the host communicates with an external agent process.
type Protocol int

const (
	ProtocolStdio Protocol = iota
	ProtocolACP
	ProtocolPipe
)

// AgentDef describes an external agent that can be delegated work.
// When Command is empty the agent is a "built-in" — its definition
// lives in code and is used to configure a sub-agent internally
// (system prompt, tool whitelist, etc.) rather than spawned as a
// separate process.
type AgentDef struct {
	Name         string
	Command      string
	Args         []string
	Protocol     Protocol
	Model        string
	Env          map[string]string
	SystemPrompt string   // built-in agents only: injected as system prompt
	Tools        []string // built-in agents only: tool whitelist names

	// Claude Code parity fields — all optional.
	Description        string   `yaml:"description,omitempty"`        // when to auto-select this agent
	DisallowedTools    []string `yaml:"disallowedTools,omitempty"`    // tools to deny even if inherited
	MaxTurns           int      `yaml:"maxTurns,omitempty"`           // max agentic turns before stopping
	PermissionMode     string   `yaml:"permissionMode,omitempty"`     // acceptEdits/auto/bypass
	Skills             []string `yaml:"skills,omitempty"`             // skills to preload at startup
	Color              string   `yaml:"color,omitempty"`              // UI color for agent identification
	Effort             string   `yaml:"effort,omitempty"`             // thinking effort: off/minimal/low/medium/high/x-high
	InitialPrompt      string   `yaml:"initialPrompt,omitempty"`      // prepended to first user turn
	Background         bool     `yaml:"background,omitempty"`         // always run as background task
	Memory             string   `yaml:"memory,omitempty"`             // persistent memory: user/project/local
	MCPServers         []string `yaml:"mcpServers,omitempty"`         // MCP servers scoped to this agent
	Hooks              []string `yaml:"hooks,omitempty"`              // lifecycle hooks for this agent
	RequiredMCPServers []string `yaml:"requiredMcpServers,omitempty"` // MCP servers required for agent availability
	Filename           string   `yaml:"-"`                            // original filename (set at load time)
}

// ExternalDelegator manages registration and delegation of tasks to external
// agent processes. All public methods are safe for concurrent use.
type ExternalDelegator struct {
	mu        sync.RWMutex
	agents    map[string]AgentDef
	workDir   string
	timeout   time.Duration
	fileState *FileStateTracker

	// Registry, when set, provides file-based agent discovery and
	// auto-selection via SelectBest. Populated at boot time.
	Registry *AgentRegistry
}

// NewExternalDelegator creates a delegator that runs external agents in workDir
// with the given default timeout. If fileState is nil, a new tracker is created.
func NewExternalDelegator(workDir string, timeout time.Duration, fileState *FileStateTracker) *ExternalDelegator {
	if fileState == nil {
		fileState = NewFileStateTracker()
	}
	return &ExternalDelegator{
		agents:    make(map[string]AgentDef),
		workDir:   workDir,
		timeout:   timeout,
		fileState: fileState,
	}
}

// Register adds an external agent definition. If an agent with the same name
// already exists it is replaced.
func (d *ExternalDelegator) Register(def AgentDef) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.agents[def.Name] = def
}

// ListAgents returns all registered agent definitions in an indeterminate order.
// When a Registry is configured, it returns the registry's resolved list
// (which includes file-based, built-in, and CLI agents).
func (d *ExternalDelegator) ListAgents() []AgentDef {
	// Prefer registry if available — it has the full priority-resolved picture.
	if d.Registry != nil {
		return d.Registry.List()
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]AgentDef, 0, len(d.agents))
	for _, def := range d.agents {
		out = append(out, def)
	}
	return out
}

// Delegate sends a goal to the named external agent. If agentName is empty
// and a Registry is configured, SelectBest is used for auto-selection.
// It is a convenience wrapper around DelegateWithExport using a minimal ContextExport.
func (d *ExternalDelegator) Delegate(ctx context.Context, agentName, goal string) (*Result, error) {
	// Auto-select when agent name is empty and registry is available.
	if agentName == "" && d.Registry != nil {
		best := d.Registry.SelectBest(goal)
		if best != nil {
			agentName = best.Name
		}
	}
	export := ContextExport{
		Goal: goal,
	}
	return d.DelegateWithExport(ctx, agentName, export)
}

// DelegateWithExport sends a filtered ContextExport to the named external agent
// process, waits for it to finish (or timeout), and returns the result.
//
// Steps:
//  1. Look up agent by name → error if not found
//  2. exec.LookPath(agent.Command) → error if not in PATH
//  3. Call export.FilterSecrets() then export.ToJSON()
//  4. Create context with timeout
//  5. Build exec.CommandContext(ctx, agent.Command, append(agent.Args, inputData)...)
//  6. Set cmd.Dir to workDir
//  7. Capture stdout/stderr via bytes.Buffer
//  8. Run command
//  9. On error: check ctx.Err() for DeadlineExceeded → "timeout",
//     Canceled → "interrupted", else "failed"
//  10. Return Result with Summary=stdout, status/exitReason set accordingly
func (d *ExternalDelegator) DelegateWithExport(ctx context.Context, agentName string, export ContextExport) (*Result, error) {
	// 1. Look up agent by name — check registry first (priority-resolved),
	// then fall back to locally registered map.
	var agent AgentDef
	if d.Registry != nil {
		if def := d.Registry.Get(agentName); def != nil {
			agent = *def
		}
	}
	if agent.Name == "" {
		d.mu.RLock()
		a, ok := d.agents[agentName]
		d.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("agent %q not found", agentName)
		}
		agent = a
	}

	// 2. exec.LookPath(agent.Command) → error if not in PATH.
	if _, err := exec.LookPath(agent.Command); err != nil {
		return nil, fmt.Errorf("agent %q: command %q not found in PATH", agentName, agent.Command)
	}

	// 3. Call export.FilterSecrets() then export.ToJSON().
	filtered := export.FilterSecrets()
	inputData, err := filtered.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("agent %q: serialise context: %w", agentName, err)
	}

	// 4. Create context with timeout.
	timeout := d.timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 5. Build exec.CommandContext(ctx, agent.Command, agent.Args...).
	// Context JSON is passed via stdin (pipe) instead of as a CLI argument
	// so it never appears in `ps aux` output (C1 fix).
	args := make([]string, len(agent.Args))
	copy(args, agent.Args)
	cmd := exec.CommandContext(ctx, agent.Command, args...)

	// 6. Set cmd.Dir to workDir.
	cmd.Dir = d.workDir

	// Pass extra env vars if set.
	if len(agent.Env) > 0 {
		// B057: cmd.Env is explicitly set (not inherited from os.Environ())
		// so the sub-agent gets a clean environment with only the
		// caller-specified extras. This prevents secrets from the parent
		// process from leaking into sub-agent invocations.
		cmd.Env = envSlice(agent.Env)
	} else {
		// B057: when no explicit env is provided, use a minimal
		// allowlist so the child does NOT inherit os.Environ().
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
			"USER=" + os.Getenv("USER"),
		}
	}

	// 7. Pipe context JSON via stdin instead of a CLI argument.
	cmd.Stdin = strings.NewReader(inputData)

	// 8. Capture stdout/stderr via bytes.Buffer.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 8. Run command.
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	// 9. On error: check ctx.Err() for DeadlineExceeded → "timeout",
	//    Canceled → "interrupted", else "failed".
	if runErr != nil {
		status := "failed"
		exitReason := "failed"

		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout"
			exitReason = "timeout"
		} else if ctx.Err() == context.Canceled {
			status = "interrupted"
			exitReason = "interrupted"
		}

		errMsg := runErr.Error()
		if stderr.Len() > 0 {
			errMsg = stderr.String()
		}

		return &Result{
			Status:     status,
			Summary:    stdout.String(),
			Error:      errMsg,
			ExitReason: exitReason,
			DurationMs: elapsed,
		}, nil
	}

	// 10. Return Result with Summary=stdout, status/exitReason set accordingly.
	return &Result{
		Status:     "completed",
		Summary:    stdout.String(),
		ExitReason: "completed",
		DurationMs: elapsed,
	}, nil
}

// envSlice converts a map to KEY=VALUE slices suitable for cmd.Env.
func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
