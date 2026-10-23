// Package coordination provides durable, project-scoped correspondence between
// agents. Credentials identify senders; message payloads never choose identity.
package coordination

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrRequestContracts = errors.New("coordination: required contract agreement unavailable")
	ErrUnauthorized     = errors.New("coordination: unauthorized peer")
	ErrNotFound         = errors.New("coordination: not found")
	ErrConflict         = errors.New("coordination: revision conflict")
	ErrIdempotency      = errors.New("coordination: idempotency key reused for a different request")
	ErrBusy             = errors.New("coordination: peer has active work")
	ErrLeaseLost        = errors.New("coordination: worker lease lost")
	ErrRecoveryRequired = errors.New("coordination: interrupted work needs explicit recovery")
)

type Peer struct {
	ID           string   `json:"id"`
	ProjectID    string   `json:"project_id"`
	Name         string   `json:"name"`
	Role         string   `json:"role"`
	Workspace    string   `json:"workspace"`
	Endpoint     string   `json:"endpoint"`
	Capabilities []string `json:"capabilities"`
	Enabled      bool     `json:"enabled"`
}

type State string

const (
	Accepted  State = "accepted"
	Started   State = "started"
	Blocked   State = "blocked"
	Completed State = "completed"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

func (s State) Terminal() bool { return s == Completed || s == Failed || s == Cancelled }

type SendInput struct {
	RequiredContracts []ContractDependency `json:"required_contracts,omitempty"`
	DependsOn         []string             `json:"depends_on,omitempty"`
	To                string               `json:"to"`
	Kind              string               `json:"kind"`
	ConversationID    string               `json:"conversation_id,omitempty"`
	TaskID            string               `json:"task_id,omitempty"`
	InReplyTo         string               `json:"in_reply_to,omitempty"`
	Payload           json.RawMessage      `json:"payload"`
	Deadline          time.Time            `json:"deadline,omitempty"`
	IdempotencyKey    string               `json:"idempotency_key"`
}

type Message struct {
	RequiredContracts []ContractDependency `json:"required_contracts,omitempty"`
	DependsOn         []string             `json:"depends_on,omitempty"`
	ID                string               `json:"id"`
	ProjectID         string               `json:"project_id"`
	From              string               `json:"from"`
	To                string               `json:"to"`
	Kind              string               `json:"kind"`
	ConversationID    string               `json:"conversation_id"`
	TaskID            string               `json:"task_id,omitempty"`
	InReplyTo         string               `json:"in_reply_to,omitempty"`
	Payload           json.RawMessage      `json:"payload"`
	State             State                `json:"state"`
	Revision          int64                `json:"revision"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
	Deadline          time.Time            `json:"deadline"`
	ParentMessageID   string               `json:"parent_message_id,omitempty"`
	RequestDepth      int                  `json:"request_depth"`
	ExecutionAttempts int                  `json:"execution_attempts"`
}

type Event struct {
	ProjectID string          `json:"project_id"`
	Sequence  int64           `json:"sequence"`
	MessageID string          `json:"message_id"`
	Revision  int64           `json:"revision"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}
