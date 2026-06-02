package security

import (
	"regexp"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
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
				// Case-insensitive — every other pattern in this list
				// has (?i); the omission here let `<SYSTEM>`, `<User>`
				// etc. bypass while `<system>` was caught.
				regex:       regexp.MustCompile(`(?i)<(system|assistant|user)>`),
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

	// RT-SEC-1 + RT-SEC-2: Normalize Unicode and strip invisible chars
	// before pattern matching. Cyrillic homoglyphs like U+043E 'о' look
	// identical to ASCII 'o' but bypass ASCII-only regex. Zero-width
	// joiners/space markers break word-boundary patterns entirely.
	normalized := norm.NFKD.String(input)
	normalized = stripInvisible(normalized)
	normalized = mapHomoglyphs(normalized)

	var findings []Finding
	maxLevel := ThreatNone

	// RT-SEC-4: Scan ALL patterns against the original normalized input
	// first, collecting every match. Building sanitized output
	// incrementally (replacing matches one at a time) collapses
	// overlapping or adjacent match sites.
	var redactions []redactSpan

	// Primary scan: normalized input.
	for _, p := range s.patterns {
		locs := p.regex.FindAllStringIndex(normalized, -1)
		for _, loc := range locs {
			match := normalized[loc[0]:loc[1]]
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

			redactions = append(redactions, redactSpan{loc[0], loc[1]})
		}
	}

	// Build sanitized output by replacing matched spans in reverse
	// order so earlier indices stay valid.
	sanitized := input
	sortRedactions(redactions)
	for i := len(redactions) - 1; i >= 0; i-- {
		r := redactions[i]
		sanitized = sanitized[:r.start] + "[REDACTED: potential prompt injection]" + sanitized[r.end:]
	}

	// RT-SEC-3: Base64-decode pass. Injection directives encoded as
	// base64 are invisible to ASCII patterns but decode to the original
	// text. Unlike CommandScanner (which has decodeAndScan for its own
	// deny patterns), InjectionScanner had no decode layer.
	for _, f := range s.decodeAndScan(normalized) {
		findings = append(findings, f)
		if f.Level > maxLevel {
			maxLevel = f.Level
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

// stripInvisible removes zero-width and invisible Unicode characters
// that break regex matching without affecting visual appearance.
// Covers U+200B–U+200F (ZWSP, ZWJ, ZWNJ, LRM, RLM), U+2028–U+202E
// (line/paragraph separators, text direction overrides), U+2060–U+2069
// (word joiner, invisible operators), and U+FEFF (BOM/ZWNBS).
func stripInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0x200B && r <= 0x200F:
			return -1
		case r >= 0x2028 && r <= 0x202E:
			return -1
		case r >= 0x2060 && r <= 0x2069:
			return -1
		case r == 0xFEFF:
			return -1
		}
		return r
	}, s)
}

// homoglyphMap is the set of Cyrillic and other Unicode characters that
// are visually indistinguishable from ASCII letters. NFKD normalization
// is NOT sufficient for these — Cyrillic 'о' (U+043E) and Latin 'o' are
// distinct code points that do not decompose.
var homoglyphMap = map[rune]rune{
	// Cyrillic → Latin
	0x0430: 'a', 0x0410: 'A',
	0x0435: 'e', 0x0415: 'E',
	0x0456: 'i', 0x0406: 'I',
	0x043E: 'o', 0x041E: 'O',
	0x0440: 'p', 0x0420: 'P',
	0x0441: 'c', 0x0421: 'C',
	0x0443: 'y', 0x0423: 'Y',
	0x0445: 'x', 0x0425: 'X',
	0x0455: 's', 0x0405: 'S',
	0x043A: 'k', 0x041A: 'K',
	0x043C: 'm', 0x041C: 'M',
	0x043D: 'n', 0x041D: 'N',
	0x0432: 'b', 0x0412: 'B',
	0x0442: 't', 0x0422: 'T',
	0x04BB: 'h', 0x04BA: 'H',
	// Greek → Latin
	0x03BF: 'o', 0x039F: 'O',
	0x03B5: 'e', 0x0395: 'E',
	0x03B9: 'i', 0x0399: 'I',
}

// mapHomoglyphs replaces visually-identical Unicode characters with
// their ASCII equivalents so injection regex patterns can match them.
// NFKD handles many cases (e.g. fullwidth → ASCII) but Cyrillic and
// Greek lookalikes have distinct code points that never decompose.
func mapHomoglyphs(s string) string {
	return strings.Map(func(r rune) rune {
		if ascii, ok := homoglyphMap[r]; ok {
			return ascii
		}
		return r
	}, s)
}

// decodeAndScan decodes base64 runs from the input and re-scans the
// decoded text against every injection pattern. Returns additional
// findings when decoded payloads contain injection directives.
func (s *InjectionScanner) decodeAndScan(input string) []Finding {
	b64re := regexp.MustCompile(`[A-Za-z0-9+/_-]{4,}={0,2}`)
	seen := make(map[string]bool)
	var out []Finding

	for _, run := range b64re.FindAllString(input, -1) {
		if seen[run] {
			continue
		}
		seen[run] = true
		decoded := tryBase64(run)
		if decoded == "" {
			continue
		}
		if !looksLikeCommand([]byte(decoded)) {
			continue
		}
		for _, p := range s.patterns {
			if p.regex.MatchString(decoded) {
				out = append(out, Finding{
					Type:        "prompt_injection",
					Level:       classifyThreat(p.confidence, p.level),
					Description: "encoded injection: " + p.description,
					Match:       run,
					Confidence:  p.confidence * 0.8,
				})
			}
		}
	}
	return out
}

func isPrintableASCII(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7E {
			if c != '\n' && c != '\r' && c != '\t' {
				return false
			}
		}
	}
	return true
}

// sortRedactions sorts redaction spans by start position ascending.
type redactSpan struct{ start, end int }

func sortRedactions(r []redactSpan) {
	sort.Slice(r, func(i, j int) bool { return r[i].start < r[j].start })
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
