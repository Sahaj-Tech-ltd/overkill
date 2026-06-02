package walls

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func TestOuroboros_Disabled(t *testing.T) {
	w := NewOuroborosWall(OuroborosConfig{Enabled: false})
	res, err := w.Check(context.Background(), "code", "spec")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Passed {
		t.Error("should pass when disabled")
	}
	if res.Severity != SeverityInfo {
		t.Errorf("expected info severity, got %v", res.Severity)
	}
	if res.Wall != WallOuroboros {
		t.Errorf("expected WallOuroboros, got %v", res.Wall)
	}
}

func TestOuroboros_NoProvider(t *testing.T) {
	w := NewOuroborosWall(OuroborosConfig{Enabled: true})
	res, err := w.Check(context.Background(), "code", "spec")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Error("should not pass without provider")
	}
	if res.Severity != SeverityWarning {
		t.Errorf("expected warning severity, got %v", res.Severity)
	}
}

func TestOuroboros_Enabled(t *testing.T) {
	expected := ouroborosResponse{
		Severity:    "warning",
		Issues:      []string{"potential nil dereference"},
		Suggestions: []string{"add nil check"},
	}
	respJSON, _ := json.Marshal(expected)

	mock := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{Content: string(respJSON)}, nil
	})

	w := NewOuroborosWall(OuroborosConfig{
		Enabled:  true,
		Provider: mock,
		Model:    "test-model",
	})

	res, err := w.Check(context.Background(), "var x *int; *x = 5", "do something unsafe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Error("should not pass with warning issues")
	}
	if len(res.Details) != 1 {
		t.Errorf("expected 1 detail, got %d", len(res.Details))
	}
	if len(res.Suggestions) != 1 {
		t.Errorf("expected 1 suggestion, got %d", len(res.Suggestions))
	}
}

