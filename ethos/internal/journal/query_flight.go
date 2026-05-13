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
// Implementation: linear scan over raw/ JSONL files. Fine for typical
// journal sizes; FTS / vector index ships later when needed.
package journal

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// newest first. Best-effort over per-file read failures.
func (r *FlightRecorder) SearchFlight(opts FlightSearchOptions) ([]FlightIndexHit, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	queryLower := strings.ToLower(opts.Query)

	entries, err := r.scanRaw()
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	hits := make([]FlightIndexHit, 0, limit)
	for _, e := range entries {
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
		if len(hits) >= limit {
			break
		}
	}
	return hits, nil
}

// TimelineFlight returns `depth` entries before AND after the anchor,
// in chronological order. The anchor is included as the middle entry.
// Useful for "what was happening around <event>".
func (r *FlightRecorder) TimelineFlight(anchorID string, depth int) ([]Entry, error) {
	if anchorID == "" {
		return nil, nil
	}
	if depth <= 0 {
		depth = 5
	}
	entries, err := r.scanRaw()
	if err != nil {
		return nil, err
	}
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

// GetFlight returns the raw-journal entry with the matching ID, or
// (nil, nil) when not found.
func (r *FlightRecorder) GetFlight(id string) (*Entry, error) {
	if id == "" {
		return nil, nil
	}
	entries, err := r.scanRaw()
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.ID == id {
			cp := e
			return &cp, nil
		}
	}
	return nil, nil
}

// scanRaw reads every JSONL file under raw/. Wraps readFiltered with a
// no-op filter to share the per-file read logic.
func (r *FlightRecorder) scanRaw() ([]Entry, error) {
	all, err := r.readFiltered(func(Entry) bool { return true })
	if err != nil {
		// Fresh-install case: raw/ doesn't exist yet → return empty.
		if _, statErr := os.Stat(filepath.Join(r.dir, "raw")); statErr != nil && os.IsNotExist(statErr) {
			return nil, nil
		}
		return nil, err
	}
	return all, nil
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
