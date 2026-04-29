package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
)

func TestStatus_String(t *testing.T) {
	assert.Equal(t, "ok", StatusOK.String())
	assert.Equal(t, "warn", StatusWarn.String())
	assert.Equal(t, "fail", StatusFail.String())
	assert.Equal(t, "fixed", StatusFixed.String())
	assert.Equal(t, "unknown", Status(99).String())
}

func TestDoctor_Run_AllChecks(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	d := NewDoctor(tmpDir)
	report := d.Run(context.Background())

	assert.Equal(t, 10, report.Total)
	assert.Equal(t, len(report.Results), report.Total)
}

func TestDoctor_RunCheck_Single(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	d := NewDoctor(tmpDir)
	result := d.RunCheck(context.Background(), "git-installed")

	assert.Equal(t, CheckID("git-installed"), result.ID)
	assert.Equal(t, StatusOK, result.Status)
}

func TestDoctor_RunCheck_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	d := NewDoctor(tmpDir)
	result := d.RunCheck(context.Background(), "nonexistent")

	assert.Equal(t, CheckID("nonexistent"), result.ID)
	assert.Equal(t, StatusFail, result.Status)
}

func TestDoctor_RunAndFix(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	d := NewDoctor(tmpDir)
	report := d.RunAndFix(context.Background())

	assert.Equal(t, 10, report.Total)

	for _, r := range report.Results {
		if r.ID == "data-dir" || r.ID == "sessions-dir" || r.ID == "skills-dir" ||
			r.ID == "memories-dir" || r.ID == "journal-dir" {
			assert.True(t, r.Status == StatusOK || r.Status == StatusFixed,
				"check %s should be ok or fixed, got %s", r.ID, r.Status)
		}
	}
}

func TestCheck_ConfigExists_Missing(t *testing.T) {
	tmpDir := t.TempDir()
	d := NewDoctor(tmpDir)
	result := d.RunCheck(context.Background(), "config-exists")

	assert.Equal(t, StatusFail, result.Status)
}

func TestCheck_ConfigExists_Present(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	d := NewDoctor(tmpDir)
	result := d.RunCheck(context.Background(), "config-exists")

	assert.Equal(t, StatusOK, result.Status)
}

func TestCheck_DataDir(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	d := NewDoctor(tmpDir)

	result := d.RunCheck(context.Background(), "data-dir")
	assert.Equal(t, StatusFail, result.Status)

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "data"), 0o755))
	result = d.RunCheck(context.Background(), "data-dir")
	assert.Equal(t, StatusOK, result.Status)
}

func TestCheck_SessionsDir(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	d := NewDoctor(tmpDir)

	result := d.RunCheck(context.Background(), "sessions-dir")
	assert.Equal(t, StatusFail, result.Status)

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "data", "sessions"), 0o755))
	result = d.RunCheck(context.Background(), "sessions-dir")
	assert.Equal(t, StatusOK, result.Status)
}

func TestCheck_GitInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	d := NewDoctor(tmpDir)
	result := d.RunCheck(context.Background(), "git-installed")

	assert.Equal(t, StatusOK, result.Status)
}

func TestDoctor_RegisterCustomCheck(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	d := NewDoctor(tmpDir)
	d.RegisterCheck(Check{
		ID:   "custom-check",
		Name: "Custom test check",
		Fn: func(ctx context.Context) CheckResult {
			return CheckResult{
				Status:  StatusOK,
				Message: "custom check passed",
			}
		},
	})

	result := d.RunCheck(context.Background(), "custom-check")
	assert.Equal(t, CheckID("custom-check"), result.ID)
	assert.Equal(t, StatusOK, result.Status)
	assert.Equal(t, "custom check passed", result.Message)

	report := d.Run(context.Background())
	assert.Equal(t, 11, report.Total)
}

func TestDoctor_Report(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	dirs := []string{
		filepath.Join(tmpDir, "data"),
		filepath.Join(tmpDir, "data", "sessions"),
		filepath.Join(tmpDir, "skills"),
		filepath.Join(tmpDir, "data", "memories"),
		filepath.Join(tmpDir, "data", "journal"),
	}
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	d := NewDoctor(tmpDir)
	report := d.Run(context.Background())

	assert.Equal(t, 10, report.Total)
	assert.Equal(t, report.Passed+report.Failed, report.Total)

	for _, r := range report.Results {
		if r.Status == StatusOK || r.Status == StatusWarn {
			assert.Contains(t, []Status{StatusOK, StatusWarn}, r.Status)
		}
	}
}

func TestFixes_CreateMissingDirs(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, cfg.Save(cfgPath))

	d := NewDoctor(tmpDir)
	report := d.RunAndFix(context.Background())

	expectedDirs := []string{
		filepath.Join(tmpDir, "data"),
		filepath.Join(tmpDir, "data", "sessions"),
		filepath.Join(tmpDir, "skills"),
		filepath.Join(tmpDir, "data", "memories"),
		filepath.Join(tmpDir, "data", "journal"),
	}

	for _, dir := range expectedDirs {
		info, err := os.Stat(dir)
		assert.NoError(t, err, "directory %s should exist after RunAndFix", dir)
		if err == nil {
			assert.True(t, info.IsDir(), "%s should be a directory", dir)
		}
	}

	assert.GreaterOrEqual(t, report.Fixed, 5, "at least 5 dirs should have been fixed")
}

func TestFixConfigExists_CreatesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	_ = config.Default()
	result := fixConfigExists(tmpDir)

	assert.Equal(t, StatusFixed, result.Status)
	assert.True(t, result.Fixed)

	cfgPath := filepath.Join(tmpDir, "config.toml")
	_, err := os.Stat(cfgPath)
	assert.NoError(t, err, "config file should have been created")
}

func TestFixConfigVersion_Migrates(t *testing.T) {
	tmpDir := t.TempDir()

	oldCfg := &config.Config{Version: 0}
	cfgPath := filepath.Join(tmpDir, "config.toml")
	saveErr := oldCfg.Save(cfgPath)
	require.NoError(t, saveErr)

	result := fixConfigVersion(tmpDir)
	assert.Equal(t, StatusFixed, result.Status)
	assert.True(t, result.Fixed)
}
