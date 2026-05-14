package tui

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/browser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/lsp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/mcp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plugin"
	"github.com/Sahaj-Tech-ltd/overkill/internal/routing"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
	toolspkg "github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/workspace"
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
	Alerts   *journal.AlertStore
	Subagent *subagent.Manager
	MCP      *mcp.Manager
	LSP      *lsp.Manager
	Plugins  *plugin.Manager
	Browser        *browser.Manager
	Checkpoints    *checkpoint.Manager
	StandingOrders *automation.OrdersFile
	Learn          *skills.LearnTrigger

	// Phase-3 polish features. Each is nil-safe — TUI checks before use.
	Tags      *tags.Manager
	Workspace *workspace.Manager
	Skills    []skills.Skill

	// Frustration is the live detector wired to the agent's UserInputObserver.
	// Exposed so the TUI's personality provider can read short-term state for
	// tone mirroring (§4.16). Nil-safe.
	Frustration *personality.FrustrationDetector

	// Transparency surfaces a single rate-limited heads-up when the active
	// model has repeatedly failed at the current task type (§4.16). Nil-safe.
	Transparency *personality.TransparencyEngine

	// BlindSpot surfaces a single gentle one-liner when the user has
	// hammered the same verb past the detector's threshold (§4.16).
	// Nil-safe.
	BlindSpot *personality.BlindSpotDetector

	// Relationship is the persisted §6.3 milestone tracker. Loaded
	// from ~/.overkill/memories/relationship-arc.json on boot and
	// saved on clean exit. Shared between the agent's beat
	// firing and the TUI's tone-mirror surfaces.
	Relationship *personality.RelationshipTracker

	// Style is the §4.16 two-layer style inferencer. Short-term flips
	// per turn; baseline only drifts after 5 consecutive sessions of
	// the same dominant pattern. Nil-safe.
	Style *personality.StyleInferencer

	// StylePath is where Style is persisted on session-end so the
	// streak counter survives across boots.
	StylePath string

	// StoreProbe carries the §4.20 BadgerDB integrity check from
	// boot. When Corrupt is true the TUI surfaces a restore prompt
	// instead of cold-starting; when false the field is zero-value
	// and consumers no-op.
	StoreProbe session.ProbeResult

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

	// Plan is the per-session plan store backing the right-pane Plan
	// panel. The agent mutates it via plan_set / plan_check tools;
	// the sidebar reads it for rendering. Nil-safe.
	Plan *plan.Store

	// Learnings is the append-only end-of-task lesson stream
	// (§6.2 prose layer). Tools record_learning + learnings_search
	// read/write it. Nil-safe.
	Learnings *plan.LearningsStore
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
