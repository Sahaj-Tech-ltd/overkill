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

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
)

// AgentSender is the minimal slice of *agent.Agent that gateways call
// into. Kept tiny so the package never imports the cmd layer.
type AgentSender interface {
	Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error)
	SetSessionID(id string)
	SessionID() string
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
}

// Channel is one running gateway. Run blocks until ctx is cancelled.
// Name is used for logs and as the Inbound.Channel value.
type Channel interface {
	Name() string
	Run(ctx context.Context) error
}
