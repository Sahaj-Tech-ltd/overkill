// Package tools — learn_record surfaces skills.LearnTrigger so the agent
// (or the user via /learn) can mark a problem class as "solved again",
// triggering a save-as-skill suggestion once the threshold is hit
// (master plan §6.2).
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
)

// LearnRecorder is the minimal surface this tool needs from a LearnTrigger.
type LearnRecorder interface {
	RecordSuccess(class string) bool
	Snapshot() map[string]int
}

// LearnRecordTool wraps a LearnRecorder.
type LearnRecordTool struct {
	rec LearnRecorder
}

// NewLearnRecordTool wraps the trigger. Pass a *skills.LearnTrigger.
func NewLearnRecordTool(rec LearnRecorder) *LearnRecordTool {
	return &LearnRecordTool{rec: rec}
}

func (t *LearnRecordTool) Name() string { return "learn_record" }

type learnRecordInput struct {
	Class string `json:"class"`
}

func (t *LearnRecordTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.rec == nil {
		return errorJSON("learn trigger not configured"), nil
	}
	var req learnRecordInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("learn_record: %w", err)
	}
	if req.Class == "" {
		return errorJSON("class is required"), nil
	}
	suggested := t.rec.RecordSuccess(req.Class)
	out, err := json.Marshal(map[string]any{
		"class":     req.Class,
		"suggested": suggested,
		"counts":    t.rec.Snapshot(),
	})
	if err != nil {
		return nil, fmt.Errorf("learn_record: marshal: %w", err)
	}
	return out, nil
}

// Compile-time interface check — *skills.LearnTrigger must satisfy
// LearnRecorder.
var _ LearnRecorder = (*skills.LearnTrigger)(nil)
