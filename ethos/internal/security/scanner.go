package security

import (
	"fmt"
	"regexp"
	"sync"
	"time"
)

type denyPattern struct {
	id          string
	regex       *regexp.Regexp
	description string
	level       ThreatLevel
	confidence  float64
}

type CommandScanner struct {
	patterns    []denyPattern
	mu          sync.Mutex
	window      time.Duration
	maxCmds     int
	timestamps  []time.Time
	permissions *PermissionManager
	projectPath string
}

type CommandScannerOption func(*CommandScanner)

func WithPermissionManager(pm *PermissionManager) CommandScannerOption {
	return func(s *CommandScanner) {
		s.permissions = pm
	}
}

func WithProjectPath(path string) CommandScannerOption {
	return func(s *CommandScanner) {
		s.projectPath = path
	}
}

func NewCommandScanner(opts ...CommandScannerOption) *CommandScanner {
	s := &CommandScanner{
		patterns: []denyPattern{
			{
				id:          "rm_rf_root",
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

	for _, opt := range opts {
		opt(s)
	}
	return s
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

	// checkAndRecord folds the rate-limit check and the timestamp append
	// into a single critical section. The previous split (rateExceeded()
	// + recordCall()) was a TOCTOU race: two goroutines could both pass
	// the check and both append, allowing maxCmds+1 calls per window.
	rateLimited := s.checkAndRecord()
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

	blocked := maxLevel >= ThreatHigh || rateLimited

	if s.permissions != nil && blocked {
		for _, p := range s.patterns {
			status := s.permissions.Check(p.id, input, s.projectPath)
			if status.Action == ActionAllowOnce || status.Action == ActionAllowProject {
				findings = append(findings, Finding{
					Type:        "permission_override",
					Level:       ThreatNone,
					Description: fmt.Sprintf("blocked by %s, but allowed by permission", p.id),
					Confidence:  1.0,
				})
				blocked = false
				break
			}
		}
	}

	return &ScanResult{
		Findings:  findings,
		MaxLevel:  maxLevel,
		Blocked:   blocked,
		Sanitized: input,
	}, nil
}

// checkAndRecord evicts expired timestamps, decides whether this call
// would exceed the rate limit, and (only if NOT limited) records the
// timestamp — all under one mutex. Returns true when the caller is over
// the limit and the timestamp was NOT recorded.
//
// Folding check+record fixes a TOCTOU where two concurrent Scan calls
// could both observe "len(valid) < maxCmds" and both append, letting
// the window briefly hold maxCmds+1 entries.
func (s *CommandScanner) checkAndRecord() bool {
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
	if len(valid) >= s.maxCmds {
		s.timestamps = valid
		return true
	}
	s.timestamps = append(valid, now)
	return false
}
