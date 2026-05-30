// Package gateway is the shared abstraction behind every remote messaging
// channel — Telegram, Discord, WhatsApp, and arbitrary sidecars via the
// HTTP bridge. The agent runs in the TUI; gateways pipe inbound messages
// through to it and stream replies back, so a user can step away from
// their terminal and keep driving the same session from their phone.
//
// Slack predates this package and keeps its own session map for
// historical reasons. New channels live here.
package gateway

import (
	"context"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
)

// AgentSender is the minimal slice of *agent.Agent that gateways call
// into. Kept tiny so the package never imports the cmd layer.
type AgentSender interface {
	Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error)
	SetSessionID(id string)
	SessionID() string
	EStop()
	// Interrupt cancels the currently running stream for this agent.
	// Safe to call from any goroutine. No-op if no stream is running.
	Interrupt()
	// SetQuestionFunc wires the clarify callback so the agent can ask
	// the user questions mid-execution (§8.6).
	SetQuestionFunc(fn agent.QuestionFunc)
	// Undo removes the last exchange from session history.
	Undo() (string, error)
	// Retry replays the last user message with a fresh model call.
	Retry() (string, error)
	// Steer queues a guidance message for mid-run injection into the
	// agent loop. Returns a confirmation string.
	Steer(msg string) string
	// Fork creates a new session branched from the current one with
	// an optional name. Returns the new session ID.
	Fork(name string) (string, error)
	// Snapshot creates a git-based filesystem checkpoint with the
	// given name. Returns the new commit hash.
	Snapshot(name string) (string, error)
	// Rollback rolls back to the Nth most recent checkpoint
	// (0 = latest). Returns a human-readable summary.
	Rollback(n int) (string, error)
	// Snapshots returns a human-readable listing of saved checkpoints.
	Snapshots() (string, error)
	// SetGoal sets or updates the standing goal for the current session.
	SetGoal(ctx context.Context, text string) error
	// GetGoal returns the current goal text (empty string if none set).
	GetGoal(ctx context.Context) (string, error)
	// PauseGoal pauses the current goal (keeps it but stops injecting).
	PauseGoal(ctx context.Context) error
	// ResumeGoal resumes a paused goal so it injects again.
	ResumeGoal(ctx context.Context) error
	// ClearGoal removes the goal entirely.
	ClearGoal(ctx context.Context) error
	// Compact triggers context compaction on the current session.
	// Returns the compaction result with before/after token counts and a summary.
	Compact(ctx context.Context) (*agent.CompactResult, error)
	// ExportHistory writes the current session's message history to a
	// markdown file at the given path and returns the path.
	ExportHistory(path string) (string, error)
	// SetThinkingLevel sets the extended thinking budget.
	SetThinkingLevel(level string)
	// ThinkingLevel returns the current thinking level.
	ThinkingLevel() string
	// Mode returns the current plan/build mode ("plan" or "build").
	// Plan mode means analysis-only; build mode allows execution.
	Mode() string
	// IsBusy returns true when the agent loop is actively executing Run().
	// Used by harness to guard commands that would interrupt a running agent.
	IsBusy() bool
}

// Inbound is one user-authored message arriving from any channel.
// ChatKey is the gateway-stable identifier for the conversation
// (e.g. "telegram:12345" or "bridge:whatsapp:+15551234"). Thread is
// optional — channels with no thread concept leave it empty.
type Inbound struct {
	Channel  string // "telegram", "discord", "whatsapp", "bridge:<name>"
	ChatKey  string
	Thread   string
	From     string // display name or user id, for logging only
	Text     string
	Images   []InboundImage // attached photos; describer turns into prose
	IsDirect bool           // DM/private chat vs group
}

// InboundImage is one attached image. Mime is best-effort; describers
// sniff bytes if it's empty.
type InboundImage struct {
	Bytes []byte
	Mime  string
}

// Reply is the surface a Channel exposes to the dispatcher to render an
// agent reply. PostInitial returns an opaque handle the channel uses to
// route Update / Final / Error back to the right message.
//
// Channels that can't edit messages in place (SMS, email) MUST still
// implement Update by no-op'ing — Final is what users actually see.
type Reply interface {
	PostInitial(ctx context.Context, in Inbound, text string) (handle string, err error)
	Update(ctx context.Context, handle, text string) error
	Final(ctx context.Context, handle, text string) error
	Error(ctx context.Context, handle string, err error) error
	// StartTyping begins the native typing indicator if the channel supports it.
	// Returns a stop function that clears the indicator. Channels without
	// native typing support return a no-op stop function.
	StartTyping() (stop func())
}

// Channel is one running gateway. Run blocks until ctx is cancelled.
// Name is used for logs and as the Inbound.Channel value.
type Channel interface {
	Name() string
	Run(ctx context.Context) error
}

// HealthChecker is implemented by gateways that can report their
// current health. A healthy gateway can send and receive messages.
type HealthChecker interface {
	Healthy() bool
}

// Reconnecter is implemented by gateways that support explicit
// reconnection with backoff. Run handles automatic reconnection;
// Reconnect is the public hook for callers to trigger a reconnect
// externally (e.g. from a health monitor).
type Reconnecter interface {
	Reconnect(ctx context.Context) error
}
