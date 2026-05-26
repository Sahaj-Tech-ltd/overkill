package security

import (
	"fmt"
	"regexp"
	"strings"
)

type secretPattern struct {
	regex       *regexp.Regexp
	redactLabel string
	description string
	confidence  float64
	level       ThreatLevel
}

type SecretScanner struct {
	patterns []secretPattern
}

func NewSecretScanner() *SecretScanner {
	return &SecretScanner{
		patterns: []secretPattern{
			{
				regex:       regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
				redactLabel: "AWS_ACCESS_KEY",
				description: "AWS access key",
				confidence:  0.95,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`gh[ps]_[A-Za-z0-9_]{36,}`),
				redactLabel: "GITHUB_TOKEN",
				description: "GitHub token",
				confidence:  0.95,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`(?i)api[_\-]?key\s*[=:]\s*['"]?[A-Za-z0-9_\-]{20,}`),
				redactLabel: "API_KEY",
				description: "generic API key",
				confidence:  0.6,
				level:       ThreatMedium,
			},
			{
				regex:       regexp.MustCompile(`Bearer\s+[A-Za-z0-9_\-\.]{20,}`),
				redactLabel: "BEARER_TOKEN",
				description: "bearer token",
				confidence:  0.85,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`-----BEGIN (RSA |EC |DSA )?PRIVATE KEY-----`),
				redactLabel: "PRIVATE_KEY",
				description: "private key",
				confidence:  0.95,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`eyJ[A-Za-z0-9_\-]*\.eyJ[A-Za-z0-9_\-]*\.[A-Za-z0-9_\-]*`),
				redactLabel: "JWT_TOKEN",
				description: "JWT token",
				confidence:  0.9,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`(?i)(postgres|mysql|mongodb)://[^\s'"]+:[^\s'"]+@`),
				redactLabel: "DATABASE_URL",
				description: "database URL with credentials",
				confidence:  0.85,
				level:       ThreatMedium,
			},
			{
				regex:       regexp.MustCompile(`(?i)(?:password|passwd|secret|token)\s*[=:]\s*['"]?[A-Za-z0-9_\-+/=]{32,}`),
				redactLabel: "HIGH_ENTROPY_SECRET",
				description: "high-entropy secret in assignment",
				confidence:  0.6,
				level:       ThreatLow,
			},
		},
	}
}

func (s *SecretScanner) Name() string {
	return "secrets"
}

func (s *SecretScanner) Scan(input string) (*ScanResult, error) {
	if input == "" {
		return &ScanResult{
			Findings:  nil,
			MaxLevel:  ThreatNone,
			Blocked:   false,
			Sanitized: input,
		}, nil
	}

	var findings []Finding
	sanitized := input
	maxLevel := ThreatNone

	for _, p := range s.patterns {
		locs := p.regex.FindAllStringIndex(input, -1)
		for _, loc := range locs {
			match := input[loc[0]:loc[1]]
			confidence := p.confidence
			level := p.level

			findings = append(findings, Finding{
				Type:        "secret_exposure",
				Level:       level,
				Description: p.description,
				Match:       match,
				Confidence:  confidence,
			})

			if level > maxLevel {
				maxLevel = level
			}

			redacted := fmt.Sprintf("[REDACTED: %s]", p.redactLabel)
			sanitized = strings.ReplaceAll(sanitized, match, redacted)
		}
	}

	blocked := maxLevel >= ThreatHigh

	return &ScanResult{
		Findings:  findings,
		MaxLevel:  maxLevel,
		Blocked:   blocked,
		Sanitized: sanitized,
	}, nil
}
