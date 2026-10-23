package acp

import (
	"errors"
	"time"
)

const (
	TaskMaxContentBytes  = 64 * 1024
	TaskMaxReplyBytes    = 256 * 1024
	TaskMaxErrorBytes    = 4096
	TaskMaxProgressBytes = 4096
	TaskMaxActive        = 256
	TaskConcurrency      = 4
	TaskRunTimeout       = 15 * time.Minute
	TaskMaxAwait         = 25 * time.Second
)

var (
	ErrTaskInvalid     = errors.New("invalid task request")
	ErrTaskConflict    = errors.New("task idempotency conflict")
	ErrTaskNotFound    = errors.New("task not found")
	ErrTaskCapacity    = errors.New("task active capacity reached")
	ErrTaskUnavailable = errors.New("task store unavailable")
)

type TaskSubmitRequest struct {
	From           string `json:"from,omitempty"`
	Content        string `json:"content"`
	SessionID      string `json:"sessionID"`
	IdempotencyKey string `json:"idempotencyKey"`
}
type Task struct {
	WorkerError string    `json:"workerError,omitempty"`
	TaskID      string    `json:"taskID"`
	MessageID   string    `json:"messageID"`
	SessionID   string    `json:"sessionID"`
	Status      string    `json:"status"`
	Reply       string    `json:"reply,omitempty"`
	Error       string    `json:"error,omitempty"`
	Progress    string    `json:"progress,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (t Task) Terminal() bool {
	switch t.Status {
	case "completed", "failed", "cancelled", "interrupted":
		return true
	}
	return false
}
