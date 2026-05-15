package personality

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

type ColdStartState int

const (
	ColdStartUnknown ColdStartState = iota
	ColdStartPending
	ColdStartComplete
)

type ColdStartProfile struct {
	CommunicationStyle  string `json:"communication_style"`
	VerbosityPreference string `json:"verbosity_preference"`
	TechnicalDepth      string `json:"technical_depth"`
	ToneTolerance       string `json:"tone_tolerance"`
	UrgencyBaseline     string `json:"urgency_baseline"`
	UserName            string `json:"user_name"`
	Timezone            string `json:"timezone"`
}

type ColdStartProtocol struct {
	// mu guards state + profile. The TUI's boot-detection path and the
	// async response-processing path both touch these; without the
	// lock a -race build flagged the concurrent access. Mutex is fine
	// — cold-start is a one-off operation, contention isn't a concern.
	mu      sync.Mutex
	state   ColdStartState
	profile *ColdStartProfile
}

func NewColdStartProtocol() *ColdStartProtocol {
	return &ColdStartProtocol{
		state:   ColdStartUnknown,
		profile: &ColdStartProfile{},
	}
}

func (csp *ColdStartProtocol) IsColdStart(relationshipFile string) bool {
	if relationshipFile == "" {
		return true
	}
	info, err := os.Stat(relationshipFile)
	if err != nil {
		return true
	}
	if info.Size() == 0 {
		return true
	}
	return false
}

func (csp *ColdStartProtocol) State() ColdStartState {
	csp.mu.Lock()
	defer csp.mu.Unlock()
	return csp.state
}

func (csp *ColdStartProtocol) SetState(state ColdStartState) {
	csp.mu.Lock()
	defer csp.mu.Unlock()
	csp.state = state
}

func (csp *ColdStartProtocol) OpeningQuestion() string {
	return "I don't know you yet. What are you working on right now? Tell me about your project and how you like to work."
}

func (csp *ColdStartProtocol) ProcessResponse(response string) *ColdStartProfile {
	profile := &ColdStartProfile{}

	wordCount := len(strings.Fields(response))

	switch {
	case wordCount < 20:
		profile.VerbosityPreference = "terse"
		profile.CommunicationStyle = "direct"
	case wordCount <= 100:
		profile.VerbosityPreference = "moderate"
		profile.CommunicationStyle = "contextual"
	default:
		profile.VerbosityPreference = "verbose"
		profile.CommunicationStyle = "verbose"
	}

	techCount := countTechnicalTerms(response)
	switch {
	case techCount <= 1:
		profile.TechnicalDepth = "low"
	case techCount <= 5:
		profile.TechnicalDepth = "medium"
	default:
		profile.TechnicalDepth = "high"
	}

	lower := strings.ToLower(response)
	casualWords := []string{"hey", "yeah", "cool", "lol", "haha"}
	formalWords := []string{"please", "would", "could you", "i would like"}

	hasCasual := false
	hasFormal := false
	for _, w := range casualWords {
		if strings.Contains(lower, w) {
			hasCasual = true
			break
		}
	}
	for _, w := range formalWords {
		if strings.Contains(lower, w) {
			hasFormal = true
			break
		}
	}

	switch {
	case hasCasual && !hasFormal:
		profile.ToneTolerance = "casual"
	case hasFormal && !hasCasual:
		profile.ToneTolerance = "formal"
	default:
		profile.ToneTolerance = "moderate"
	}

	urgencyWords := []string{"asap", "urgent", "deadline", "need now", "quickly", "today"}
	relaxedWords := []string{"whenever", "no rush", "eventually", "when you can"}

	hasUrgency := false
	hasRelaxed := false
	for _, w := range urgencyWords {
		if strings.Contains(lower, w) {
			hasUrgency = true
			break
		}
	}
	for _, w := range relaxedWords {
		if strings.Contains(lower, w) {
			hasRelaxed = true
			break
		}
	}

	switch {
	case hasUrgency && !hasRelaxed:
		profile.UrgencyBaseline = "high"
	case hasRelaxed && !hasUrgency:
		profile.UrgencyBaseline = "low"
	default:
		profile.UrgencyBaseline = "moderate"
	}

	profile.UserName = extractUserName(response)
	profile.Timezone = extractTimezone(response)

	csp.mu.Lock()
	csp.state = ColdStartComplete
	csp.profile = profile
	csp.mu.Unlock()
	return profile
}

func (p *ColdStartProfile) ToRelationshipArc() map[string]string {
	return map[string]string{
		"communication":   p.CommunicationStyle,
		"verbosity":       p.VerbosityPreference,
		"technical_depth": p.TechnicalDepth,
		"tone":            p.ToneTolerance,
		"urgency":         p.UrgencyBaseline,
		"user_name":       p.UserName,
		"timezone":        p.Timezone,
	}
}

func countTechnicalTerms(s string) int {
	count := 0

	backtickRe := regexp.MustCompile("`[^`]+`")
	count += len(backtickRe.FindAllString(s, -1))

	extRe := regexp.MustCompile(`\b\w+\.(go|py|js|ts|rs|java|rb|cpp|c|h|tsx|jsx|mod|sum)\b`)
	count += len(extRe.FindAllString(s, -1))

	dotRe := regexp.MustCompile(`\b\w+\.\w+`)
	for _, match := range dotRe.FindAllString(s, -1) {
		if !extRe.MatchString(match) {
			count++
		}
	}

	underscoreRe := regexp.MustCompile(`\b\w+_\w+\b`)
	count += len(underscoreRe.FindAllString(s, -1))

	pathRe := regexp.MustCompile(`(?:^|\s)(?:\./|/\w+/|~/)\S+`)
	count += len(pathRe.FindAllString(s, -1))

	return count
}

func extractUserName(s string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)I'm\s+(\w+)`),
		regexp.MustCompile(`(?i)my name is\s+(\w+)`),
		regexp.MustCompile(`(?i)call me\s+(\w+)`),
	}
	for _, re := range patterns {
		matches := re.FindStringSubmatch(s)
		if len(matches) >= 2 {
			return matches[1]
		}
	}
	return ""
}

func extractTimezone(s string) string {
	tzRe := regexp.MustCompile(`(?i)\b(EST|CST|MST|PST|EDT|CDT|MDT|PDT|UTC[+-]\d{1,2})\b`)
	matches := tzRe.FindStringSubmatch(s)
	if len(matches) >= 2 {
		return matches[1]
	}

	tzInRe := regexp.MustCompile(`(?i)(?:I'm in|I am in)\s+([A-Z]{2,5})\b`)
	matches = tzInRe.FindStringSubmatch(s)
	if len(matches) >= 2 {
		return matches[1]
	}

	return ""
}
