package security

import (
	"fmt"
	"log"
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
	patterns      []denyPattern
	mu            sync.Mutex
	window        time.Duration
	maxCmds       int
	timestamps    []time.Time
	permissions   *PermissionManager
	projectPath   string
	maxCommandLen int // 0 = disabled
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

// WithExtraDenyPatterns appends user-supplied regexes to the scanner's
// deny list. Each entry is compiled at construction; invalid regexes
// are silently dropped (logging is left to the caller). All matches
// produce ThreatHigh blocks under the id "user_deny_<n>".
func WithExtraDenyPatterns(patterns []string) CommandScannerOption {
	return func(s *CommandScanner) {
		for i, p := range patterns {
			re, err := regexp.Compile(p)
			if err != nil {
				log.Printf("security: invalid user deny pattern %q: %v", p, err)
				continue
			}
			s.patterns = append(s.patterns, denyPattern{
				id:          fmt.Sprintf("user_deny_%d", i),
				regex:       re,
				description: "user deny pattern: " + p,
				level:       ThreatHigh,
				confidence:  0.9,
			})
		}
	}
}

// WithForbiddenPaths adds a regex that blocks any command containing
// any of the supplied path substrings. Empty list is a no-op.
func WithForbiddenPaths(paths []string) CommandScannerOption {
	return func(s *CommandScanner) {
		for i, p := range paths {
			if p == "" {
				continue
			}
			re, err := regexp.Compile(regexp.QuoteMeta(p))
			if err != nil {
				log.Printf("security: invalid user deny pattern %q: %v", p, err)
				continue
			}
			s.patterns = append(s.patterns, denyPattern{
				id:          fmt.Sprintf("forbidden_path_%d", i),
				regex:       re,
				description: "forbidden path: " + p,
				level:       ThreatHigh,
				confidence:  0.95,
			})
		}
	}
}

// WithMaxCommandLen caps the length of an input passed to Scan. Inputs
// over the cap are blocked with a ThreatHigh finding. 0 disables.
func WithMaxCommandLen(n int) CommandScannerOption {
	return func(s *CommandScanner) {
		s.maxCommandLen = n
	}
}

// WithSandbox injects additional deny patterns that restrict what the agent
// can do on the host. When enabled:
//   - No privilege escalation (sudo, su, pkexec)
//   - No network exfiltration tools (nc, socat beyond localhost)
//   - No writes outside the project directory
//   - No systemd/service manipulation
func WithSandbox(enabled bool) CommandScannerOption {
	return func(s *CommandScanner) {
		if !enabled {
			return
		}
		s.patterns = append(s.patterns,
			denyPattern{
				id:          "sandbox_sudo",
				regex:       regexp.MustCompile(`(?i)\b(sudo|su\b|pkexec)\b`),
				description: "privilege escalation blocked by sandbox",
				level:       ThreatCritical,
				confidence:  0.95,
			},
			denyPattern{
				id:          "sandbox_network_exfil",
				regex:       regexp.MustCompile(`(?i)\b(nc\s|ncat\s|socat\s.*TCP:|telnet\s)`),
				description: "network exfiltration blocked by sandbox",
				level:       ThreatCritical,
				confidence:  0.9,
			},
			denyPattern{
				id:          "sandbox_write_outside",
				regex:       regexp.MustCompile(`(?i)(>\s*/(etc|var|usr|boot|opt|srv)/|mkdir\s+-p\s*/(etc|var|usr|boot|opt|srv)/)`),
				description: "write outside project blocked by sandbox",
				level:       ThreatHigh,
				confidence:  0.85,
			},
			denyPattern{
				id:          "sandbox_systemctl",
				regex:       regexp.MustCompile(`(?i)\b(systemctl|service\s+\S+\s+(start|stop|restart|enable))\b`),
				description: "service manipulation blocked by sandbox",
				level:       ThreatCritical,
				confidence:  0.9,
			},
		)
	}
}

func NewCommandScanner(opts ...CommandScannerOption) *CommandScanner {
	s := &CommandScanner{
		patterns: []denyPattern{
			{
				id:          "rm_rf_root",
				regex:       regexp.MustCompile(`(?i)rm\s+(?:-rf|--recursive\s+--force|-r\s+-f)\s+(/|\.|\*|~|\$HOME|\$\{)`),
				description: "recursive force delete (root/home/current dir)",
				level:       ThreatCritical,
				confidence:  0.95,
			},
			{
				id:          "fork_bomb",
				regex:       regexp.MustCompile(`\(\s*\)\s*\{\s*:\|:&\s*\}`),
				description: "fork bomb",
				level:       ThreatCritical,
				confidence:  0.95,
			},
			{
				id:          "mkfs",
				regex:       regexp.MustCompile(`(?i)mkfs\.`),
				description: "filesystem format",
				level:       ThreatCritical,
				confidence:  0.95,
			},
			{
				id:          "dd_raw_disk",
				regex:       regexp.MustCompile(`(?i)dd\s+if=.*of=/dev/`),
				description: "raw disk write",
				level:       ThreatCritical,
				confidence:  0.95,
			},
			{
				id:          "direct_device_write",
				regex:       regexp.MustCompile(`(?i)>\s*/dev/sd`),
				description: "direct device write",
				level:       ThreatCritical,
				confidence:  0.9,
			},
			{
				id:          "chmod_777_root",
				regex:       regexp.MustCompile(`(?i)chmod\s+(-R\s+)?777\s+/`),
				description: "world-writable root",
				level:       ThreatHigh,
				confidence:  0.85,
			},
			{
				id:          "curl_pipe_sh",
				regex:       regexp.MustCompile(`(?i)curl\s+.*\|\s*(ba)?sh`),
				description: "pipe curl to shell",
				level:       ThreatHigh,
				confidence:  0.9,
			},
			{
				id:          "wget_pipe_sh",
				regex:       regexp.MustCompile(`(?i)wget\s+.*\|\s*(ba)?sh`),
				description: "pipe wget to shell",
				level:       ThreatHigh,
				confidence:  0.9,
			},
			{
				id:          "system_shutdown",
				regex:       regexp.MustCompile(`(?i)\b(shutdown|reboot|poweroff|halt)\b`),
				description: "system shutdown command",
				level:       ThreatHigh,
				confidence:  0.85,
			},
			{
				id:          "overwrite_etc",
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

	// Length cap (Security.MaxCommandLen). Wired from user config so
	// a paranoid deployment can refuse pathologically long inputs
	// before the regex sweep even runs. 0 = disabled.
	if s.maxCommandLen > 0 && len(input) > s.maxCommandLen {
		findings = append(findings, Finding{
			Type:        "command_too_long",
			Level:       ThreatHigh,
			Description: fmt.Sprintf("command length %d exceeds cap %d", len(input), s.maxCommandLen),
			Confidence:  1.0,
		})
		maxLevel = ThreatHigh
	}

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

	// Encoded-bypass scan (paper #48 design input). Decode base64/hex
	// runs in the input and re-scan against the same deny patterns;
	// also flag the decode-and-execute shapes (base64 -d | sh, eval
	// $(...|base64...), xxd -r | bash) which have no legitimate
	// purpose in agent traffic. See decode.go for the full rationale.
	for _, f := range s.decodeAndScan(input) {
		findings = append(findings, f)
		if f.Level > maxLevel {
			maxLevel = f.Level
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
		// Only consult the permission manager for patterns that actually
		// matched THIS input. Iterating every pattern and checking on its
		// id (some of which used to be "") meant a single AllowOnce on
		// the empty key would silently un-block any future scan.
		matched := make(map[string]bool, len(findings))
		for _, p := range s.patterns {
			if p.id == "" {
				continue
			}
			if p.regex.MatchString(input) {
				matched[p.id] = true
			}
		}
		// Rate-limit-only blocks have no matching pattern, so the loop
		// above produces an empty `matched` set and the user has no
		// path to override. Inject a synthetic pseudo-id so AllowOnce /
		// AllowProject on "rate_limit" lets a power user re-enable a
		// command that's been hammering the limiter.
		if rateLimited {
			matched["rate_limit"] = true
		}
		for id := range matched {
			status := s.permissions.Check(id, input, s.projectPath)
			if status.Action == ActionAllowOnce || status.Action == ActionAllowProject {
				findings = append(findings, Finding{
					Type:        "permission_override",
					Level:       ThreatNone,
					Description: fmt.Sprintf("blocked by %s, but allowed by permission", id),
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
