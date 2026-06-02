package main

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
)

// checkpointSnapshotterAdapter bridges *checkpoint.Manager.Snapshot (which
// returns *Manifest, error) to agent.CheckpointSnapshotter (which returns
// error only). internal/agent can't import internal/checkpoint without a
// cycle; the adapter lives here so wire-up stays in cmd/overkill.
//
// Empty paths slice → treat as "snapshot the whole tracked workspace".
// For now we forward as-is; Manager.Snapshot interprets nil/empty as
// "all paths it knows about." Whole-tree snapshotting beyond that is a
// future enhancement.
type checkpointSnapshotterAdapter struct {
	mgr *checkpoint.Manager
}

var _ agent.CheckpointSnapshotter = (*checkpointSnapshotterAdapter)(nil)

func (a *checkpointSnapshotterAdapter) Snapshot(sessionID, reason string, paths []string) error {
	if a == nil || a.mgr == nil {
		return nil
	}
	_, err := a.mgr.Snapshot(sessionID, reason, paths)
	return err
}
