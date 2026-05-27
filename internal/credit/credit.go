// Package credit — retrospective credit assignment (§8.6 Wave 4,
// approximation of Zhang 2026).
//
// The original paper uses gradient attribution over a trained
// policy. We don't have training infrastructure — what we CAN do
// at the application layer is correlation-based analytics over
// the flight recorder + outcome record:
//
//   - For each completed session with a known outcome (success or
//     failure), tag every action (tool call, decision keyword)
//     with the eventual outcome.
//   - Aggregate over many sessions: count how often each action
//     appears in success vs. failure runs.
//   - Surface "this kind of decision correlates with success" or
//     "this tool-call sequence shows up disproportionately in
//     failures".
//
// This isn't credit assignment in the RL sense — no gradients, no
// learned policy. It's frequency analysis with confidence
// estimates, useful for surfacing patterns the user wouldn't
// notice manually.
//
// What's HERE:
//
//   - Action: one tagged event we can correlate (tool name, error
//     class, decision keyword).
//   - SessionRecord: bundles a session ID + outcome + actions.
//   - Analyzer: folds session records and produces ActionStats —
//     per-action success / failure counts, support, lift.
//   - Lift = P(success | action) / P(success | no action). Lift > 1
//     means the action correlates with success; < 1 with failure.
package credit

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Outcome is the labeled result of a session.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	// OutcomeUnknown is the legal "we don't know yet" state.
	// Sessions with this outcome are not folded into stats.
	OutcomeUnknown Outcome = "unknown"
)

// Action is one tagged event within a session. The Tag is the
// dimension we correlate on — typically the tool name, an error
// class, or a decision keyword.
type Action struct {
	Tag      string `json:"tag"`
	Category string `json:"category,omitempty"` // optional: "tool", "error", "decision"
}

// SessionRecord is one completed session with its labeled outcome
// and the list of actions taken.
type SessionRecord struct {
	SessionID string   `json:"session_id"`
	Outcome   Outcome  `json:"outcome"`
	Actions   []Action `json:"actions"`
	// Tags is operator-supplied free-form labels (model ID, task
	// type) for downstream filtering.
	Tags []string `json:"tags,omitempty"`
}

// ActionStats holds aggregate frequencies for one action tag.
type ActionStats struct {
	Tag           string `json:"tag"`
	Category      string `json:"category,omitempty"`
	SuccessCount  int64  `json:"success_count"`
	FailureCount  int64  `json:"failure_count"`
	TotalSessions int64  `json:"total_sessions"` // sessions where this action appeared
	// Lift = P(success | action) / P(success | no action). Higher
	// than 1.0 means the action correlates with success. NaN when
	// we can't compute (no baseline data).
	Lift float64 `json:"lift"`
	// Confidence is a coarse signal driven by support — the more
	// sessions an action appears in, the more we trust the lift.
	// Buckets: "low" (<5), "medium" (5–19), "high" (20+).
	Confidence string `json:"confidence"`
}

// Analyzer aggregates SessionRecord folds and produces per-action
// statistics. Concurrency-safe.
type Analyzer struct {
	mu        sync.Mutex
	stats     map[string]*ActionStats // keyed by Tag
	sessions  int64                   // total folded
	successes int64                   // total successful sessions
	failures  int64                   // total failed sessions
}

func NewAnalyzer() *Analyzer {
	return &Analyzer{stats: map[string]*ActionStats{}}
}

// Fold incorporates one session's actions into the running stats.
// Outcome must be Success or Failure — Unknown is silently skipped.
func (a *Analyzer) Fold(rec SessionRecord) {
	if rec.Outcome != OutcomeSuccess && rec.Outcome != OutcomeFailure {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions++
	if rec.Outcome == OutcomeSuccess {
		a.successes++
	} else {
		a.failures++
	}
	// One bump per session per action — we don't want a session
	// that called the same tool 10 times to skew lift.
	seen := map[string]bool{}
	for _, act := range rec.Actions {
		if act.Tag == "" || seen[act.Tag] {
			continue
		}
		seen[act.Tag] = true
		s, ok := a.stats[act.Tag]
		if !ok {
			s = &ActionStats{Tag: act.Tag, Category: act.Category}
			a.stats[act.Tag] = s
		}
		if act.Category != "" && s.Category == "" {
			s.Category = act.Category
		}
		s.TotalSessions++
		if rec.Outcome == OutcomeSuccess {
			s.SuccessCount++
		} else {
			s.FailureCount++
		}
	}
}

// Compute returns the current ActionStats with Lift + Confidence
// populated. Sorted by absolute distance of Lift from 1.0
// (largest correlations first).
func (a *Analyzer) Compute() []ActionStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ActionStats, 0, len(a.stats))
	pSuccessOverall := 0.5
	if a.sessions > 0 {
		pSuccessOverall = float64(a.successes) / float64(a.sessions)
	}
	for _, s := range a.stats {
		dup := *s
		dup.Lift = computeLift(s, a.sessions, a.successes)
		dup.Confidence = confidenceFor(s.TotalSessions)
		_ = pSuccessOverall
		out = append(out, dup)
	}
	sort.Slice(out, func(i, j int) bool {
		// Larger |lift - 1| ranks first.
		return math.Abs(out[i].Lift-1.0) > math.Abs(out[j].Lift-1.0)
	})
	return out
}

