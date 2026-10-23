package walls

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
)

type TestQualityConfig struct {
	Enabled      bool
	MinCoverage  float64
	RequireTests bool
	StrictMode   bool
}

type TestQualityWall struct {
	config TestQualityConfig
}

type TestAnalysis struct {
	TestCount      int  `json:"test_count"`
	HasTableDriven bool `json:"has_table_driven"`
	HasEdgeCases   bool `json:"has_edge_cases"`
	HasErrorCases  bool `json:"has_error_cases"`
	HasConcurrency bool `json:"has_concurrency"`
	// Coverage is unknown until a coverage-tool profile is supplied.
	Coverage            *float64 `json:"coverage"`
	StructuralTestRatio float64  `json:"structural_test_ratio"`
	MeasurementSource   string   `json:"measurement_source"`
}

func NewTestQualityWall(cfg TestQualityConfig) *TestQualityWall {
	return &TestQualityWall{config: cfg}
}

func (w *TestQualityWall) AnalyzeTestQuality(code, tests string) *TestAnalysis {
	a := &TestAnalysis{}

	a.TestCount = strings.Count(tests, "func Test")

	a.HasTableDriven = strings.Contains(tests, "t.Run(")

	edgeKeywords := []string{"empty", "nil", "zero", "max", "overflow"}
	lower := strings.ToLower(tests)
	for _, kw := range edgeKeywords {
		if strings.Contains(lower, kw) {
			a.HasEdgeCases = true
			break
		}
	}

	errorKeywords := []string{"error", "fail", "invalid"}
	for _, kw := range errorKeywords {
		if strings.Contains(lower, kw) {
			a.HasErrorCases = true
			break
		}
	}

	concurrencyKeywords := []string{"Concurrent", "race", "parallel", "goroutine"}
	for _, kw := range concurrencyKeywords {
		if strings.Contains(tests, kw) {
			a.HasConcurrency = true
			break
		}
	}

	testLines := float64(len(strings.Split(tests, "\n")))
	codeLines := float64(len(strings.Split(code, "\n")))
	if strings.TrimSpace(tests) == "" {
		testLines = 0
	}
	if strings.TrimSpace(code) == "" {
		codeLines = 0
	}
	total := testLines + codeLines
	ratio := float64(0)
	if total > 0 {
		ratio = testLines / total
	}
	if ratio > 1.0 {
		ratio = 1.0
	}
	a.StructuralTestRatio = ratio
	a.MeasurementSource = "source_heuristics_only"

	return a
}

func (w *TestQualityWall) Check(ctx context.Context, code string, tests string) (*WallResult, error) {
	return w.check(ctx, code, tests, nil)
}

// CheckWithCoverage accepts a caller-supplied Go coverage-tool profile from
// the tested source revision. It never infers execution from source length.
// Callers are responsible for supplying the artifact from their actual run.
func (w *TestQualityWall) CheckWithCoverage(ctx context.Context, code, tests string, profile io.Reader) (*WallResult, error) {
	coverage, err := parseGoCoverage(ctx, profile)
	if err != nil {
		return nil, err
	}
	return w.check(ctx, code, tests, &coverage)
}

func (w *TestQualityWall) check(ctx context.Context, code, tests string, coverage *float64) (*WallResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !w.config.Enabled {
		return &WallResult{
			Wall:     WallTestQuality,
			Passed:   true,
			Severity: SeverityInfo,
			Message:  "Test quality wall disabled",
		}, nil
	}

	if strings.TrimSpace(tests) == "" {
		if w.config.RequireTests {
			return &WallResult{
				Wall:        WallTestQuality,
				Passed:      false,
				Severity:    SeverityBlock,
				Message:     "No tests provided",
				Suggestions: []string{"add tests for the implementation"},
			}, nil
		}
		return &WallResult{
			Wall:        WallTestQuality,
			Passed:      false,
			Severity:    SeverityWarning,
			Message:     "No tests provided",
			Suggestions: []string{"adding tests is recommended"},
		}, nil
	}

	analysis := w.AnalyzeTestQuality(code, tests)
	analysis.Coverage = coverage
	if coverage != nil {
		analysis.MeasurementSource = "go_coverprofile"
	}

	var warnings []string
	var suggestions []string

	if analysis.TestCount == 0 {
		warnings = append(warnings, "no test functions detected")
		suggestions = append(suggestions, "add at least one test function")
	}

	if math.IsNaN(w.config.MinCoverage) || math.IsInf(w.config.MinCoverage, 0) || w.config.MinCoverage < 0 || w.config.MinCoverage > 1 {
		return nil, fmt.Errorf("test quality: minimum coverage must be between 0 and 1")
	}
	if w.config.MinCoverage > 0 {
		if coverage == nil {
			warnings = append(warnings, "measured coverage unavailable; source length cannot satisfy a coverage requirement")
			suggestions = append(suggestions, "run tests with coverage instrumentation and supply the profile from the tested source revision")
		} else if *coverage < w.config.MinCoverage {
			warnings = append(warnings, fmt.Sprintf("measured statement coverage %.0f%% below target %.0f%%", *coverage*100, w.config.MinCoverage*100))
			suggestions = append(suggestions, "exercise uncovered statements with behavioral tests")
		}
	}

	if !analysis.HasEdgeCases && !analysis.HasErrorCases {
		warnings = append(warnings, "no edge case or error case tests detected")
		suggestions = append(suggestions, "add tests for nil inputs, empty strings, error conditions")
	}

	if !analysis.HasTableDriven {
		suggestions = append(suggestions, "consider table-driven tests for additional input cases")
	}

	if len(warnings) == 0 {
		return &WallResult{
			Wall:        WallTestQuality,
			Passed:      true,
			Severity:    SeverityInfo,
			Message:     fmt.Sprintf("Test source checks passed: %d test functions", analysis.TestCount),
			Details:     qualityDetails(analysis),
			Suggestions: suggestions,
		}, nil
	}

	severity := SeverityWarning
	if w.config.StrictMode {
		severity = SeverityBlock
	}
	return &WallResult{
		Wall:        WallTestQuality,
		Passed:      false,
		Severity:    severity,
		Message:     "Test quality issues detected",
		Details:     warnings,
		Suggestions: suggestions,
	}, nil
}

func qualityDetails(a *TestAnalysis) []string {
	details := []string{fmt.Sprintf("source heuristics: table-driven=%v edge-cases=%v error-cases=%v concurrent=%v; structural test-line ratio=%.2f (not coverage)", a.HasTableDriven, a.HasEdgeCases, a.HasErrorCases, a.HasConcurrency, a.StructuralTestRatio)}
	if a.Coverage == nil {
		details = append(details, "No tests were executed by this source check; behavioral verification and measured coverage remain separate.")
	} else {
		details = append(details, fmt.Sprintf("Caller-supplied Go profile: measured statement coverage %.2f%%", *a.Coverage*100))
	}
	return details
}
