// orphan: vertical slicer (master plan §5.4); needs /slice slash command
package pipeline

import (
	"context"
	"encoding/json"
	"time"
)

type Stage int

const (
	StageSpec      Stage = iota
	StageTest      Stage = iota
	StageCode      Stage = iota
	StageRefactor  Stage = iota
)

func (s Stage) String() string {
	switch s {
	case StageSpec:
		return "spec"
	case StageTest:
		return "test"
	case StageCode:
		return "code"
	case StageRefactor:
		return "refactor"
	default:
		return "unknown"
	}
}

func (s Stage) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

type StageResult struct {
	Stage    Stage            `json:"stage"`
	Content  string           `json:"content"`
	Files    map[string]string `json:"files,omitempty"`
	Passed   bool             `json:"passed"`
	Errors   []string         `json:"errors,omitempty"`
	Duration time.Duration    `json:"duration"`
}

type PipelineResult struct {
	Stages     []StageResult     `json:"stages"`
	TotalTime  time.Duration     `json:"total_time"`
	Success    bool              `json:"success"`
	FinalFiles map[string]string `json:"final_files,omitempty"`
}

type Pipeline interface {
	Run(ctx context.Context, request string) (*PipelineResult, error)
	RunStage(ctx context.Context, stage Stage, input string) (*StageResult, error)
}
