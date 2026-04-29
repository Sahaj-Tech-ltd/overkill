package security

import (
	"fmt"
	"regexp"
	"sync"
	"time"
)

type denyPattern struct {
	regex       *regexp.Regexp
	description string
	level       ThreatLevel
	confidence  float64
}

type CommandScanner struct {
	patterns []denyPattern
	mu       sync.Mutex
	window   time.Duration
	maxCmds  int
	timestamps []time.Time
}

func NewCommandScanner() *CommandScanner {
	return &CommandScanner{
		patterns: []denyPattern{
			{
				regex:       regexp.MustCompile(`(?i)rm\s+-rf\s+/`),
				description: "recursive force delete root",
				level:       ThreatCritical,
				confidence:  0.95,
			},
			{
				regex:       regexp.MustCompile(`\(\)\{\s*:\|\:&\s*\}`),
				description: "fork bomb",
				level:       ThreatCritical,
				confidence:  0.95,
			},
			{
				regex:       regexp.MustCompile(`(?i)mkfs\.`),
				description: "filesystem format",
				level:       ThreatCritical,
				confidence:  0.95,
			},
			{
				regex:       regexp.MustCompile(`(?i)dd\s+if=.*of=/dev/`),
				description: "raw disk write",
				level:       ThreatCritical,
				confidence:  0.95,
			},
			{
				regex:       regexp.MustCompile(`(?i)>\s*/dev/sd`),
				description: "direct device write",
				level:       ThreatCritical,
				confidence:  0.9,
			},
			{
				regex:       regexp.MustCompile(`(?i)chmod\s+(-R\s+)?777\s+/`),
				description: "world-writable root",
				level:       ThreatHigh,
				confidence:  0.85,
			},
			{
				regex:       regexp.MustCompile(`(?i)curl\s+.*\|\s*(ba)?sh`),
				description: "pipe curl to shell",
				level:       ThreatHigh,
				confidence:  0.9,
			},
			{
				regex:       regexp.MustCompile(`(?i)wget\s+.*\|\s*(ba)?sh`),
				description: "pipe wget to shell",
				level:       ThreatHigh,
				confidence:  0.9,
			},
			{
				regex:       regexp.MustCompile(`(?i)\b(shutdown|reboot|poweroff|halt)\b`),
				description: "system shutdown command",
				level:       ThreatHigh,
				confidence:  0.85,
			},
			{
				regex:       regexp.MustCompile(`(?i)(: >|>)\s*/etc/`),
				description: "overwrite system files",
				level:       ThreatHigh,
				confidence:  0.85,
			},
		},
		window:     time.Minute,
		maxCmds:    100,
		timestamps: make([]time.Time, 0),
	}
}

func (s *CommandScanner) Name() string {
	return "command"
}

func (s *CommandScanner) Scan(input string) (*ScanResult, error) {
	if input == "" {
		return &ScanResult{
			Findings:  nil,
			MaxLevel:  ThreatNone,
			Blocked:   false,
			Sanitized: input,
		}, nil
	}

	var findings []Finding
	maxLevel := ThreatNone

	for _, p := range s.patterns {
		locs := p.regex.FindAllStringIndex(input, -1)
		for _, loc := range locs {
			match := input[loc[0]:loc[1]]
			findings = append(findings, Finding{
				Type:        "dangerous_command",
				Level:       p.level,
				Description: p.description,
				Match:       match,
				Confidence:  p.confidence,
			})
			if p.level > maxLevel {
				maxLevel = p.level
			}
		}
	}

	rateLimited := s.rateExceeded()
	if rateLimited {
		findings = append(findings, Finding{
			Type:        "rate_limit",
			Level:       ThreatMedium,
			Description: fmt.Sprintf("command rate limit exceeded (max %d per %s)", s.maxCmds, s.window),
			Match:       input,
			Confidence:  1.0,
		})
		if ThreatMedium > maxLevel {
			maxLevel = ThreatMedium
		}
	}

	s.recordCall()

	blocked := maxLevel >= ThreatHigh || rateLimited

	return &ScanResult{
		Findings:  findings,
		MaxLevel:  maxLevel,
		Blocked:   blocked,
		Sanitized: input,
	}, nil
}

func (s *CommandScanner) rateExceeded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-s.window)
	valid := make([]time.Time, 0, len(s.timestamps))
	for _, t := range s.timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	s.timestamps = valid
	return len(valid) >= s.maxCmds
}

func (s *CommandScanner) recordCall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timestamps = append(s.timestamps, time.Now())
}
