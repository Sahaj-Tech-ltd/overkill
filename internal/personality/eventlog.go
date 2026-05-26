// Package personality — event-log durability for memory state files.
//
// The relationship arc, fingerprint, and style state are persisted
// as JSON snapshots (atomic write + rename). That's fast and small
// but it has one bad failure mode: a single corrupted write or a
// stray overwrite blows away every prior session's accumulated
// state. The pre-tool scanner now blocks the easy adversarial case
// (agent calling Write on the file), but process crashes,
// filesystem hiccups, and unknown future bugs can still corrupt it.
//
// This package adds a parallel append-only event log next to each
// snapshot. Every save appends a `{ts, version, state}` line to
// the log before rewriting the snapshot. Load prefers the snapshot
// (fast path) but falls back to the latest event-log line if the
// snapshot is missing, empty, or corrupt.
//
// Design notes:
//
//   - Full state per event (not deltas). State files are small —
//     low tens of KB max — so disk cost is negligible and recovery
//     is trivial: read the last well-formed line.
//   - Atomic-append via single-syscall O_APPEND write. Partial
//     lines from a crash are skipped at replay time.
//   - The log grows monotonically. A future pass can compact old
//     entries; for now, growth is bounded by save frequency
//     (a save per session × ~1KB per save = years before it
//     matters).
package personality

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventLogEntry is one durable save record. The Version field is a
// monotonic counter so callers can tell stale snapshots from fresh
// ones — used by LatestFromLog to pick the right entry when
// multiple boots interleave writes.
type EventLogEntry struct {
	Timestamp time.Time       `json:"ts"`
	Version   int64           `json:"version"`
	State     json.RawMessage `json:"state"`
}

// EventLog wraps the on-disk JSONL for one snapshot file. Caller
// supplies the snapshot path; the log lives next to it with a
// ".log.jsonl" suffix.
type EventLog struct {
	path string // log path
	mu   sync.Mutex
}

// NewEventLog builds an EventLog for the given SNAPSHOT path. The
// log path is derived (path + ".log.jsonl") so the caller doesn't
// have to think about it.
func NewEventLog(snapshotPath string) *EventLog {
	return &EventLog{path: snapshotPath + ".log.jsonl"}
}

// Append writes a single event for the given state blob. The
// version counter is computed from the log's current line count —
// callers don't manage it themselves. Best-effort: write failures
// surface as errors, but callers should treat the snapshot as the
// authoritative store and the event log as defense-in-depth.
func (e *EventLog) Append(state []byte) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
		return fmt.Errorf("eventlog: mkdir: %w", err)
	}
	version, _ := e.countLocked() // best-effort; 0 on missing file
	entry := EventLogEntry{
		Timestamp: time.Now().UTC(),
		Version:   version + 1,
		State:     json.RawMessage(state),
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("eventlog: marshal: %w", err)
	}
	// Rotate when the on-disk file passes the size cap so an
	// always-on agent doesn't accumulate gigabytes of event-log data
	// over the months. We keep the LAST half-cap-worth by truncating
	// the file's head: copy the tail forward, then re-open and write
	// the new entry. Best-effort; on rotation failure we still append
	// (no worse than today's unbounded growth).
	if info, statErr := os.Stat(e.path); statErr == nil && info.Size() > maxEventLogBytes {
		_ = e.rotateLocked()
	}
	f, err := os.OpenFile(e.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("eventlog: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("eventlog: write: %w", err)
	}
	return nil
}

// maxEventLogBytes is the rotation threshold — at 5 MiB an event log
// holds tens of thousands of personality state snapshots, far more
// than any rollback/audit ever needs. Rotation drops the oldest half.
const maxEventLogBytes = 5 * 1024 * 1024

// rotateLocked keeps the last ~half of the event log and discards the
// older half. Best-effort: on any error the existing file is left in
// place and the next append continues. Caller must hold e.mu.
func (e *EventLog) rotateLocked() error {
	data, err := os.ReadFile(e.path)
	if err != nil {
		return err
	}
	if len(data) <= maxEventLogBytes/2 {
		return nil
	}
	// Find the first newline AFTER the midpoint so we cut on an
	// entry boundary, not mid-JSON.
	mid := len(data) - maxEventLogBytes/2
	for mid < len(data) && data[mid] != '\n' {
		mid++
	}
	if mid >= len(data) {
		return nil
	}
	return os.WriteFile(e.path, data[mid+1:], 0o644)
}

// Latest reads the most recent well-formed entry from the log, or
// (nil, nil) if the log doesn't exist or is empty. Corrupt trailing
// lines are skipped — we walk backwards from EOF, preferring the
// last parseable line, so a partial-line crash doesn't lose the
// prior good state.
func (e *EventLog) Latest() (*EventLogEntry, error) {
	if e == nil {
		return nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	f, err := os.Open(e.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("eventlog: open: %w", err)
	}
	defer f.Close()

	var lastGood *EventLogEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry EventLogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// Corrupt/partial — keep the previous good entry.
			continue
		}
		dup := entry
		lastGood = &dup
	}
	if err := sc.Err(); err != nil {
		return lastGood, fmt.Errorf("eventlog: scan: %w", err)
	}
	return lastGood, nil
}

// countLocked is best-effort line counting for version assignment.
// Errors return 0 (treated as "first save"), which is the right
// answer for a missing log.
func (e *EventLog) countLocked() (int64, error) {
	f, err := os.Open(e.path)
	if err != nil {
		return 0, nil
	}
	defer f.Close()
	var count int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			count++
		}
	}
	return count, sc.Err()
}

// LoadWithFallback is the canonical read pattern for the three
// memory files. It tries the snapshot first; if that returns
// (nil, nil) — i.e. file missing or empty — OR if unmarshaling
// fails, it falls back to the latest event-log entry. The
// returned bytes are the raw JSON for the caller's State type.
//
// snapshotReader is the snapshot path's read function (typically
// os.ReadFile). snapshotValid is a callback the caller uses to
// decide if the snapshot bytes parse cleanly into the expected
// state shape — returning false triggers fallback.
func LoadWithFallback(
	snapshotPath string,
	log *EventLog,
	snapshotValid func([]byte) bool,
) ([]byte, error) {
	data, err := os.ReadFile(snapshotPath)
	if err == nil && len(data) > 0 && (snapshotValid == nil || snapshotValid(data)) {
		return data, nil
	}
	// Snapshot missing, empty, or corrupt — try the event log.
	if log == nil {
		return data, err
	}
	entry, lerr := log.Latest()
	if lerr != nil || entry == nil {
		return data, err // surface the snapshot error if any
	}
	return entry.State, nil
}
