// Package tools — wall_architecture and wall_ouroboros tools surface
// walls.ArchitectureWall and walls.OuroborosWall to the agent so the model
// can self-check before declaring a change done (master plan §6.5 walls 1
// and 2). Wall 3 (regression bank) lives in regression.go.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
)

// ArchitectureWallTool runs the rule-based architecture wall over a set
// of changed files. Cheap — no LLM call. Designed to be invoked by the
// agent before committing a patch.
type ArchitectureWallTool struct {
	wall *walls.ArchitectureWall
}

func NewArchitectureWallTool(w *walls.ArchitectureWall) *ArchitectureWallTool {
	return &ArchitectureWallTool{wall: w}
}

func (t *ArchitectureWallTool) Name() string { return "wall_architecture" }

type wallArchInput struct {
	// Files maps relative-path → file content. The agent supplies only
	// the files it just modified; the wall checks them against the loaded
	// arch rules. Empty input is a no-op success.
	Files map[string]string `json:"files"`
}

func (t *ArchitectureWallTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.wall == nil {
		return errorJSON("architecture wall not configured"), nil
	}
	var req wallArchInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("wall_architecture: %w", err)
	}
	result, err := t.wall.Check(ctx, req.Files)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(result)
	return out, nil
}

// OuroborosWallTool runs the LLM-based adversarial code review. EXPENSIVE
// — every invocation costs a provider call against the configured review
// model. The agent should only call this when the user explicitly asks
// for a review, or before a destructive merge.
//
// The wall needs its own provider (a separate LLM instance from the main
// agent — the §6.5 spec: "uses a different model so it isn't reviewing
// itself"). When the wall is constructed without a provider, the tool
// returns a configuration error so the agent surfaces the gap to the user
// instead of pretending it ran.
type OuroborosWallTool struct {
	wall *walls.OuroborosWall
}

func NewOuroborosWallTool(w *walls.OuroborosWall) *OuroborosWallTool {
	return &OuroborosWallTool{wall: w}
}

func (t *OuroborosWallTool) Name() string { return "wall_ouroboros" }

type wallOuroborosInput struct {
	// Code is the implementation under review. Spec is what the code is
	// SUPPOSED to do — the wall scores the gap between the two.
	Code string `json:"code"`
	Spec string `json:"spec"`
}

func (t *OuroborosWallTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.wall == nil {
		return errorJSON("ouroboros wall not configured (needs a separate review provider — see §6.5)"), nil
	}
	var req wallOuroborosInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("wall_ouroboros: %w", err)
	}
	if req.Code == "" {
		return errorJSON("code is required"), nil
	}
	if req.Spec == "" {
		return errorJSON("spec is required (the wall needs to know what the code SHOULD do to evaluate the gap)"), nil
	}
	result, err := t.wall.Check(ctx, req.Code, req.Spec)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(result)
	return out, nil
}
