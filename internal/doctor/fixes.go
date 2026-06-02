package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

func fixConfigExists(configDir string) CheckResult {
	path := filepath.Join(configDir, "config.toml")
	if _, err := os.Stat(path); err == nil {
		return CheckResult{
			Status:  StatusOK,
			Message: "config file already exists",
		}
	}
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: cannot create config directory: %v", err),
		}
	}
	cfg := config.Default()
	if err := cfg.Save(path); err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: cannot create default config: %v", err),
		}
	}
	return CheckResult{
		Status:     StatusFixed,
		Message:    "created default config file",
		Fixed:      true,
		FixApplied: fmt.Sprintf("created default config at %s", path),
	}
}

func fixConfigParseable(configDir string) CheckResult {
	path := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return fixConfigExists(configDir)
	}
	var cfg config.Config
	if err := toml.Unmarshal(data, &cfg); err == nil {
		return CheckResult{
			Status:  StatusOK,
			Message: "config file is already parseable",
		}
	}

	// B059: back up the broken config before replacing with defaults.
	// Use timestamped backup to avoid overwriting previous backups.
	bakPath := path + ".bak." + fmt.Sprintf("%d", time.Now().Unix())
	if bakErr := os.WriteFile(bakPath, data, 0o600); bakErr != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: cannot back up config before reset: %v", bakErr),
		}
	}

	defaultCfg := config.Default()
	if err := defaultCfg.Save(path); err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: cannot reset config to defaults: %v", err),
		}
	}

	parseErr := err.Error()
	return CheckResult{
		Status:  StatusFixed,
		Message: fmt.Sprintf("reset unparseable config to defaults — parse error: %s", parseErr),
		Fixed:   true,
		FixApplied: fmt.Sprintf(
			"Replaced unparseable config with defaults. Your original config is backed up at:\n"+
				"  %s\n"+
				"  Parse error: %s\n"+
				"  To recover: diff %s %s and re-apply your custom settings.",
			bakPath, parseErr, bakPath, path,
		),
	}
}

func fixConfigVersion(configDir string) CheckResult {
	path := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return fixConfigExists(configDir)
	}
	var cfg config.Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return fixConfigParseable(configDir)
	}
	if cfg.Version == config.CurrentVersion {
		return CheckResult{
			Status:  StatusOK,
			Message: "config version is already current",
		}
	}
	migrated, _, err := cfg.Migrate()
	if err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: config migration failed: %v", err),
		}
	}
	if err := migrated.Save(path); err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: cannot save migrated config: %v", err),
		}
	}
	return CheckResult{
		Status:     StatusFixed,
		Message:    fmt.Sprintf("migrated config from version %d to %d", cfg.Version, config.CurrentVersion),
		Fixed:      true,
		FixApplied: fmt.Sprintf("migrated config to version %d", config.CurrentVersion),
	}
}

func fixDataDir(configDir string) CheckResult {
	return fixMissingDir(filepath.Join(configDir, "data"), "data directory")
}

func fixSessionsDir(configDir string) CheckResult {
	return fixMissingDir(filepath.Join(configDir, "data", "sessions"), "sessions directory")
}

func fixSkillsDir(configDir string) CheckResult {
	return fixMissingDir(filepath.Join(configDir, "skills"), "skills directory")
}

func fixMemoriesDir(configDir string) CheckResult {
	return fixMissingDir(filepath.Join(configDir, "data", "memories"), "memories directory")
}

func fixJournalDir(configDir string) CheckResult {
	return fixMissingDir(filepath.Join(configDir, "data", "journal"), "journal directory")
}

func fixMissingDir(path, label string) CheckResult {
	if _, err := os.Stat(path); err == nil {
		return CheckResult{
			Status:  StatusOK,
			Message: fmt.Sprintf("%s already exists", label),
		}
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: cannot create %s at %s: %v", label, path, err),
		}
	}
	return CheckResult{
		Status:     StatusFixed,
		Message:    fmt.Sprintf("created %s", label),
		Fixed:      true,
		FixApplied: fmt.Sprintf("created %s at %s", label, path),
	}
}
