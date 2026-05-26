package walls

import (
	"context"
	"fmt"
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
	TestCount      int     `json:"test_count"`
	HasTableDriven bool    `json:"has_table_driven"`
	HasEdgeCases   bool    `json:"has_edge_cases"`
	HasErrorCases  bool    `json:"has_error_cases"`
	HasConcurrency bool    `json:"has_concurrency"`
	Coverage       float64 `json:"coverage"`
}

func NewTestQualityWall(cfg TestQualityConfig) *TestQualityWall {
	return &TestQualityWall{config: cfg}
}

func (w *TestQualityWall) AnalyzeTestQuality(tests string) *TestAnalysis {
	a := &TestAnalysis{}

	a.TestCount = strings.Count(tests, "func Test")
	if a.TestCount == 0 {
		a.TestCount = strings.Count(tests, "func Test")
	}

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
	codeLines := testLines
	ratio := testLines / (testLines + codeLines)
	if ratio > 1.0 {
		ratio = 1.0
	}
	a.Coverage = ratio

	return a
}

func (w *TestQualityWall) Check(_ context.Context, code string, tests string) (*WallResult, error) {
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

	analysis := w.AnalyzeTestQuality(tests)

	var warnings []string
	var suggestions []string

	if analysis.TestCount == 0 {
		warnings = append(warnings, "no test functions detected")
		suggestions = append(suggestions, "add at least one test function")
	}

	if analysis.Coverage < w.config.MinCoverage {
		warnings = append(warnings, fmt.Sprintf("estimated coverage %.0f%% below target %.0f%%", analysis.Coverage*100, w.config.MinCoverage*100))
		suggestions = append(suggestions, "add more test cases to improve coverage")
	}

	if !analysis.HasEdgeCases && !analysis.HasErrorCases {
		warnings = append(warnings, "no edge case or error case tests detected")
		suggestions = append(suggestions, "add tests for nil inputs, empty strings, error conditions")
	}

	if !analysis.HasTableDriven {
		suggestions = append(suggestions, "consider table-driven tests for better coverage")
	}

	if len(warnings) == 0 {
		return &WallResult{
			Wall:        WallTestQuality,
			Passed:      true,
			Severity:    SeverityInfo,
			Message:     fmt.Sprintf("Test quality passed: %d tests, coverage ~%.0f%%", analysis.TestCount, analysis.Coverage*100),
			Details:     []string{fmt.Sprintf("table-driven=%v edge-cases=%v error-cases=%v concurrent=%v", analysis.HasTableDriven, analysis.HasEdgeCases, analysis.HasErrorCases, analysis.HasConcurrency)},
			Suggestions: suggestions,
		}, nil
	}

	return &WallResult{
		Wall:        WallTestQuality,
		Passed:      false,
		Severity:    SeverityWarning,
		Message:     "Test quality issues detected",
		Details:     warnings,
		Suggestions: suggestions,
	}, nil
}
