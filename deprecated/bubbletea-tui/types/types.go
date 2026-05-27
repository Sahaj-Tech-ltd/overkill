package types

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// ModelCatalogLoadedMsg is emitted when a live/cache/baked catalog has been
// loaded, ready for the model picker dialog to consume.
type ModelCatalogLoadedMsg struct {
	Catalog *providers.Catalog
	Source  string // "live" | "cache" | "baked"
	Err     error
}

type SendMsg struct {
	Text        string
	Attachments []Attachment
}

type Attachment struct {
	Name    string
	Content []byte
	Type    string
}

type AgentResponseMsg struct {
	Content string
	Done    bool
	Err     error
}

type AgentStreamMsg struct {
	Chunk      string
	Tokens     int
	ToolName   string
	ToolOutput string
	Done       bool
	Err        error
	// MetadataLine, when non-empty, is a single-line summary rendered
	// inline under a tool call — e.g. "✓ exit 0 · 0.3s · ~/repo" for
	// shell. Phase 1.5 #8. Pump fills this from EventToolOutput
	// metadata; the chat reducer appends it as a "tool" message.
	MetadataLine string
}

type SessionLoadedMsg struct {
	Session *session.Session
}

type CostUpdateMsg struct {
	TotalCost    float64
	InputTokens  int64
	OutputTokens int64
	ContextPct   float64
}

type FilesChangedMsg struct {
	Files []FileChange
}

type FileChange struct {
	Path    string
	Added   int
	Deleted int
	Status  string
}

type PersonalityStateMsg struct {
	Relationship  int
	FunFact       string
	SoulMDExcerpt string
	Mode          string
}

type BridgeStatusMsg struct {
	Connected bool
	Err       error
}

type SessionListMsg struct {
	Sessions []*session.Session
}

type ModelSelectedMsg struct {
	ModelID  string
	Provider string
}

// ToastMsg is a transient on-screen notification.
type ToastMsg struct {
	Text string
	Kind string // info|success|warning|error
}

// PermissionRequestMsg asks the TUI to display the permission dialog and
// returns the user's decision via Reply (must be buffered so the agent
// goroutine can deliver synchronously without deadlock).
type PermissionRequestMsg struct {
	ToolName string
	Args     string
	Risk     string
	Reply    chan<- PermissionReply
}

// PermissionReply is sent back from the dialog to the waiting agent goroutine.
type PermissionReply struct {
	Allow   bool
	Persist bool
}

// QuestionRequestMsg asks the TUI to display the question dialog. The user's
// response flows back via Reply (must be buffered).
type QuestionRequestMsg struct {
	Prompt  string
	Choices []string
	Reply   chan<- QuestionReply
}

// QuestionReply is sent back from the dialog to the waiting agent goroutine.
type QuestionReply struct {
	Text   string
	Index  int
	Cancel bool
}

// SubagentTickMsg is emitted on a periodic tick to refresh the subagent footer.
type SubagentTickMsg struct{}

// SidebarRefreshMsg drives periodic sidebar (files, cost, sessions) refresh.
type SidebarRefreshMsg struct{}

type StatusState int

const (
	StatusIdle StatusState = iota
	StatusThinking
	StatusGenerating
	StatusToolCall
)
