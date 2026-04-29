package walls

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type ArchitectureConfig struct {
	ArchFile   string
	Enabled    bool
	StrictMode bool
}

type ArchRule struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Pattern     string `json:"pattern"`
	Severity    Severity `json:"severity"`
}

type ArchitectureWall struct {
	config    ArchitectureConfig
	archRules []ArchRule
}

func NewArchitectureWall(cfg ArchitectureConfig) *ArchitectureWall {
	w := &ArchitectureWall{
		config: cfg,
		archRules: builtinArchRules(),
	}
	return w
}

func builtinArchRules() []ArchRule {
	return []ArchRule{
		{
			ID:          "no-sync-in-async",
			Description: "No sync endpoints in async system",
			Pattern:     `http\.HandleFunc.*Sync`,
			Severity:    SeverityWarning,
		},
		{
			ID:          "no-direct-db-access",
			Description: "No direct DB access outside repository layer",
			Pattern:     `badger\.Open`,
			Severity:    SeverityWarning,
		},
		{
			ID:          "no-hardcoded-secrets",
			Description: "No hardcoded secrets",
			Pattern:     `(?i)(password|secret|api_key)\s*(?::=|=|:)\s*"[^"]+"`,
			Severity:    SeverityWarning,
		},
		{
			ID:          "no-bare-errors",
			Description: "No catch-all error handling without context wrapping",
			Pattern:     `if err != nil \{\s*\n?\s*return err`,
			Severity:    SeverityInfo,
		},
	}
}

func (w *ArchitectureWall) LoadRules(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("walls: failed to load arch rules: %w", err)
	}

	var rules []ArchRule
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 3 {
			continue
		}
		severity := SeverityWarning
		if len(parts) >= 4 {
			switch strings.TrimSpace(parts[3]) {
			case "info":
				severity = SeverityInfo
			case "block":
				severity = SeverityBlock
			}
		}
		rules = append(rules, ArchRule{
			ID:          strings.TrimSpace(parts[0]),
			Description: strings.TrimSpace(parts[1]),
			Pattern:     strings.TrimSpace(parts[2]),
			Severity:    severity,
		})
	}

	w.archRules = append(w.archRules, rules...)
	return nil
}

func (w *ArchitectureWall) Check(_ context.Context, files map[string]string) (*WallResult, error) {
	if !w.config.Enabled {
		return &WallResult{
			Wall:     WallArchitecture,
			Passed:   true,
			Severity: SeverityInfo,
			Message:  "Architecture wall disabled",
		}, nil
	}

	var violations []string
	var details []string

	for filename, content := range files {
		for _, rule := range w.archRules {
			re, err := regexp.Compile(rule.Pattern)
			if err != nil {
				continue
			}

			matches := re.FindAllString(content, -1)
			if len(matches) > 0 {
				if rule.ID == "no-direct-db-access" {
					if isRepoFile(filename) {
						continue
					}
				}

				violations = append(violations, rule.ID)
				for _, m := range matches {
					details = append(details, fmt.Sprintf("%s: %s in %s", rule.ID, truncate(m, 80), filename))
				}
			}
		}
	}

	if len(violations) == 0 {
		return &WallResult{
			Wall:     WallArchitecture,
			Passed:   true,
			Severity: SeverityInfo,
			Message:  "Architecture check passed",
		}, nil
	}

	severity := SeverityWarning
	if w.config.StrictMode {
		severity = SeverityBlock
	}

	return &WallResult{
		Wall:        WallArchitecture,
		Passed:      false,
		Severity:    severity,
		Message:     fmt.Sprintf("Architecture violations detected: %d issue(s)", len(details)),
		Details:     details,
		Suggestions: []string{"review architecture guidelines and fix violations"},
	}, nil
}

func isRepoFile(filename string) bool {
	base := filename
	if idx := strings.LastIndex(filename, "/"); idx >= 0 {
		base = filename[idx+1:]
	}
	return base == "store.go" || base == "badger.go" || strings.Contains(base, "repo")
}

func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
