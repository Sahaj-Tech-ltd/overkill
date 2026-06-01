// Package automation — SOP engine, routines, alarm clocks, standing orders,
// background task ledger. Wired via daemon.go; Postgres-backed.
package automation

import (
	"errors"
	"time"
)

type SOPMode int

const (
	ModeAuto          SOPMode = iota
	ModeSupervised    SOPMode = iota
	ModeStepByStep    SOPMode = iota
	ModePriority      SOPMode = iota
	ModeDeterministic SOPMode = iota
)

type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
	StepSkipped StepStatus = "skipped"
	StepWaiting StepStatus = "waiting"
)

type SOPStatus string

const (
	SOPStatusActive    SOPStatus = "active"
	SOPStatusRunning   SOPStatus = "running"
	SOPStatusPaused    SOPStatus = "paused"
	SOPStatusCompleted SOPStatus = "completed"
	SOPStatusFailed    SOPStatus = "failed"
	SOPStatusCancelled SOPStatus = "cancelled"
)

type Step struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	Action           string        `json:"action"`
	Priority         int           `json:"priority"`
	Status           StepStatus    `json:"status"`
	Output           string        `json:"output"`
	Error            string        `json:"error"`
	RequiresApproval bool          `json:"requires_approval"`
	Timeout          time.Duration `json:"timeout"`
}

var (
	ErrNotFound      = errors.New("automation: not found")
	ErrAlreadyExists = errors.New("automation: already exists")
	ErrInvalidState  = errors.New("automation: invalid state")
	ErrNoSteps       = errors.New("automation: no steps")
	ErrStepWaiting   = errors.New("automation: step waiting for approval")
)
