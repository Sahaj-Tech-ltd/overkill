package security

import (
	"regexp"
	"strings"
)

type injectionPattern struct {
	regex       *regexp.Regexp
	description string
	confidence  float64
	level       ThreatLevel
}

type InjectionScanner struct {
	patterns []injectionPattern
}

func NewInjectionScanner() *InjectionScanner {
	return &InjectionScanner{
		patterns: []injectionPattern{
			{
				regex:       regexp.MustCompile(`(?i)ignore\s+(all\s+)?previous\s+instructions`),
				description: "instruction override attempt",
				confidence:  0.95,
				level:       ThreatCritical,
			},
			{
				regex:       regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous\s+|above\s+)?instructions`),
				description: "instruction disregard attempt",
				confidence:  0.95,
				level:       ThreatCritical,
			},
			{
				regex:       regexp.MustCompile(`(?i)forget\s+(everything|all|what\s+you\s+know)`),
				description: "memory wipe attempt",
				confidence:  0.95,
				level:       ThreatCritical,
			},
			{
				regex:       regexp.MustCompile(`(?i)you\s+are\s+now\s+(a\s+|an\s+)?(DAN|evil|malicious|hacker|unrestricted)`),
				description: "role switch attempt",
				confidence:  0.95,
				level:       ThreatCritical,
			},
			{
				regex:       regexp.MustCompile(`(?i)system\s+prompt\s*:`),
				description: "system prompt extraction",
				confidence:  0.9,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`(?i)system\s*:`),
				description: "system role injection",
				confidence:  0.7,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`(?i)new\s+(role|persona|identity)`),
				description: "identity replacement attempt",
				confidence:  0.85,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`(?i)jailbreak`),
				description: "jailbreak keyword",
				confidence:  0.85,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`(?i)\bBYPASS\b`),
				description: "bypass keyword",
				confidence:  0.8,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`(?i)sudo\s+rm\b`),
				description: "destructive sudo rm command",
				confidence:  0.7,
				level:       ThreatHigh,
			},
			{
				regex:       regexp.MustCompile(`<(system|assistant|user)>`),
				description: "XML role injection",
				confidence:  0.9,
				level:       ThreatHigh,
			},
		},
	}
}

func (s *InjectionScanner) Name() string {
	return "injection"
}

func (s *InjectionScanner) Scan(input string) (*ScanResult, error) {
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
			level := classifyThreat(confidence, p.level)

			findings = append(findings, Finding{
				Type:        "prompt_injection",
				Level:       level,
				Description: p.description,
				Match:       match,
				Confidence:  confidence,
			})

			if level > maxLevel {
				maxLevel = level
			}

			sanitized = strings.Replace(sanitized, match, "[REDACTED: potential prompt injection]", 1)
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

func classifyThreat(confidence float64, baseLevel ThreatLevel) ThreatLevel {
	if confidence >= 0.9 {
		if baseLevel >= ThreatCritical {
			return ThreatCritical
		}
		return ThreatHigh
	}
	if confidence >= 0.7 {
		return ThreatHigh
	}
	if confidence >= 0.5 {
		return ThreatMedium
	}
	return ThreatLow
}

