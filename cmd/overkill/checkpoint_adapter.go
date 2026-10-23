package main

import (
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
)

// Adapter keeps filesystem recovery and its published scope in the shared
// session runtime. Empty paths capture the selected workspace inventory.
type checkpointSnapshotterAdapter struct{ mgr *checkpoint.Manager }

var _ agent.CheckpointSnapshotter = (*checkpointSnapshotterAdapter)(nil)

func (a *checkpointSnapshotterAdapter) Snapshot(sessionID, reason string, paths []string) error {
	_, err := a.SnapshotReceipt(sessionID, reason, paths)
	return err
}
func (a *checkpointSnapshotterAdapter) SnapshotReceipt(sessionID, reason string, paths []string) (agent.CheckpointReceipt, error) {
	if a == nil || a.mgr == nil {
		return agent.CheckpointReceipt{}, fmt.Errorf("checkpoint recovery unavailable")
	}
	man, err := a.mgr.Snapshot(sessionID, reason, paths)
	if err != nil {
		return agent.CheckpointReceipt{}, err
	}
	scope, notice := man.Scope, "Recovery protects the selected file paths."
	if scope == "" {
		scope = "selected-files"
	}
	if scope == "git-visible" {
		notice = "Recovery protects tracked and untracked nonignored files. Ignored files and Git metadata are excluded."
	}
	if scope == "workspace" {
		notice = "Recovery protects regular workspace files. Git metadata is excluded."
	}
	return agent.CheckpointReceipt{ID: man.ID, Scope: scope, Entries: len(man.Entries), Notice: notice}, nil
}
