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
	AlertCompactionSkip   AlertType = "compaction_skip"
	AlertTaskDeferred     AlertType = "task_deferred"
	AlertPatternDetected  AlertType = "pattern_detected"
	AlertFrustration      AlertType = "frustration_signal"
	AlertDelegationFailed AlertType = "delegation_failure"
	// AlertMemoryCorruption fires when a DB open or integrity
	// check fails (§4.20). The TUI surfaces this with a restore
	// prompt: "Memory corrupted. I knew I knew you. I don't know what
	// I knew. Last export was 3 days ago — want me to restore from
	// that?"
	AlertMemoryCorruption AlertType = "memory_corruption"

	// AlertTaskCompleted fires from the ledger when a long-running
	// background task transitions to a terminal state (Completed /
	// Failed / Cancelled / Lost / TimedOut). §7.1 Layer 6: push
	// notification on completion. The gateway hub reads these out
	// of the alert store and delivers to the user's active channels.
	AlertTaskCompleted AlertType = "task_completed"
)

type Alert struct {
	ID           string    `json:"id"`
	Type         AlertType `json:"type"`
	Message      string    `json:"message"`
	SessionID    string    `json:"session_id"`
	Timestamp    time.Time `json:"timestamp"`
	Acknowledged bool      `json:"acknowledged"`
}
