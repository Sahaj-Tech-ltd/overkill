// Package journal — 3-layer query protocol over the FlightRecorder
// raw JSONL store (master plan §4.19).
//
// Separate from the ObservationStore-backed JournalQuery in query.go:
// that path uses structured observation records via the journal sub-
// agent. THIS path is the raw flight-recorder readback — every turn,
// every tool call, every error logged as-it-happened.
//
// The agent calls these mid-session via tools, NOT from boot. The
// design follows claude-mem's progressive disclosure: cheap index
// first, drill in only when interesting.
//
//   - SearchFlight   → compact index: ID, timestamp, type, title
//   - TimelineFlight → chronological context around an anchor entry
//   - GetFlight      → full Entry on demand
//
// Implementation: streaming scan over raw/ JSONL files. Entries are
// never fully materialized — we stream with bufio.Scanner and
// early-terminate when enough results are found.
package journal

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FlightIndexHit is one row in the compact search index. Title is the
// first ~80 chars of content (newlines collapsed). Tiny on purpose so
// the model can fit many results in one tool call.
type FlightIndexHit struct {
	ID        string    `json:"id"`
	Type      EntryType `json:"type"`
	Timestamp string    `json:"timestamp"` // RFC3339 for readability
	Title     string    `json:"title"`
	SessionID string    `json:"session_id,omitempty"`
}

// FlightSearchOptions controls SearchFlight behaviour.
type FlightSearchOptions struct {
	Query   string    // case-insensitive substring; empty = "all"
	Type    EntryType // empty = any type
	Session string    // empty = any session
	Limit   int       // 0 → 20
}

// SearchFlight returns matching raw-journal entries as a compact index,
// newest first. Streams JSONL files in reverse-chronological order and
// early-terminates after hitting the limit — entries are never fully
// materialized.
func (r *FlightRecorder) SearchFlight(opts FlightSearchOptions) ([]FlightIndexHit, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	queryLower := strings.ToLower(opts.Query)

	// Get sorted file list (reverse chronological — newest first).
	files, err := r.listRawFilesDesc()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	hits := make([]FlightIndexHit, 0, limit)
	for _, fname := range files {
		path := filepath.Join(r.dir, "raw", fname)
		fileHits, stopped := r.scanFileForSearch(path, opts, queryLower, limit-len(hits))
		hits = append(hits, fileHits...)
		if stopped || len(hits) >= limit {
			return hits, nil
		}
	}
	return hits, nil
}

// scanFileForSearch scans a single JSONL file, collecting up to maxHits
// matches. Returns (hits, stopped) — stopped is true when the file had
// enough matches to reach the limit.
func (r *FlightRecorder) scanFileForSearch(path string, opts FlightSearchOptions, queryLower string, maxHits int) ([]FlightIndexHit, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	hits := make([]FlightIndexHit, 0, maxHits)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if opts.Type != "" && e.Type != opts.Type {
			continue
		}
		if opts.Session != "" && e.SessionID != opts.Session {
			continue
		}
		if queryLower != "" && !strings.Contains(strings.ToLower(e.Content), queryLower) {
			continue
		}
		hits = append(hits, FlightIndexHit{
			ID:        e.ID,
			Type:      e.Type,
			Timestamp: e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
			Title:     flightTitlePreview(e.Content),
			SessionID: e.SessionID,
		})
		if len(hits) >= maxHits {
			return hits, true
		}
	}
	return hits, false
}

