package subagent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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
type AgentDef struct {
	Name     string
	Command  string
	Args     []string
	Protocol Protocol
	Model    string
	Env      map[string]string
}

// ExternalDelegator manages registration and delegation of tasks to external
// agent processes. All public methods are safe for concurrent use.
type ExternalDelegator struct {
	mu        sync.RWMutex
	agents    map[string]AgentDef
	workDir   string
	timeout   time.Duration
	fileState *FileStateTracker
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
func (d *ExternalDelegator) ListAgents() []AgentDef {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make([]AgentDef, 0, len(d.agents))
	for _, def := range d.agents {
		out = append(out, def)
	}
	return out
}

// Delegate sends a goal to the named external agent. It is a convenience
// wrapper around DelegateWithExport using a minimal ContextExport.
func (d *ExternalDelegator) Delegate(ctx context.Context, agentName, goal string) (*Result, error) {
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
	// 1. Look up agent by name → error if not found.
	d.mu.RLock()
	agent, ok := d.agents[agentName]
	d.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("agent %q not found", agentName)
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

	// 5. Build exec.CommandContext(ctx, agent.Command, append(agent.Args, inputData)...)
	// Copy agent.Args to a new slice so the stored AgentDef is never mutated.
	args := make([]string, len(agent.Args), len(agent.Args)+1)
	copy(args, agent.Args)
	args = append(args, inputData)
	cmd := exec.CommandContext(ctx, agent.Command, args...)

	// 6. Set cmd.Dir to workDir.
	cmd.Dir = d.workDir

	// Pass extra env vars if set.
	if len(agent.Env) > 0 {
		cmd.Env = append(cmd.Environ(), envSlice(agent.Env)...)
	}

	// 7. Capture stdout/stderr via bytes.Buffer.
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
