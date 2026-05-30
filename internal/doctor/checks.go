package doctor

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

type Check struct {
	ID   CheckID
	Name string
	Fn   func(ctx context.Context) CheckResult
}

type Doctor struct {
	checks    []Check
	configDir string
}

func NewDoctor(configDir string) *Doctor {
	d := &Doctor{
		configDir: configDir,
	}

	d.RegisterCheck(Check{
		ID:   "config-exists",
		Name: "Config file exists",
		Fn: func(ctx context.Context) CheckResult {
			return checkConfigExists(configDir)
		},
	})

	d.RegisterCheck(Check{
		ID:   "config-parseable",
		Name: "Config file is parseable",
		Fn: func(ctx context.Context) CheckResult {
			return checkConfigParseable(configDir)
		},
	})

	d.RegisterCheck(Check{
		ID:   "config-version",
		Name: "Config version is current",
		Fn: func(ctx context.Context) CheckResult {
			return checkConfigVersion(configDir)
		},
	})

	d.RegisterCheck(Check{
		ID:   "data-dir",
		Name: "Data directory exists",
		Fn: func(ctx context.Context) CheckResult {
			return checkDir(filepath.Join(configDir, "data"), "data directory")
		},
	})

	d.RegisterCheck(Check{
		ID:   "sessions-dir",
		Name: "Sessions directory exists",
		Fn: func(ctx context.Context) CheckResult {
			return checkDir(filepath.Join(configDir, "data", "sessions"), "sessions directory")
		},
	})

	d.RegisterCheck(Check{
		ID:   "skills-dir",
		Name: "Skills directory exists",
		Fn: func(ctx context.Context) CheckResult {
			return checkDir(filepath.Join(configDir, "skills"), "skills directory")
		},
	})

	d.RegisterCheck(Check{
		ID:   "memories-dir",
		Name: "Memories directory exists",
		Fn: func(ctx context.Context) CheckResult {
			return checkDir(filepath.Join(configDir, "data", "memories"), "memories directory")
		},
	})

	d.RegisterCheck(Check{
		ID:   "journal-dir",
		Name: "Journal directory exists",
		Fn: func(ctx context.Context) CheckResult {
			return checkDir(filepath.Join(configDir, "data", "journal"), "journal directory")
		},
	})

	d.RegisterCheck(Check{
		ID:   "provider-keys",
		Name: "At least one provider API key configured",
		Fn: func(ctx context.Context) CheckResult {
			return checkProviderKeys(configDir)
		},
	})

	d.RegisterCheck(Check{
		ID:   "git-installed",
		Name: "Git is available on PATH",
		Fn: func(ctx context.Context) CheckResult {
			return checkGitInstalled()
		},
	})

	return d
}

func (d *Doctor) RegisterCheck(check Check) {
	d.checks = append(d.checks, check)
}

func (d *Doctor) Run(ctx context.Context) *Report {
	report := &Report{}
	for _, c := range d.checks {
		result := c.Fn(ctx)
		result.ID = c.ID
		result.Name = c.Name
		report.Results = append(report.Results, result)
	}
	report.Total = len(report.Results)
	for _, r := range report.Results {
		switch {
		case r.Status == StatusOK:
			report.Passed++
		case r.Status == StatusFail:
			report.Failed++
		case r.Status == StatusFixed:
			report.Fixed++
			report.Passed++
		case r.Status == StatusWarn:
			report.Passed++
		}
	}
	return report
}

func (d *Doctor) RunCheck(ctx context.Context, id CheckID) *CheckResult {
	for _, c := range d.checks {
		if c.ID == id {
			result := c.Fn(ctx)
			result.ID = c.ID
			result.Name = c.Name
			return &result
		}
	}
	return &CheckResult{
		ID:      id,
		Status:  StatusFail,
		Message: fmt.Sprintf("doctor: check %q not found", id),
	}
}

func (d *Doctor) RunAndFix(ctx context.Context) *Report {
	report := d.Run(ctx)

	for i, r := range report.Results {
		if r.Status != StatusFail {
			continue
		}
		fixed := d.tryFix(r.ID)
		if fixed != nil {
			report.Results[i] = *fixed
		}
	}

	report.Passed = 0
	report.Failed = 0
	report.Fixed = 0
	for _, r := range report.Results {
		switch {
		case r.Status == StatusOK:
			report.Passed++
		case r.Status == StatusFail:
			report.Failed++
		case r.Status == StatusFixed:
			report.Fixed++
			report.Passed++
		case r.Status == StatusWarn:
			report.Passed++
		}
	}

	return report
}

