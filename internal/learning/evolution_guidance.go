package learning

import (
	"context"
	"fmt"
)

const MaxAppliedGuidanceBytes = 32 * 1024
const MaxAppliedGuidanceItems = 8

// AppliedGuidance is verified, explicitly installed project guidance. Drafts in
// any other state, stale content, or mismatched workspaces never become context.
type AppliedGuidance struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	AppliedHash string `json:"appliedHash"`
}

func (e *Engine) LoadAppliedGuidance(ctx context.Context) ([]AppliedGuidance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	unlock, err := e.operationLock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if WorkspaceScope(e.options.Workspace) == "" {
		return nil, fmt.Errorf("learning: explicit workspace required for guidance")
	}
	drafts, err := e.ListDrafts(ctx)
	if err != nil {
		return nil, err
	}
	var guidance []AppliedGuidance
	total := 0
	// Prefer the most recently reviewed installed versions when the bound is hit.
	for i := len(drafts) - 1; i >= 0 && len(guidance) < MaxAppliedGuidanceItems; i-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		draft := drafts[i]
		if draft.Status != DraftApplied || draft.ReviewedAt.IsZero() || draft.AppliedAt.IsZero() || draft.AppliedHash == "" {
			continue
		}
		body, exists, err := e.currentSkill(draft.TargetSkillName)
		if err != nil || !exists || hashSkill(body, true) != draft.AppliedHash {
			continue
		}
		if len(body)+total > MaxAppliedGuidanceBytes || len(reviewDraftContent(string(body), e.options.KnownSecrets)) > 0 {
			continue
		}
		guidance = append(guidance, AppliedGuidance{ID: draft.ID, Name: draft.TargetSkillName, Body: string(body), AppliedHash: draft.AppliedHash})
		total += len(body)
	}
	return guidance, nil
}
