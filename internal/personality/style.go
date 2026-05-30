// Package personality — two-layer communication style model.
// Short-term layer flips per-turn on user input; long-term
// (baseline) drifts over consecutive sessions.
package personality

import (
	"strings"
	"sync"
	"unicode"
)

type CommunicationStyle string

const (
	CommDirect     CommunicationStyle = "direct"
	CommVerbose    CommunicationStyle = "verbose"
	CommContextual CommunicationStyle = "contextual"
)

type ResponseStyle string

const (
	RespSynthesis ResponseStyle = "synthesis"
	RespCritique  ResponseStyle = "critique"
	RespAction    ResponseStyle = "action"
)

type ApproachStyle string

const (
	ApproachPlansFirst ApproachStyle = "plans_first"
	ApproachDiveIn     ApproachStyle = "dive_in"
)

type WorkingStyle struct {
	Communication      CommunicationStyle `json:"communication"`
	ResponseExpect     ResponseStyle      `json:"response_expect"`
	FrustrationTrigger string             `json:"frustration_trigger"`
	Approach           ApproachStyle      `json:"approach"`
	DomainTerms        []string           `json:"domain_terms"`
}

type styleObservation struct {
	msgLen         int
	hasQuestion    bool
	isImperative   bool
	hasExplanation bool
	hasPlanWord    bool
	terms          []string
}

// StyleInferencer tracks per-turn observations + per-session
// baseline drift. All state mutators + readers hold the mutex.
// Concurrent callers: per-input Observe (input observer goroutine),
// session-end CommitSession + SaveToFile (TUI exit defer), per-turn
// Baseline/Current (personality provider). Returned *WorkingStyle
// pointers are defensive copies so callers can't mutate live state.
type StyleInferencer struct {
	mu                  sync.Mutex
	baseline            *WorkingStyle
	shortTerm           *WorkingStyle
	sessionCount        int
	sessionsForBaseline int
	observations        []styleObservation
	termFreq            map[string]int
	// lastCommittedStyle holds the previous session's distilled
	// shortTerm so ConsecutiveSessionCommit can compare and decide
	// whether to extend the streak or reset. Persisted via
	// SaveToFile so the streak survives across boots.
	lastCommittedStyle *WorkingStyle
}

func NewStyleInferencer() *StyleInferencer {
	return &StyleInferencer{
		sessionsForBaseline: 5,
		shortTerm: &WorkingStyle{
			Communication:  CommDirect,
			ResponseExpect: RespAction,
			Approach:       ApproachDiveIn,
			DomainTerms:    []string{},
		},
		baseline: &WorkingStyle{
			Communication:  CommDirect,
			ResponseExpect: RespAction,
			Approach:       ApproachDiveIn,
			DomainTerms:    []string{},
		},
		observations: []styleObservation{},
		termFreq:     map[string]int{},
	}
}

func (si *StyleInferencer) Observe(userInput string) {
	si.mu.Lock()
	defer si.mu.Unlock()
	obs := si.parseObservation(userInput)
	si.observations = append(si.observations, obs)

	for _, term := range obs.terms {
		si.termFreq[term]++
	}

	si.updateShortTerm(obs)
}

// Baseline returns a defensive copy of the baseline style so the
// caller cannot mutate live state.
func (si *StyleInferencer) Baseline() *WorkingStyle {
	si.mu.Lock()
	defer si.mu.Unlock()
	return copyStyle(si.baseline)
}

// Current returns a defensive copy of the short-term style.
func (si *StyleInferencer) Current() *WorkingStyle {
	si.mu.Lock()
	defer si.mu.Unlock()
	return copyStyle(si.shortTerm)
}

func (si *StyleInferencer) ShouldUpdateBaseline() bool {
	si.mu.Lock()
	defer si.mu.Unlock()
	return si.sessionCount >= si.sessionsForBaseline && si.shortTerm != nil
}

func (si *StyleInferencer) CommitSession() {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.sessionCount++
	if si.sessionCount >= si.sessionsForBaseline && si.shortTerm != nil {
		si.baseline = copyStyle(si.shortTerm)
		si.sessionCount = 0
	}
}

