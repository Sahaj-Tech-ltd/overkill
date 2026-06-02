package drift

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestStats_WelfordMatchesNaive(t *testing.T) {
	// Welford should match the naive mean/variance for a small
	// dataset. Lets us trust it on the production-grade case.
	data := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	var s Stats
	for _, x := range data {
		s.Update(x)
	}
	if math.Abs(s.Mean-5.0) > 1e-9 {
		t.Errorf("mean: got %v want 5", s.Mean)
	}
	// Sample variance: (sum of (x-mean)^2) / (n-1) = 32/7 ≈ 4.571
	got := s.Variance()
	want := 32.0 / 7.0
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("variance: got %v want %v", got, want)
	}
}

func TestStats_ZScoreZeroOnInsufficientData(t *testing.T) {
	s := &Stats{}
	if s.ZScore(42) != 0 {
		t.Error("z-score should be 0 with no data")
	}
	s.Update(3.0)
	if s.ZScore(42) != 0 {
		t.Error("z-score should be 0 with one sample (zero variance)")
	}
}

func TestStats_ZScoreCorrect(t *testing.T) {
	s := &Stats{}
	for _, x := range []float64{2, 4, 4, 4, 5, 5, 7, 9} {
		s.Update(x)
	}
	// Observe 9 with mean=5, stddev≈2.138 → z ≈ 1.87
	z := s.ZScore(9)
	if z < 1.85 || z > 1.90 {
		t.Errorf("z-score off: got %v", z)
	}
}

func TestBaseline_FoldUpdatesMetrics(t *testing.T) {
	b := NewBaseline()
	b.Fold(map[Metric]float64{
		MetricToolCallsPerTurn: 3,
		MetricErrorRate:        0.1,
	})
	b.Fold(map[Metric]float64{
		MetricToolCallsPerTurn: 5,
		MetricErrorRate:        0.2,
	})
	if b.Sessions != 2 {
		t.Errorf("expected 2 sessions, got %d", b.Sessions)
	}
	if math.Abs(b.Metrics[MetricToolCallsPerTurn].Mean-4) > 1e-9 {
		t.Errorf("tool-call mean: %v", b.Metrics[MetricToolCallsPerTurn].Mean)
	}
}

func TestCompare_SkipsWhenBaselineWarming(t *testing.T) {
	b := NewBaseline()
	b.Fold(map[Metric]float64{MetricErrorRate: 0.1})
	// Only 1 session — below MinSessions=5 default.
	findings := b.Compare(map[Metric]float64{MetricErrorRate: 1.0}, CompareOptions{})
	if len(findings) != 0 {
		t.Errorf("warmup should yield no findings, got %+v", findings)
	}
}

func TestCompare_FlagsOutliersAboveThreshold(t *testing.T) {
	b := NewBaseline()
	// 6 sessions clustered around 0.1.
	for _, v := range []float64{0.08, 0.10, 0.12, 0.09, 0.11, 0.10} {
		b.Fold(map[Metric]float64{MetricErrorRate: v})
	}
	// New session way above: should flag.
	findings := b.Compare(map[Metric]float64{MetricErrorRate: 0.8}, CompareOptions{Threshold: 2.0})
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d (%+v)", len(findings), findings)
	}
	if findings[0].Direction != "above" {
		t.Errorf("direction should be above: %+v", findings[0])
	}
	if findings[0].ZScore < 2 {
		t.Errorf("z-score should exceed threshold: %v", findings[0].ZScore)
	}
}

func TestCompare_BelowDirectionDetected(t *testing.T) {
	b := NewBaseline()
	for i := 0; i < 6; i++ {
		b.Fold(map[Metric]float64{MetricToolCallsPerTurn: 5})
	}
	// Way fewer tool calls → drift below baseline.
	findings := b.Compare(map[Metric]float64{MetricToolCallsPerTurn: 0}, CompareOptions{})
	// Note: zero-variance baseline means z-score is 0 and we'll
	// skip. We need some variance for the below-direction test.
	for _, v := range []float64{5.5, 4.5, 5.2, 4.8} {
		b.Fold(map[Metric]float64{MetricToolCallsPerTurn: v})
	}
	findings = b.Compare(map[Metric]float64{MetricToolCallsPerTurn: 0}, CompareOptions{Threshold: 1.5})
	if len(findings) == 0 || findings[0].Direction != "below" {
		t.Errorf("expected below-direction finding: %+v", findings)
	}
}

func TestCompare_SortsByAbsZScore(t *testing.T) {
	b := NewBaseline()
	for i := 0; i < 6; i++ {
		b.Fold(map[Metric]float64{
			MetricErrorRate:        0.1,
			MetricToolCallsPerTurn: 3,
		})
	}
	// Inject variance so z-scores are computable.
	for _, v := range []float64{0.09, 0.11, 0.10} {
		b.Fold(map[Metric]float64{MetricErrorRate: v, MetricToolCallsPerTurn: v * 30})
	}
	findings := b.Compare(map[Metric]float64{
		MetricErrorRate:        0.5, // mild outlier
		MetricToolCallsPerTurn: 50,  // huge outlier
	}, CompareOptions{Threshold: 1.0})
	if len(findings) < 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	if findings[0].Metric != MetricToolCallsPerTurn {
		t.Errorf("worst drift should sort first: %+v", findings)
	}
}

func TestStore_LoadMissingReturnsEmptyBaseline(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "missing.json"))
	b, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if b == nil || b.Sessions != 0 {
		t.Errorf("missing → empty baseline, got %+v", b)
	}
}

func TestStore_SaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	s := NewStore(path)
	b := NewBaseline()
	b.Fold(map[Metric]float64{MetricErrorRate: 0.1})
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Sessions != 1 {
		t.Errorf("roundtrip sessions: got %d want 1", loaded.Sessions)
	}
}

func TestFormatFinding_RendersAxesAndDirection(t *testing.T) {
	f := Finding{
		Metric:    MetricToolCallsPerTurn,
		Observed:  8.2,
		Mean:      3.1,
		StdDev:    1.4,
		ZScore:    3.6,
		Direction: "above",
	}
	got := FormatFinding(f)
	for _, want := range []string{"tool_calls_per_turn", "above baseline", "observed 8.20", "mean 3.10", "+3.6σ"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestFormatFindings_JoinsWithSemicolon(t *testing.T) {
	findings := []Finding{
		{Metric: MetricErrorRate, Direction: "above", ZScore: 3},
		{Metric: MetricRecoveryRate, Direction: "below", ZScore: -2},
	}
	got := FormatFindings(findings)
	if !strings.Contains(got, ";") {
		t.Errorf("expected semicolon separator: %s", got)
	}
}

func TestFormatFindings_EmptyReturnsEmpty(t *testing.T) {
	if FormatFindings(nil) != "" {
		t.Error("empty findings → empty string")
	}
}
