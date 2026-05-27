// Package tools — slice_decompose exposes the vertical-slice
// decomposition pipeline (master plan §4.11) to the agent.
//
// Given a free-text spec, the slicer:
//  1. Decomposes into tracer-bullet issues cutting through ALL layers
//     (schema → API → UI → tests), each independently demoable.
//  2. Classifies each slice HITL (needs human) vs AFK (agent-mergeable).
//  3. Topologically sorts so dependency references use real IDs.
//
// The agent calls this when the user asks "break this down" or
// "scaffold tickets for this feature". Pure function — no LLM call.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/pipeline"
)

// SliceDecomposeTool wraps pipeline.DecomposeIntoSlices +
// pipeline.TopologicalSort behind the agent tool interface.
type SliceDecomposeTool struct{}

func NewSliceDecomposeTool() *SliceDecomposeTool { return &SliceDecomposeTool{} }

func (t *SliceDecomposeTool) Name() string { return "slice_decompose" }

type sliceDecomposeInput struct {
	// Spec is the free-text feature description / scope statement to
	// break into slices. Required.
	Spec string `json:"spec"`
	// SortTopologically toggles the dependency-aware sort. Default
	// true — most callers want issues created in build order.
	SortTopologically *bool `json:"sort_topologically,omitempty"`
}

type sliceDecomposeOutput struct {
	Count  int              `json:"count"`
	Slices []pipeline.Slice `json:"slices"`
}

func (t *SliceDecomposeTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	var req sliceDecomposeInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("slice_decompose: %w", err)
	}
	if req.Spec == "" {
		return errorJSON("spec is required (feature description to decompose)"), nil
	}

	slices, err := pipeline.DecomposeIntoSlices(req.Spec)
	if err != nil {
		return errorJSON(err.Error()), nil
	}

	sortIt := true
	if req.SortTopologically != nil {
		sortIt = *req.SortTopologically
	}
	if sortIt && len(slices) > 1 {
		sorted, serr := pipeline.TopologicalSort(slices)
		if serr != nil {
			// Cycle in declared dependencies — surface as a tool
			// error but include the unsorted list so the model has
			// something to work with.
			body, _ := json.Marshal(struct {
				Error  string           `json:"error"`
				Count  int              `json:"count"`
				Slices []pipeline.Slice `json:"slices"`
			}{
				Error:  fmt.Sprintf("topological sort failed: %v; returning unsorted slices", serr),
				Count:  len(slices),
				Slices: slices,
			})
			return body, nil
		}
		slices = sorted
	}

	body, _ := json.Marshal(sliceDecomposeOutput{
		Count:  len(slices),
		Slices: slices,
	})
	return body, nil
}
