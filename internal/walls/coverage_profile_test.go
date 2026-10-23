package walls

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
	"github.com/stretchr/testify/require"
)

const qualityTestSource = `package fixture
func TestExample(t *testing.T) { t.Run("empty input error",func(t *testing.T){}) }`

func TestLongTestSourceCannotSatisfyMeasuredCoverage(t *testing.T) {
	wall := NewTestQualityWall(TestQualityConfig{Enabled: true, MinCoverage: 0.8, StrictMode: true})
	tests := qualityTestSource + strings.Repeat("\n// empty nil error t.Run( pretend tests", 10000)
	analysis := wall.AnalyzeTestQuality("code", tests)
	require.Nil(t, analysis.Coverage)
	require.Greater(t, analysis.StructuralTestRatio, 0.99)
	result, err := wall.Check(t.Context(), "code", tests)
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Equal(t, SeverityBlock, result.Severity)
	require.Contains(t, strings.Join(result.Details, " "), "measured coverage unavailable")
}

func TestGoCoverageProfileValidationAndWeighting(t *testing.T) {
	profile := "mode: atomic\nfixture/a.go:1.1,2.2 3 0\nfixture/b.go:2.1,5.2 1 9\n"
	coverage, err := parseGoCoverage(t.Context(), strings.NewReader(profile))
	require.NoError(t, err)
	require.Equal(t, 0.25, coverage)
	coverage, err = parseGoCoverage(t.Context(), strings.NewReader(profile+"fixture/a.go:1.1,2.2 3 2\n"))
	require.NoError(t, err)
	require.Equal(t, 1.0, coverage, "duplicate package profiles merge execution without double-counting statements")
	for _, invalid := range []string{"", "mode: made-up\n", "mode: set\n", "mode: set\na.go:1.1,2.2 -1 1\n", "mode: set\na.go:1.1,2.2 1 -1\n", "mode: set\na.go:1,1,2,2 1 1\n", profile + "fixture/a.go:1.1,2.2 4 2\n"} {
		_, err := parseGoCoverage(t.Context(), strings.NewReader(invalid))
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = parseGoCoverage(ctx, strings.NewReader(profile))
	require.ErrorIs(t, err, context.Canceled)
}

func TestQualityUsesActualExecutedGoCoverageProfile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o600))
	code := "package fixture\nfunc Choose(b bool) int { if b { return 1 }; return 2 }\n"
	tests := "package fixture\nimport \"testing\"\nfunc TestChoose(t *testing.T) { t.Run(\"empty false error case\",func(t *testing.T){if Choose(false)!=2 { t.Fatal(\"wrong result\") }}) }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "choose.go"), []byte(code), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "choose_test.go"), []byte(tests), 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.Command("go", "test", "-count=1", "-coverprofile=coverage.out", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off")
	out, err := processrunner.CombinedOutput(ctx, cmd)
	require.NoError(t, err, string(out))
	bytes, err := os.ReadFile(filepath.Join(dir, "coverage.out"))
	require.NoError(t, err)
	measured, err := parseGoCoverage(t.Context(), strings.NewReader(string(bytes)))
	require.NoError(t, err)
	require.Greater(t, measured, 0.0)
	require.Less(t, measured, 1.0)
	wall := NewTestQualityWall(TestQualityConfig{Enabled: true, MinCoverage: 1, StrictMode: true})
	result, err := wall.CheckWithCoverage(t.Context(), code, tests, strings.NewReader(string(bytes)))
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Contains(t, strings.Join(result.Details, " "), "measured statement coverage")
	wall = NewTestQualityWall(TestQualityConfig{Enabled: true, MinCoverage: measured})
	result, err = wall.CheckWithCoverage(t.Context(), code, tests, strings.NewReader(string(bytes)))
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.Contains(t, strings.Join(result.Details, " "), "Caller-supplied Go profile")
}

func TestQualityRejectsInvalidCoverageThreshold(t *testing.T) {
	for _, value := range []float64{-1, 2, math.NaN(), math.Inf(1)} {
		wall := NewTestQualityWall(TestQualityConfig{Enabled: true, MinCoverage: value})
		_, err := wall.Check(t.Context(), "code", qualityTestSource)
		require.ErrorContains(t, err, "minimum coverage")
	}
}

func TestCoveragePreservesDistinctRealSourceFilenamesWithSpaces(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module fixture\n\ngo 1.26\n",
		"a b.go":        "package fixture\nfunc One() int { return 1 }\n",
		"a  b.go":       "package fixture\nfunc Two() int { return 2 }\n",
		"cover_test.go": "package fixture\nimport \"testing\"\nfunc TestCoverage(t *testing.T){ t.Run(\"empty error case\",func(t *testing.T){if One()!=1 {t.Fatal(\"wrong value\")}}) }\n",
	}
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.Command("go", "test", "-count=1", "-coverprofile=coverage.out", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off")
	out, err := processrunner.CombinedOutput(ctx, cmd)
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "50.0%")
	bytes, err := os.ReadFile(filepath.Join(dir, "coverage.out"))
	require.NoError(t, err)
	require.Contains(t, string(bytes), "fixture/a b.go:")
	require.Contains(t, string(bytes), "fixture/a  b.go:")
	coverage, err := parseGoCoverage(t.Context(), strings.NewReader(string(bytes)))
	require.NoError(t, err)
	require.Equal(t, 0.5, coverage)
	wall := NewTestQualityWall(TestQualityConfig{Enabled: true, MinCoverage: 0.8})
	result, err := wall.CheckWithCoverage(t.Context(), files["a b.go"]+files["a  b.go"], files["cover_test.go"], strings.NewReader(string(bytes)))
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Contains(t, strings.Join(result.Details, " "), "50% below target 80%")
}
