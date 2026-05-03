package tui

import (
	"github.com/Sahaj-Tech-ltd/ethos/internal/acp"
	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
	"github.com/Sahaj-Tech-ltd/ethos/internal/browser"
	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/cost"
	"github.com/Sahaj-Tech-ltd/ethos/internal/hooks"
	"github.com/Sahaj-Tech-ltd/ethos/internal/journal"
	"github.com/Sahaj-Tech-ltd/ethos/internal/lsp"
	"github.com/Sahaj-Tech-ltd/ethos/internal/mcp"
	"github.com/Sahaj-Tech-ltd/ethos/internal/plugin"
	"github.com/Sahaj-Tech-ltd/ethos/internal/routing"
	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
	"github.com/Sahaj-Tech-ltd/ethos/internal/skills"
	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
	syncpkg "github.com/Sahaj-Tech-ltd/ethos/internal/sync"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tags"
	toolspkg "github.com/Sahaj-Tech-ltd/ethos/internal/tools"
	"github.com/Sahaj-Tech-ltd/ethos/internal/workspace"
)

// AgentBuilder rebuilds an agent from a fresh config. Supplied by the CLI
// command that owns the wiring (provider factories, tool registry, etc.) so
// the TUI can hot-swap configuration without restart.
type AgentBuilder func(cfg *config.Config) (*agent.Agent, error)

type App struct {
	Agent    *agent.Agent
	Store    session.Store
	Router   routing.Router
	Costs    cost.Tracker
	Hooks    *hooks.Registry
	Config   *config.Config
	Journal  *journal.FlightRecorder
	Subagent *subagent.Manager
	MCP      *mcp.Manager
	LSP      *lsp.Manager
	Plugins  *plugin.Manager
	Browser  *browser.Manager

	// Phase-3 polish features. Each is nil-safe — TUI checks before use.
	Tags      *tags.Manager
	Workspace *workspace.Manager
	Skills    []skills.Skill

	// Build, when set, is used by Reconfigure to rebuild the agent after the
	// user re-runs setup from inside the TUI. CLI wires this up.
	Build AgentBuilder

	// Sync manager (multi-machine session sync). Nil when sync is disabled.
	Sync *syncpkg.Manager

	// ConfigPath is the resolved on-disk path to the active config.toml. Used
	// by dialogs that want to persist user toggles (skills, plugins) without
	// re-running the full setup flow.
	ConfigPath string

	// Tools is the live tool registry. Exposed so dialogs (e.g. /mcp) can
	// rescan and register new tools that came online after startup.
	Tools *toolspkg.Registry

	// ACPServer hosts the inbound HTTP/SSE surface for other agents. Nil
	// when ACP is disabled.
	ACPServer *acp.Server
}

// Reconfigure swaps in a new config and rebuilds the agent. Returns the new
// agent (which is also assigned to App.Agent) so the caller can refresh any
// page that holds an *agent.Agent reference.
func (a *App) Reconfigure(cfg *config.Config) (*agent.Agent, error) {
	if a == nil {
		return nil, nil
	}
	a.Config = cfg
	if a.Build == nil {
		return a.Agent, nil
	}
	ag, err := a.Build(cfg)
	if err != nil {
		return nil, err
	}
	a.Agent = ag
	return ag, nil
}