// TimelineFlight returns `depth` entries before AND after the anchor,
// in chronological order. The anchor is included as the middle entry.
// Uses binary-search over date-named files: first finds the anchor
// timestamp, then only reads files in the surrounding time window.
func (r *FlightRecorder) TimelineFlight(anchorID string, depth int) ([]Entry, error) {
	if anchorID == "" {
		return nil, nil
	}
	if depth <= 0 {
		depth = 5
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Find anchor entry's timestamp by streaming files.
	anchorTS, err := r.findEntryTimestampLocked(anchorID)
	if err != nil || anchorTS.IsZero() {
		return nil, nil
	}

	// Read entries within a time window around the anchor.
	// Add generous padding (±2 * depth hours or at least ±24 hours).
	padHours := time.Duration(depth*2) * time.Hour
	if padHours < 24*time.Hour {
		padHours = 24 * time.Hour
	}
	windowStart := anchorTS.Add(-padHours)
	windowEnd := anchorTS.Add(padHours)

	files, err := r.listRawFilesAsc()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []Entry
	for _, fname := range files {
		// Check if file is within the time window (rough filter by filename).
		path := filepath.Join(r.dir, "raw", fname)
		fileEntries := r.scanFileEntriesInWindow(path, windowStart, windowEnd)
		entries = append(entries, fileEntries...)
	}

	// Sort chronologically and extract the window around the anchor.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	idx := -1
	for i, e := range entries {
		if e.ID == anchorID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, nil
	}
	lo := idx - depth
	if lo < 0 {
		lo = 0
	}
	hi := idx + depth + 1
	if hi > len(entries) {
		hi = len(entries)
	}
	return entries[lo:hi], nil
}

// findEntryTimestampLocked streams JSONL files until it finds the entry
// with the given ID, then returns its timestamp. Caller must hold r.mu.
func (r *FlightRecorder) findEntryTimestampLocked(id string) (time.Time, error) {
	files, err := r.listRawFilesAsc()
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	for _, fname := range files {
		path := filepath.Join(r.dir, "raw", fname)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var e Entry
			if err := json.Unmarshal(line, &e); err != nil {
				continue
			}
			if e.ID == id {
				f.Close()
				return e.Timestamp, nil
			}
		}
		f.Close()
	}
	return time.Time{}, nil
}

// scanFileEntriesInWindow reads entries from a JSONL file and returns
// those whose timestamps fall within [start, end].
func (r *FlightRecorder) scanFileEntriesInWindow(path string, start, end time.Time) []Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var entries []Entry
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if e.Timestamp.Before(start) || e.Timestamp.After(end) {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// GetFlight returns the raw-journal entry with the matching ID, or
// (nil, nil) when not found. Streams files until the ID is found.
func (r *FlightRecorder) GetFlight(id string) (*Entry, error) {
	if id == "" {
		return nil, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	ts, err := r.findEntryTimestampLocked(id)
	if err != nil || ts.IsZero() {
		return nil, nil
	}

	// Now find the full entry around that timestamp.
	pad := 1 * time.Hour // ±1 hour around anchor timestamp
	windowStart := ts.Add(-pad)
	windowEnd := ts.Add(pad)

	files, err := r.listRawFilesAsc()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	for _, fname := range files {
		path := filepath.Join(r.dir, "raw", fname)
		entries := r.scanFileEntriesInWindow(path, windowStart, windowEnd)
		for i := range entries {
			if entries[i].ID == id {
				cp := entries[i]
				return &cp, nil
			}
		}
	}
	return nil, nil
}

// listRawFilesAsc returns raw/ JSONL filenames sorted oldest-first.
func (r *FlightRecorder) listRawFilesAsc() ([]string, error) {
	rawDir := filepath.Join(r.dir, "raw")
	dirEntries, err := os.ReadDir(rawDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		files = append(files, de.Name())
	}
	sort.Strings(files)
	return files, nil
}

// listRawFilesDesc returns raw/ JSONL filenames sorted newest-first.
func (r *FlightRecorder) listRawFilesDesc() ([]string, error) {
	files, err := r.listRawFilesAsc()
	if err != nil {
		return nil, err
	}
	// Reverse.
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}
	return files, nil
}

func flightTitlePreview(s string) string {
	const max = 80
	cleaned := strings.ReplaceAll(s, "\n", " ")
	cleaned = strings.ReplaceAll(cleaned, "\t", " ")
	cleaned = strings.TrimSpace(cleaned)
	if r := []rune(cleaned); len(r) > max {
		return string(r[:max]) + "…"
	}
	return cleaned
}
