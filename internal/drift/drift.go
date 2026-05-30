// Package drift — behavioral drift detection (§8.3 Wave 4).
//
// Track per-session metrics, maintain a running per-user baseline
// (mean + variance via Welford's online algorithm), and flag when
// the current session is N standard deviations from baseline.
//
// Why: model swaps, prompt changes, even silent provider-side
// updates can shift the agent's behavior. The user may not notice
// until the agent does something it never used to do. Drift
// detection surfaces "this session looks unusual" before the user
// hits the surprise.
//
// What we track per session:
//
//   - ToolCallsPerTurn: agent's average tool-use intensity.
//   - ErrorRate: error entries / total entries.
//   - RecoveryRate: successful recoveries / total errors.
//   - SessionLength: total entries in the session.
//   - AvgTurnDuration: time between user_input entries.
//
// Storage: rolling baseline state at ~/.overkill/drift/baseline.json.
// Per-session metrics computed on demand from the flight recorder.
// Welford's algorithm means we don't need to keep individual
// session metrics around once they've been folded in — O(1) memory.
package drift

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
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

// Metric is one named axis we track. Keeping these as constants
// keeps the JSON shape stable across versions.
type Metric string

const (
	MetricToolCallsPerTurn Metric = "tool_calls_per_turn"
	MetricErrorRate        Metric = "error_rate"
	MetricRecoveryRate     Metric = "recovery_rate"
	MetricSessionLength    Metric = "session_length"
	MetricAvgTurnDuration  Metric = "avg_turn_duration_seconds"
)

// AllMetrics is the canonical list — useful for ranging over.
var AllMetrics = []Metric{
	MetricToolCallsPerTurn,
	MetricErrorRate,
	MetricRecoveryRate,
	MetricSessionLength,
	MetricAvgTurnDuration,
}

// Stats is the Welford running-stats record for one metric.
// Count + Mean + M2 (sum of squared diffs from mean). Variance =
// M2 / (Count - 1). Memory cost: O(1) per metric regardless of
// how many sessions you fold in.
type Stats struct {
	Count int64   `json:"count"`
	Mean  float64 `json:"mean"`
	M2    float64 `json:"m2"`
}

// Variance returns the sample variance (M2 / (N-1)). Zero when
// Count < 2.
func (s *Stats) Variance() float64 {
	if s.Count < 2 {
		return 0
	}
	return s.M2 / float64(s.Count-1)
}

// StdDev returns the sample standard deviation.
func (s *Stats) StdDev() float64 {
	return math.Sqrt(s.Variance())
}

// Update folds a new observation in via Welford's online algorithm.
// Stable + numerically robust even for huge counts.
func (s *Stats) Update(x float64) {
	s.Count++
	delta := x - s.Mean
	s.Mean += delta / float64(s.Count)
	delta2 := x - s.Mean
	s.M2 += delta * delta2
}

// ZScore returns (x - Mean) / StdDev. When StdDev is zero (too
// few observations to estimate variance), returns 0 — caller
// treats this as "not enough baseline data to flag drift".
func (s *Stats) ZScore(x float64) float64 {
	sd := s.StdDev()
	if sd == 0 {
		return 0
	}
	return (x - s.Mean) / sd
}

// Baseline holds per-metric running stats for one user.
type Baseline struct {
	mu         sync.RWMutex       `json:"-"`
	Metrics    map[Metric]*Stats  `json:"metrics"`
	UpdatedAt  time.Time          `json:"updated_at"`
	Sessions   int64              `json:"sessions"` // total folded-in
	WarmupHint string             `json:"warmup_hint,omitempty"`
}

// NewBaseline returns an empty baseline.
func NewBaseline() *Baseline {
	b := &Baseline{Metrics: map[Metric]*Stats{}}
	for _, m := range AllMetrics {
		b.Metrics[m] = &Stats{}
	}
	return b
}

// Fold incorporates one session's metrics into the baseline.
func (b *Baseline) Fold(sample map[Metric]float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for m, v := range sample {
		s, ok := b.Metrics[m]
		if !ok {
			s = &Stats{}
			b.Metrics[m] = s
		}
		s.Update(v)
	}
	b.Sessions++
	b.UpdatedAt = time.Now().UTC()
}