// computeLift returns P(success | action) / P(success | no action).
// Returns NaN when there's no baseline data on either side.
func computeLift(s *ActionStats, totalSessions, totalSuccesses int64) float64 {
	if s.TotalSessions == 0 {
		return math.NaN()
	}
	pSuccessGiven := float64(s.SuccessCount) / float64(s.TotalSessions)
	without := totalSessions - s.TotalSessions
	if without == 0 {
		return math.NaN()
	}
	successesWithout := totalSuccesses - s.SuccessCount
	pSuccessBaseline := float64(successesWithout) / float64(without)
	if pSuccessBaseline == 0 {
		// Avoid divide-by-zero: if there's no baseline success,
		// any action that ever succeeded is infinitely lifted.
		// Cap at a large finite number so JSON is happy.
		if pSuccessGiven > 0 {
			return 100.0
		}
		return 1.0
	}
	return pSuccessGiven / pSuccessBaseline
}

// confidenceFor buckets support → confidence label.
func confidenceFor(support int64) string {
	switch {
	case support >= 20:
		return "high"
	case support >= 5:
		return "medium"
	default:
		return "low"
	}
}

// TopByCategory returns the top-K actions filtered to a category
// (e.g. "tool" or "error"). Pass "" to get every category.
func (a *Analyzer) TopByCategory(category string, k int) []ActionStats {
	all := a.Compute()
	if category == "" && k <= 0 {
		return all
	}
	out := all[:0]
	for _, s := range all {
		if category != "" && s.Category != category {
			continue
		}
		out = append(out, s)
		if k > 0 && len(out) >= k {
			break
		}
	}
	return out
}

// SuccessCorrelated returns actions whose Lift exceeds the
// threshold AND meets the confidence floor. Use this for
// "actions that helped" surfacing.
func (a *Analyzer) SuccessCorrelated(minLift float64, minConfidence string) []ActionStats {
	if minLift <= 1.0 {
		minLift = 1.2
	}
	all := a.Compute()
	out := all[:0]
	for _, s := range all {
		if !math.IsNaN(s.Lift) && s.Lift >= minLift && atLeastConfidence(s.Confidence, minConfidence) {
			out = append(out, s)
		}
	}
	return out
}

// FailureCorrelated returns actions whose Lift is BELOW the
// threshold (associated with failure) AND meets the confidence
// floor.
func (a *Analyzer) FailureCorrelated(maxLift float64, minConfidence string) []ActionStats {
	if maxLift <= 0 || maxLift >= 1.0 {
		maxLift = 0.8
	}
	all := a.Compute()
	out := all[:0]
	for _, s := range all {
		if !math.IsNaN(s.Lift) && s.Lift <= maxLift && atLeastConfidence(s.Confidence, minConfidence) {
			out = append(out, s)
		}
	}
	return out
}

// atLeastConfidence compares confidence labels. low < medium < high.
func atLeastConfidence(have, want string) bool {
	rank := map[string]int{"low": 1, "medium": 2, "high": 3}
	if want == "" {
		return true
	}
	return rank[have] >= rank[want]
}

// Store persists analyzer state to disk so stats accumulate
// across daemon restarts. JSON-per-file in dir.
type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// SaveSession persists one session record to disk. Idempotent by
// SessionID — re-saving the same session replaces the prior file.
func (s *Store) SaveSession(rec SessionRecord) error {
	if rec.SessionID == "" {
		return errors.New("credit: session id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("credit: mkdir: %w", err)
	}
	path := filepath.Join(s.dir, rec.SessionID+".json")
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("credit: marshal: %w", err)
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("credit: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("credit: rename: %w", err)
	}
	return nil
}

// LoadAll reads every persisted session record. Sessions with
// OutcomeUnknown are returned but not useful for stats folding.
func (s *Store) LoadAll() ([]SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("credit: readdir: %w", err)
	}
	var out []SessionRecord
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var rec SessionRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// FormatActionStats renders a one-line summary suitable for CLI
// output: "tool/Read: lift 1.34 (success 18/22, confidence
// medium)".
func FormatActionStats(s ActionStats) string {
	lift := "n/a"
	if !math.IsNaN(s.Lift) {
		lift = fmt.Sprintf("%.2f", s.Lift)
	}
	cat := s.Category
	if cat == "" {
		cat = "?"
	}
	return fmt.Sprintf("%s/%s: lift %s (success %d/%d, confidence %s)",
		cat, s.Tag, lift, s.SuccessCount, s.TotalSessions, s.Confidence)
}

// FormatTopFindings renders the N most-correlated actions for
// surfacing. Empty when no stats.
func FormatTopFindings(stats []ActionStats, n int) string {
	if len(stats) == 0 {
		return ""
	}
	if n > 0 && len(stats) > n {
		stats = stats[:n]
	}
	parts := make([]string, len(stats))
	for i, s := range stats {
		parts[i] = FormatActionStats(s)
	}
	return strings.Join(parts, "\n  ")
}
