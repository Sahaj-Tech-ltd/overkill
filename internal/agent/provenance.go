// Package agent's provenance.go tags every message flowing into the agent
// loop with its origin. When messages flow between sessions (sub-agent →
// parent, cron → main agent), the receiving agent can distinguish "the user
// said this" from "another agent computed this." This is a security boundary.
//
// Stolen from OpenClaw's src/sessions/input-provenance.ts.
package agent

import "encoding/json"

// ProvenanceKind categorizes where a message came from.
type ProvenanceKind string

const (
	// ProvenanceUser means the message came directly from a human user
	// through a messaging channel or the TUI.
	ProvenanceUser ProvenanceKind = "user"

	// ProvenanceSubAgent means the message was produced by a sub-agent
	// spawned by Overkill. The agent should verify claims from sub-agents
	// rather than accepting them at face value.
	ProvenanceSubAgent ProvenanceKind = "sub_agent"

	// ProvenanceCron means the message was triggered by a cron job.
	ProvenanceCron ProvenanceKind = "cron"

	// ProvenanceSystem means the message was generated internally (e.g.
	// session compaction summary, journal alert, auto-compaction
	// checkpoint). These are informational and should not drive action.
	ProvenanceSystem ProvenanceKind = "system"
)

// Provenance tags a user message with its origin metadata. Every message
// that enters the agent loop carries one of these. The agent's system
// prompt includes provenance-aware instructions.
type Provenance struct {
	// Kind is the origin category.
	Kind ProvenanceKind `json:"kind"`

	// OriginSessionID is the session that produced this message (when
	// Kind is sub_agent or cron).
	OriginSessionID string `json:"originSessionId,omitempty"`

	// SourceChannel is the messaging channel this came through (when
	// Kind is user). E.g. "telegram", "discord", "tui".
	SourceChannel string `json:"sourceChannel,omitempty"`

	// SourceTool is the tool that produced this message (when Kind is
	// sub_agent or system). E.g. "code_review", "journal_summarize".
	SourceTool string `json:"sourceTool,omitempty"`
}

// Prefix returns the provenance tag that should be prepended to the
// message before it enters the agent's context. User messages get an
// empty prefix (no noise). Inter-session messages get a bracketed tag
// so the agent knows to adjust trust.
func (p Provenance) Prefix() string {
	switch p.Kind {
	case ProvenanceUser:
		return ""
	case ProvenanceSubAgent:
		return "[Inter-session: sub-agent] "
	case ProvenanceCron:
		return "[System: cron] "
	case ProvenanceSystem:
		return "[System] "
	default:
		return ""
	}
}

// MarshalJSON implements json.Marshaler.
func (p Provenance) MarshalJSON() ([]byte, error) {
	type alias Provenance
	return json.Marshal((*alias)(&p))
}

// UnmarshalJSON implements json.Unmarshaler.
func (p *Provenance) UnmarshalJSON(data []byte) error {
	type alias Provenance
	return json.Unmarshal(data, (*alias)(p))
}
