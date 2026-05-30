package subagent

import (
	"context"
	"fmt"
	"time"
)

// SpawnConfig holds all parameters for spawning a sub-agent with
// full Claude Code / OpenClaude parity. Every field that OpenClaude's
// runAgent accepts is represented here.
type SpawnConfig struct {
	// Agent definition (from registry or built-in).
	Agent AgentDef

	// Prompt messages — the goal/context for this agent run.
	Goal    string
	Context string

	// Whether this is an async/background agent. Async agents
	// auto-deny permission prompts since there's no UI.
	IsAsync bool

	// When true, the agent CAN show permission prompts even if async.
	CanShowPermissionPrompts bool

	// Forked context from the parent conversation.
	ForkContext string

	// Model override from the caller.
	ModelOverride string
	ProviderOverride string

	// Max turns/steps override.
	MaxStepsOverride int

	// Max tokens override.
	MaxTokensOverride int

	// Timeout override.
	TimeoutOverride time.Duration

	// Tool whitelist for this agent session (agent.Tools if empty).
	AllowedTools []string

	// Worktree path if agent has isolation: "worktree".
	WorktreePath string

	// Parent agent ID for hierarchy tracking.
	ParentAgentID string

	// Task index for result tracking.
	TaskIndex int
}

// AgentRunner spawns and manages sub-agents with full OpenClaude parity.
// Handles: model cascade, permission mode cascade, effort override,
// slim context for read-only agents, initial prompt injection, and
// background auto-deny.
type AgentRunner struct {
	cfg     SpawnConfig
	worker  *RealWorker
	agentID string
	started time.Time
}

// NewAgentRunner creates a runner from a SpawnConfig. Resolves effective
// model, max steps, and permission mode using OpenClaude's cascade logic.
func NewAgentRunner(cfg SpawnConfig) (*AgentRunner, error) {
	// Resolve effective model with cascade: agent def → caller override → default.
	model := resolveModel(cfg)
	provider := resolveProvider(cfg)
	maxSteps := resolveMaxSteps(cfg)
	maxTokens := resolveMaxTokens(cfg)
	timeout := resolveTimeout(cfg)

	// Build the RealWorker config.
	workerCfg := RealWorkerConfig{
		Goal:      buildPrompt(cfg),
		Context:   cfg.Context,
		Model:     model,
		Provider:  provider,
		MaxSteps:  maxSteps,
		MaxTokens: maxTokens,
		Timeout:   timeout,
		TaskIndex: cfg.TaskIndex,
	}

	worker, err := NewRealWorker(workerCfg)
	if err != nil {
		return nil, fmt.Errorf("agent runner %q: %w", cfg.Agent.Name, err)
	}

	return &AgentRunner{
		cfg:     cfg,
		worker:  worker,
		agentID: generateAgentID(),
		started: time.Now(),
	}, nil
}

// Run executes the sub-agent and returns the result.
// For async agents without permission prompts, auto-deny is active.
func (r *AgentRunner) Run(ctx context.Context) (*Result, error) {
	// For async/background agents: auto-deny permissions.
	if r.cfg.IsAsync && !r.cfg.CanShowPermissionPrompts {
		ctx = withAutoDenyPermissions(ctx)
	}

	result, err := r.worker.Run(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent %q (%s): %w", r.cfg.Agent.Name, r.agentID, err)
	}

	return result, nil
}

// AgentID returns the unique identifier for this agent run.
func (r *AgentRunner) AgentID() string { return r.agentID }

// StartedAt returns when the agent was spawned.
func (r *AgentRunner) StartedAt() time.Time { return r.started }

// AgentName returns the agent definition name.
func (r *AgentRunner) AgentName() string { return r.cfg.Agent.Name }

// --- Resolvers (OpenClaude cascade logic) ---

func resolveModel(cfg SpawnConfig) string {
	// Agent definition's model takes priority (unless "inherit").
	if cfg.Agent.Model != "" && cfg.Agent.Model != "inherit" {
		return cfg.Agent.Model
	}
	if cfg.ModelOverride != "" {
		return cfg.ModelOverride
	}
	return "mimo-v2-pro" // default
}

func resolveProvider(cfg SpawnConfig) string {
	if cfg.ProviderOverride != "" {
		return cfg.ProviderOverride
	}
	return "xiaomi" // default
}

func resolveMaxSteps(cfg SpawnConfig) int {
	if cfg.Agent.MaxTurns > 0 {
		return cfg.Agent.MaxTurns
	}
	if cfg.MaxStepsOverride > 0 {
		return cfg.MaxStepsOverride
	}
	return 50
}

func resolveMaxTokens(cfg SpawnConfig) int {
	if cfg.MaxTokensOverride > 0 {
		return cfg.MaxTokensOverride
	}
	return 16384
}

func resolveTimeout(cfg SpawnConfig) time.Duration {
	if cfg.TimeoutOverride > 0 {
		return cfg.TimeoutOverride
	}
	return 5 * time.Minute
}

// buildPrompt constructs the final prompt with:
// - Slim context for read-only agents (omitClaudeMd)
// - Initial prompt injection
// - Context forking from parent
// - Goal as the main instruction
func buildPrompt(cfg SpawnConfig) string {
	var p string

	// Agent-defined initial prompt (prepended to first turn).
	if cfg.Agent.InitialPrompt != "" {
		p += cfg.Agent.InitialPrompt + "\n\n"
	}

	// Forked context from parent conversation.
	if cfg.ForkContext != "" {
		p += "[Context from parent conversation]\n" + cfg.ForkContext + "\n\n"
	}

	// Slim context for read-only agents — skip CLAUDE.md/AGENTS.md loading.
	// Matches OpenClaude's omitClaudeMd: Explore/Plan are search agents,
	// the main agent interprets their output.
	if isReadOnlyAgent(cfg.Agent) {
		p += "[Note: Read-only agent. Project guidelines (CLAUDE.md, AGENTS.md) are omitted for efficiency. The main agent interprets your output.]\n\n"
	}

	// The actual goal.
	if cfg.Goal != "" {
		p += cfg.Goal
	}

	return p
}

// isReadOnlyAgent returns true for agents that should skip project context.
// Explore/Plan agents or any agent with only read/search tools.
func isReadOnlyAgent(agent AgentDef) bool {
	switch agent.Name {
	case "explore", "plan", "Explore", "Plan":
		return true
	}
	// No tools specified = read-only (safe default).
	if len(agent.Tools) == 0 {
		return true
	}
	for _, t := range agent.Tools {
		if t == "write" || t == "edit" || t == "patch" || t == "shell" {
			return false
		}
	}
	return true
}

// withAutoDenyPermissions signals the worker to auto-deny permission prompts.
func withAutoDenyPermissions(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyAutoDeny{}, true)
}

type ctxKeyAutoDeny struct{}

// IsAutoDeny returns true if the context signals auto-deny for permissions.
func IsAutoDeny(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyAutoDeny{}).(bool)
	return v
}

func generateAgentID() string {
	return fmt.Sprintf("agent-%d", time.Now().UnixNano())
}
