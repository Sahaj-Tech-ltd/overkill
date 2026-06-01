// Package tools — diagnose_next_tier surfaces the diagnostic ladder
// (master plan §4.13) so the agent, when a fix attempt fails, can ask for
// the next-deeper verification step rather than guessing.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/diagnostic"
)

// DiagnoseNextTierTool returns the next ladder tier given a current one.
type DiagnoseNextTierTool struct{}

func NewDiagnoseNextTierTool() *DiagnoseNextTierTool { return &DiagnoseNextTierTool{} }

func (t *DiagnoseNextTierTool) Name() string { return "diagnose_next_tier" }

type diagnoseNextInput struct {
	CurrentTier string `json:"current_tier,omitempty"` // "compile" | "unit-test" | ...
	Language    string `json:"language,omitempty"`
}

type diagnoseNextOutput struct {
	Tier             string `json:"tier"`
	Description      string `json:"description"`
	SuggestedCommand string `json:"suggested_command,omitempty"`
	Exhausted        bool   `json:"exhausted"`
}

func (t *DiagnoseNextTierTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var req diagnoseNextInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("diagnose_next_tier: %w", err)
	}

	var ladder *diagnostic.Ladder
	if req.CurrentTier == "" {
		ladder = diagnostic.NewLadder()
	} else {
		tier := tierFromName(req.CurrentTier)
		if tier == 0 {
			return errorJSON(fmt.Sprintf("unknown current_tier %q", req.CurrentTier)), nil
		}
		ladder = diagnostic.FromTier(tier)
		// Climb past the current tier to get the next one.
		_, _ = ladder.Climb()
	}

	current := ladder.Current()
	exhausted := false
	if req.CurrentTier == diagnostic.TierHITLBash.Name() {
		exhausted = true
	}

	out := diagnoseNextOutput{
		Tier:             current.Name(),
		Description:      current.Description(),
		SuggestedCommand: current.SuggestedCommand(req.Language),
		Exhausted:        exhausted,
	}
	raw, _ := json.Marshal(out)
	return raw, nil
}

func tierFromName(name string) diagnostic.Tier {
	for _, t := range diagnostic.AllTiers() {
		if t.Name() == name {
			return t
		}
	}
	return 0
}
