package journal

import (
	"encoding/json"
	"time"
)

type EntryType string

const (
	EntryUserInput  EntryType = "user_input"
	EntryAgentReply EntryType = "agent_reply"
	EntryToolCall   EntryType = "tool_call"
	EntryToolResult EntryType = "tool_result"
	EntryError      EntryType = "error"
	EntrySystem     EntryType = "system"
)

type Entry struct {
	ID        string          `json:"id"`
	Type      EntryType       `json:"type"`
	SessionID string          `json:"session_id"`
	Timestamp time.Time       `json:"timestamp"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type AlertType string

const (
	AlertCompactionSkip  AlertType = "compaction_skip"
	AlertTaskDeferred    AlertType = "task_deferred"
	AlertPatternDetected AlertType = "pattern_detected"
	AlertFrustration     AlertType = "frustration_signal"
)

type Alert struct {
	ID           string    `json:"id"`
	Type         AlertType `json:"type"`
	Message      string    `json:"message"`
	SessionID    string    `json:"session_id"`
	Timestamp    time.Time `json:"timestamp"`
	Acknowledged bool      `json:"acknowledged"`
}