func TestOuroboros_ContextCancelled(t *testing.T) {
	mock := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{}, context.Canceled
	})

	w := NewOuroborosWall(OuroborosConfig{
		Enabled:  true,
		Provider: mock,
		Model:    "test-model",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := w.Check(ctx, "code", "spec")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestArchitecture_Check_Clean(t *testing.T) {
	w := NewArchitectureWall(ArchitectureConfig{Enabled: true})
	res, err := w.Check(context.Background(), map[string]string{
		"main.go": "package main\nfunc main() {}",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Passed {
		t.Error("should pass with clean code")
	}
	if res.Wall != WallArchitecture {
		t.Errorf("expected WallArchitecture, got %v", res.Wall)
	}
}

func TestArchitecture_Check_Violation(t *testing.T) {
	w := NewArchitectureWall(ArchitectureConfig{Enabled: true})
	res, err := w.Check(context.Background(), map[string]string{
		"handler.go": `password := "supersecret"`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Error("should not pass with hardcoded secret")
	}
	if res.Severity != SeverityWarning {
		t.Errorf("expected warning, got %v", res.Severity)
	}
	if len(res.Details) == 0 {
		t.Error("expected violation details")
	}
}

func TestArchitecture_StrictMode(t *testing.T) {
	w := NewArchitectureWall(ArchitectureConfig{Enabled: true, StrictMode: true})
	res, err := w.Check(context.Background(), map[string]string{
		"handler.go": `password := "supersecret"`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Error("should not pass in strict mode with violation")
	}
	if res.Severity != SeverityBlock {
		t.Errorf("expected block in strict mode, got %v", res.Severity)
	}
}

func TestArchitecture_LoadRules(t *testing.T) {
	content := "custom-rule|No foo patterns|foo\\d+|warning\n# comment\n"
	tmp, err := os.CreateTemp("", "arch-rules-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	w := NewArchitectureWall(ArchitectureConfig{Enabled: true})
	if err := w.LoadRules(tmp.Name()); err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}

	found := false
	for _, r := range w.archRules {
		if r.ID == "custom-rule" {
			found = true
			if r.Description != "No foo patterns" {
				t.Errorf("unexpected description: %s", r.Description)
			}
		}
	}
	if !found {
		t.Error("custom rule not loaded")
	}
}

func TestArchitecture_DBAccessInRepoFile(t *testing.T) {
	w := NewArchitectureWall(ArchitectureConfig{Enabled: true})
	res, err := w.Check(context.Background(), map[string]string{
		"store.go":   "sql.Open(\"postgres\", \"...\")",
		"handler.go": "sql.Open(\"postgres\", \"...\")",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Error("handler.go should trigger DB access violation even if store.go is allowed")
	}
	dbViolation := false
	for _, d := range res.Details {
		if strings.Contains(d, "handler.go") && strings.Contains(d, "no-direct-db-access") {
			dbViolation = true
		}
	}
	if !dbViolation {
		t.Error("expected DB access violation in handler.go")
	}
	storeAllowed := true
	for _, d := range res.Details {
		if strings.Contains(d, "store.go") && strings.Contains(d, "no-direct-db-access") {
			storeAllowed = false
		}
	}
	if !storeAllowed {
		t.Error("store.go should be exempt from DB access rule")
	}
}

func TestTestQuality_NoTests_RequireTests(t *testing.T) {
	w := NewTestQualityWall(TestQualityConfig{
		Enabled:      true,
		RequireTests: true,
	})
	res, err := w.Check(context.Background(), "code", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Error("should not pass when RequireTests and no tests")
	}
	if res.Severity != SeverityBlock {
		t.Errorf("expected block, got %v", res.Severity)
	}
}

func TestTestQuality_NoTests_WarnOnly(t *testing.T) {
	w := NewTestQualityWall(TestQualityConfig{
		Enabled:      true,
		RequireTests: false,
	})
	res, err := w.Check(context.Background(), "code", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Error("should not pass without tests")
	}
	if res.Severity != SeverityWarning {
		t.Errorf("expected warning, got %v", res.Severity)
	}
}

func TestTestQuality_GoodTests(t *testing.T) {
	tests := `package walls_test

func TestFoo(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {})
	t.Run("nil input", func(t *testing.T) {})
}

func TestBar(t *testing.T) {
	t.Run("error case", func(t *testing.T) {
		if err != nil { t.Fatal("fail") }
	})
	t.Run("overflow case", func(t *testing.T) {})
}

func TestConcurrent(t *testing.T) {}
`

	w := NewTestQualityWall(TestQualityConfig{
		Enabled:     true,
		MinCoverage: 0.1,
	})
	res, err := w.Check(context.Background(), "some code", tests)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Passed {
		t.Errorf("should pass with good tests: %s", res.Message)
	}
	if res.Wall != WallTestQuality {
		t.Errorf("expected WallTestQuality, got %v", res.Wall)
	}
}

func TestTestQuality_MissingEdgeCases(t *testing.T) {
	tests := `package walls_test
func TestBasic(t *testing.T) {
	if 1+1 != 2 { t.Fatal("bad") }
}
`
	w := NewTestQualityWall(TestQualityConfig{
		Enabled:     true,
		MinCoverage: 0.1,
	})
	res, err := w.Check(context.Background(), "some code", tests)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Error("should warn about missing edge/error cases")
	}
	if res.Severity != SeverityWarning {
		t.Errorf("expected warning, got %v", res.Severity)
	}
}

func TestTestQuality_AnalyzeTestQuality(t *testing.T) {
	w := NewTestQualityWall(TestQualityConfig{Enabled: true})

	code := `package foo
func DoThing() error { return nil }`

	tests := `package foo_test
func TestOne(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {})
	t.Run("nil input", func(t *testing.T) {})
	if err != nil { t.Fatal("fail") }
}
func TestTwo(t *testing.T) {
	t.Run("Concurrent access", func(t *testing.T) {})
}
func TestThree(t *testing.T) {}
`

	a := w.AnalyzeTestQuality(code, tests)

	if a.TestCount != 3 {
		t.Errorf("expected 3 tests, got %d", a.TestCount)
	}
	if !a.HasTableDriven {
		t.Error("expected HasTableDriven")
	}
	if !a.HasEdgeCases {
		t.Error("expected HasEdgeCases (empty, nil)")
	}
	if !a.HasErrorCases {
		t.Error("expected HasErrorCases (err, fail)")
	}
	if !a.HasConcurrency {
		t.Error("expected HasConcurrency (Concurrent)")
	}
	if a.Coverage <= 0 || a.Coverage > 1 {
		t.Errorf("expected coverage in (0,1], got %f", a.Coverage)
	}
}

func TestTestQuality_Disabled(t *testing.T) {
	w := NewTestQualityWall(TestQualityConfig{Enabled: false})
	res, err := w.Check(context.Background(), "code", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Passed {
		t.Error("should pass when disabled")
	}
	if res.Severity != SeverityInfo {
		t.Errorf("expected info, got %v", res.Severity)
	}
}

func TestWallReport_AllPass(t *testing.T) {
	report := NewReport([]WallResult{
		{Wall: WallOuroboros, Passed: true, Severity: SeverityInfo},
		{Wall: WallArchitecture, Passed: true, Severity: SeverityInfo},
		{Wall: WallTestQuality, Passed: true, Severity: SeverityInfo},
	})
	if !report.Passed {
		t.Error("report should pass when all walls pass")
	}
	if report.Blocked {
		t.Error("report should not be blocked when all pass")
	}
}

func TestWallReport_OneBlocked(t *testing.T) {
	report := NewReport([]WallResult{
		{Wall: WallOuroboros, Passed: true, Severity: SeverityInfo},
		{Wall: WallArchitecture, Passed: false, Severity: SeverityBlock},
		{Wall: WallTestQuality, Passed: true, Severity: SeverityInfo},
	})
	if report.Passed {
		t.Error("report should not pass when one wall blocks")
	}
	if !report.Blocked {
		t.Error("report should be blocked when one wall has block severity")
	}
}

func TestWallReport_WarningNotBlocked(t *testing.T) {
	report := NewReport([]WallResult{
		{Wall: WallArchitecture, Passed: false, Severity: SeverityWarning},
	})
	if report.Passed {
		t.Error("should not pass with warning")
	}
	if report.Blocked {
		t.Error("warning should not cause block")
	}
}

func TestWallID_String(t *testing.T) {
	tests := []struct {
		id       WallID
		expected string
	}{
		{WallOuroboros, "ouroboros"},
		{WallArchitecture, "architecture"},
		{WallTestQuality, "test-quality"},
		{WallID(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.id.String(); got != tt.expected {
			t.Errorf("WallID(%d).String() = %q, want %q", tt.id, got, tt.expected)
		}
	}
}

func TestSeverity_String(t *testing.T) {
	tests := []struct {
		s        Severity
		expected string
	}{
		{SeverityInfo, "info"},
		{SeverityWarning, "warning"},
		{SeverityBlock, "block"},
		{Severity(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.expected {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.s, got, tt.expected)
		}
	}
}
