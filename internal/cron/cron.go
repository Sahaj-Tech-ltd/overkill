// orphan: scheduled-task runner (master plan §5.5); awaiting /cron slash command + 'overkill cron run' daemon mode
package cron

import (
	"errors"
	"time"
)

type ExecutionStyle string

const (
	StyleMain     ExecutionStyle = "main"
	StyleIsolated ExecutionStyle = "isolated"
	StyleCurrent  ExecutionStyle = "current"
	StyleSession  ExecutionStyle = "session"
)

type JobStatus string

const (
	StatusActive    JobStatus = "active"
	StatusPaused    JobStatus = "paused"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
)

type Job struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Schedule       string            `json:"schedule"`
	Timezone       string            `json:"timezone"`
	ExecutionStyle ExecutionStyle    `json:"execution_style"`
	SessionID      string            `json:"session_id,omitempty"`
	Command        string            `json:"command"`
	Status         JobStatus         `json:"status"`
	LastRun        time.Time         `json:"last_run"`
	NextRun        time.Time         `json:"next_run"`
	RunCount       int               `json:"run_count"`
	FailureCount   int               `json:"failure_count"`
	MaxRetries     int               `json:"max_retries"`
	CreatedAt      time.Time         `json:"created_at"`
	Metadata       map[string]string `json:"metadata"`
}

var (
	ErrJobNotFound = errors.New("cron: job not found")
	ErrJobExists   = errors.New("cron: job already exists")
	ErrInvalidJob  = errors.New("cron: invalid job")
)