// Finding is one drift signal.
type Finding struct {
	Metric   Metric
	Observed float64
	Mean     float64
	StdDev   float64
	ZScore   float64
	// Direction is "above" or "below" so the caller can say "the
	// agent was unusually QUIET this session" vs. "unusually NOISY".
	Direction string
}

// CompareOptions tunes Compare.
type CompareOptions struct {
	// Threshold is the |z-score| above which we flag drift.
	// Default 2.0 — caught at the ~5% tail.
	Threshold float64
	// MinSessions is the minimum baseline session count before we
	// trust the variance estimate. Below this we skip flagging.
	// Default 5.
	MinSessions int64
}

func (o CompareOptions) threshold() float64 {
	if o.Threshold > 0 {
		return o.Threshold
	}
	return 2.0
}

func (o CompareOptions) minSessions() int64 {
	if o.MinSessions > 0 {
		return o.MinSessions
	}
	return 5
}

// Compare evaluates a new session's metrics against the baseline
// and returns drift findings. Empty when no metric crossed the
// threshold OR the baseline has too few sessions to trust.
func (b *Baseline) Compare(sample map[Metric]float64, opts CompareOptions) []Finding {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.Sessions < opts.minSessions() {
		return nil
	}
	var out []Finding
	thresh := opts.threshold()
	for m, v := range sample {
		s, ok := b.Metrics[m]
		if !ok || s.Count < 2 {
			continue
		}
		z := s.ZScore(v)
		if math.Abs(z) < thresh {
			continue
		}
		dir := "above"
		if z < 0 {
			dir = "below"
		}
		out = append(out, Finding{
			Metric:    m,
			Observed:  v,
			Mean:      s.Mean,
			StdDev:    s.StdDev(),
			ZScore:    z,
			Direction: dir,
		})
	}
	// Sort by absolute z-score descending so the worst drifts surface
	// first.
	sort.Slice(out, func(i, j int) bool {
		return math.Abs(out[i].ZScore) > math.Abs(out[j].ZScore)
	})
	return out
}

// Store persists a Baseline at a fixed path. Atomic temp+rename
// writes so a crash mid-save leaves the prior baseline intact.
type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load reads the baseline. Missing file → empty baseline (cold
// start). Corrupt file → nil baseline + non-nil error so callers
// can detect the failure and avoid folding corrupt data.
func (s *Store) Load() (*Baseline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewBaseline(), nil
		}
		return nil, fmt.Errorf("drift: read: %w", err)
	}
	if len(data) == 0 {
		return NewBaseline(), nil
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("drift: parse: %w", err)
	}
	if b.Metrics == nil {
		b.Metrics = map[Metric]*Stats{}
	}
	for _, m := range AllMetrics {
		if _, ok := b.Metrics[m]; !ok {
			b.Metrics[m] = &Stats{}
		}
	}
	return &b, nil
}

// Save persists the baseline atomically.
func (s *Store) Save(b *Baseline) error {
	if b == nil {
		return errors.New("drift: nil baseline")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("drift: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("drift: marshal: %w", err)
	}
	if err := atomicfile.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("drift: write: %w", err)
	}
	return nil
}

// FormatFinding renders a one-line summary suitable for a session-
// open heads-up: "tool_calls_per_turn unusually above baseline
// (observed 8.2, mean 3.1 ± 1.4, z=+3.6σ)".
func FormatFinding(f Finding) string {
	sign := "+"
	if f.ZScore < 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s unusually %s baseline (observed %.2f, mean %.2f ± %.2f, z=%s%.1fσ)",
		f.Metric, f.Direction, f.Observed, f.Mean, f.StdDev, sign, math.Abs(f.ZScore))
}

// FormatFindings renders all findings joined by "; ". Empty when
// no findings.
func FormatFindings(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	parts := make([]string, len(findings))
	for i, f := range findings {
		parts[i] = FormatFinding(f)
	}
	return strings.Join(parts, "; ")
}
