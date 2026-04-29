package types

import (
	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
)

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
	Chunk  string
	Tokens int
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
	ModelID string
}

type StatusState int

const (
	StatusIdle StatusState = iota
	StatusThinking
	StatusGenerating
	StatusToolCall
)
