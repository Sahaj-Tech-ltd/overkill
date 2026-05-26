// Package session — BadgerDB integrity check (master plan §4.20).
//
// The plan demands graceful degradation when the embedded store goes
// bad: don't silently cold-start the relationship arc; surface the
// damage and offer a restore from memory-export.md.
//
// Probe does the cheap detection work — opens the DB read-only briefly
// to confirm the file structure is parseable. Two layers of failure:
//   1. Open fails outright (typical: corrupt VLog header).
//   2. Open succeeds but the head sequence is unreadable.
//
// Either layer trips Probe → caller writes a memory_corruption alert
// and switches the TUI into restore-prompt mode.
package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dgraph-io/badger/v4"
)

// ErrCorrupt is returned by Probe when the database cannot be opened
// or the integrity smoke fails. Callers compare with errors.Is.
var ErrCorrupt = errors.New("session: BadgerDB corruption detected")

// ProbeResult summarises an integrity check.
type ProbeResult struct {
	Dir         string
	Corrupt     bool
	Cause       string
	ExportFound bool   // memory-export.md exists alongside — restore path available
	ExportPath  string // populated when ExportFound is true
}

// Probe opens dir read-only and runs a 1-key smoke check to confirm
// the DB is at least structurally parseable. Empty / missing dirs
// are fine (the store will just be created fresh on first Open).
//
// memoryExportPath, when non-empty, is checked for existence — Probe
// fills ExportFound so the caller can offer the restore prompt
// without re-stat'ing.
func Probe(dir, memoryExportPath string) ProbeResult {
	out := ProbeResult{Dir: dir, ExportPath: memoryExportPath}
	if dir == "" {
		return out
	}
	// Missing dir → not corrupt (fresh install). Caller's Open will
	// create it.
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			out = checkExport(out, memoryExportPath)
			return out
		}
		out.Corrupt = true
		out.Cause = fmt.Sprintf("stat %s: %v", dir, err)
		out = checkExport(out, memoryExportPath)
		return out
	}

	// Read-only open is cheap — Badger will refuse to open broken
	// VLog / SSTable files and return an error we surface as cause.
	opts := badger.DefaultOptions(dir).
		WithLoggingLevel(badger.ERROR).
		WithReadOnly(true)
	db, err := badger.Open(opts)
	if err != nil {
		// "manifest has unsupported version" or "decoded value is
		// not nil" — usually means file truncation or version skew.
		// Both are user-visible corruption.
		out.Corrupt = true
		out.Cause = fmt.Sprintf("open %s: %v", filepath.Base(dir), err)
		out = checkExport(out, memoryExportPath)
		return out
	}
	defer db.Close()

	// Run a no-op view as the smoke check — Badger lazily parses
	// some structures, so we touch one to force any deferred read.
	err = db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		it.Rewind()
		return nil
	})
	if err != nil {
		out.Corrupt = true
		out.Cause = fmt.Sprintf("iterator probe %s: %v", filepath.Base(dir), err)
	}
	out = checkExport(out, memoryExportPath)
	return out
}

func checkExport(res ProbeResult, exportPath string) ProbeResult {
	if exportPath == "" {
		return res
	}
	if info, err := os.Stat(exportPath); err == nil && info.Size() > 0 {
		res.ExportFound = true
		res.ExportPath = exportPath
	}
	return res
}

// CorruptionNotice returns the single-line notice the TUI shows when
// Probe reports corruption. Composes differently depending on whether
// a memory-export.md is available to restore from.
func (r ProbeResult) CorruptionNotice() string {
	if !r.Corrupt {
		return ""
	}
	if r.ExportFound {
		return fmt.Sprintf(
			"Memory corrupted (%s). I knew I knew you. I don't know what I knew. "+
				"Last export at %s — want me to restore from that?",
			r.Cause, r.ExportPath)
	}
	return fmt.Sprintf(
		"Memory corrupted (%s). I don't remember anything. We're starting fresh. "+
			"Here's what I wish I still knew — type /restore <path> if you have a backup.",
		r.Cause)
}

// CheckOnBoot runs Probe at startup and, when corruption is detected,
// creates a memory_corruption alert via the onCorrupt callback. Returns
// the probe result so the caller can surface the notice to the user.
//
// dir is the BadgerDB directory (~/.overkill/sessions). exportPath is
// the memory-export.md path (~/.overkill/memory-export.md).
// onCorrupt receives ("memory_corruption", corruptionNotice, "").
func CheckOnBoot(dir, exportPath string, onCorrupt func(alertType, message, sessionID string) error) ProbeResult {
	res := Probe(dir, exportPath)
	if !res.Corrupt {
		return res
	}
	// Best-effort alert creation — don't fail the boot if the alert
	// store is also broken. The probe result is still available for
	// the TUI to surface directly.
	_ = onCorrupt("memory_corruption", res.CorruptionNotice(), "")
	return res
}