func (d *Doctor) tryFix(id CheckID) *CheckResult {
	switch id {
	case "config-exists":
		r := fixConfigExists(d.configDir)
		r.ID = id
		return &r
	case "config-parseable":
		r := fixConfigParseable(d.configDir)
		r.ID = id
		return &r
	case "config-version":
		r := fixConfigVersion(d.configDir)
		r.ID = id
		return &r
	case "data-dir":
		r := fixDataDir(d.configDir)
		r.ID = id
		return &r
	case "sessions-dir":
		r := fixSessionsDir(d.configDir)
		r.ID = id
		return &r
	case "skills-dir":
		r := fixSkillsDir(d.configDir)
		r.ID = id
		return &r
	case "memories-dir":
		r := fixMemoriesDir(d.configDir)
		r.ID = id
		return &r
	case "journal-dir":
		r := fixJournalDir(d.configDir)
		r.ID = id
		return &r
	default:
		return nil
	}
}

func checkConfigExists(configDir string) CheckResult {
	path := filepath.Join(configDir, "config.toml")
	if _, err := os.Stat(path); err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: config file not found at %s", path),
		}
	}
	return CheckResult{
		Status:  StatusOK,
		Message: "config file exists",
	}
}

func checkConfigParseable(configDir string) CheckResult {
	path := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: cannot read config file: %v", err),
		}
	}
	var cfg config.Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: config parse error: %v", err),
		}
	}
	return CheckResult{
		Status:  StatusOK,
		Message: "config file is valid TOML",
	}
}

func checkConfigVersion(configDir string) CheckResult {
	path := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: cannot read config file: %v", err),
		}
	}
	var cfg config.Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: cannot parse config for version check: %v", err),
		}
	}
	if cfg.Version != config.CurrentVersion {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: config version %d does not match app version %d", cfg.Version, config.CurrentVersion),
		}
	}
	return CheckResult{
		Status:  StatusOK,
		Message: fmt.Sprintf("config version is %d", cfg.Version),
	}
}

func checkDir(path, label string) CheckResult {
	info, err := os.Stat(path)
	if err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: %s not found at %s", label, path),
		}
	}
	if !info.IsDir() {
		return CheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("doctor: %s path exists but is not a directory: %s", label, path),
		}
	}
	// B055: Use PID + random suffix to avoid collisions when two
	// doctor runs race against each other.
	tmpFile := filepath.Join(path, probeFilename())
	if err := os.WriteFile(tmpFile, []byte("test"), 0o644); err != nil {
		return CheckResult{
			Status:  StatusWarn,
			Message: fmt.Sprintf("doctor: %s exists but is not writable: %v", label, err),
		}
	}
	os.Remove(tmpFile)
	return CheckResult{
		Status:  StatusOK,
		Message: fmt.Sprintf("%s exists and is writable", label),
	}
}

func checkProviderKeys(configDir string) CheckResult {
	path := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return CheckResult{
			Status:  StatusWarn,
			Message: "doctor: cannot read config file to check provider keys",
		}
	}
	var cfg config.Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return CheckResult{
			Status:  StatusWarn,
			Message: "doctor: cannot parse config to check provider keys",
		}
	}
	if len(cfg.Providers) == 0 {
		return CheckResult{
			Status:  StatusWarn,
			Message: "doctor: no providers configured",
		}
	}
	hasKey := false
	for _, p := range cfg.Providers {
		if p.APIKey != "" {
			hasKey = true
			break
		}
	}
	if !hasKey {
		return CheckResult{
			Status:  StatusWarn,
			Message: "doctor: no provider API keys configured",
		}
	}
	return CheckResult{
		Status:  StatusOK,
		Message: "at least one provider API key is configured",
	}
}

func checkGitInstalled() CheckResult {
	_, err := exec.LookPath("git")
	if err != nil {
		return CheckResult{
			Status:  StatusFail,
			Message: "doctor: git not found on PATH",
		}
	}
	return CheckResult{
		Status:  StatusOK,
		Message: "git is available",
	}
}

// probeFilename returns a unique temp probe name (B055). Uses PID + random
// suffix so concurrent doctor runs don't collide on the same filename.
func probeFilename() string {
	return fmt.Sprintf(".doctor-probe-%d-%x", os.Getpid(), rand.Int63())
}