func (si *StyleInferencer) SetBaseline(style *WorkingStyle) {
	si.mu.Lock()
	defer si.mu.Unlock()
	si.baseline = copyStyle(style)
}

// copyStyle returns a deep copy of style (or nil for a nil input)
// so callers holding returned pointers can't mutate live state.
func copyStyle(s *WorkingStyle) *WorkingStyle {
	if s == nil {
		return nil
	}
	return &WorkingStyle{
		Communication:      s.Communication,
		ResponseExpect:     s.ResponseExpect,
		FrustrationTrigger: s.FrustrationTrigger,
		Approach:           s.Approach,
		DomainTerms:        append([]string{}, s.DomainTerms...),
	}
}

func (si *StyleInferencer) parseObservation(input string) styleObservation {
	lower := strings.ToLower(input)
	words := splitWords(lower)
	wordCount := len(words)

	hasQuestion := strings.Contains(input, "?")

	imperativeStarters := []string{"fix", "add", "remove", "create", "delete", "update", "build", "write", "refactor"}
	isImperative := false
	if wordCount > 0 {
		first := words[0]
		for _, imp := range imperativeStarters {
			if first == imp {
				isImperative = true
				break
			}
		}
	}

	hasExplanation := strings.Contains(lower, "because") ||
		strings.Contains(lower, "since") ||
		strings.Contains(lower, "the reason")

	planWords := []string{"plan", "first", "then", "steps", "approach"}
	hasPlanWord := false
	for _, pw := range planWords {
		if strings.Contains(lower, pw) {
			hasPlanWord = true
			break
		}
	}

	var terms []string
	for _, w := range words {
		runeCount := 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				runeCount++
			}
		}
		if runeCount > 5 {
			terms = append(terms, w)
		}
	}

	return styleObservation{
		msgLen:         wordCount,
		hasQuestion:    hasQuestion,
		isImperative:   isImperative,
		hasExplanation: hasExplanation,
		hasPlanWord:    hasPlanWord,
		terms:          terms,
	}
}

func (si *StyleInferencer) updateShortTerm(obs styleObservation) {
	avgLen := si.averageMsgLen()
	switch {
	case avgLen > 30:
		si.shortTerm.Communication = CommVerbose
	case avgLen >= 10:
		if obs.hasExplanation {
			si.shortTerm.Communication = CommContextual
		} else {
			si.shortTerm.Communication = CommContextual
		}
	default:
		si.shortTerm.Communication = CommDirect
	}

	if obs.isImperative {
		si.shortTerm.ResponseExpect = RespAction
	}
	if obs.hasQuestion {
		si.shortTerm.ResponseExpect = RespCritique
	}

	if obs.hasPlanWord {
		si.shortTerm.Approach = ApproachPlansFirst
	}

	lower := strings.ToLower(strings.Join(si.recentInputs(), " "))
	frustrationMarkers := []string{"!", "again", "still", "why"}
	for _, marker := range frustrationMarkers {
		if strings.Contains(lower, marker) {
			si.shortTerm.FrustrationTrigger = marker
			break
		}
	}

	si.updateDomainTerms()
}

func (si *StyleInferencer) averageMsgLen() float64 {
	if len(si.observations) == 0 {
		return 0
	}
	total := 0
	for _, obs := range si.observations {
		total += obs.msgLen
	}
	return float64(total) / float64(len(si.observations))
}

func (si *StyleInferencer) recentInputs() []string {
	inputs := make([]string, len(si.observations))
	for i, obs := range si.observations {
		inputs[i] = strings.Join(obs.terms, " ")
	}
	return inputs
}

func (si *StyleInferencer) updateDomainTerms() {
	seen := map[string]bool{}
	var domainTerms []string
	for term, count := range si.termFreq {
		if count >= 3 && !seen[term] {
			domainTerms = append(domainTerms, term)
			seen[term] = true
		}
	}
	si.shortTerm.DomainTerms = domainTerms
}

func splitWords(s string) []string {
	return strings.Fields(s)
}
