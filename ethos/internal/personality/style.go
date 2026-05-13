package personality

import (
	"strings"
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

type StyleInferencer struct {
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
	obs := si.parseObservation(userInput)
	si.observations = append(si.observations, obs)

	for _, term := range obs.terms {
		si.termFreq[term]++
	}

	si.updateShortTerm(obs)
}

func (si *StyleInferencer) Baseline() *WorkingStyle {
	return si.baseline
}

func (si *StyleInferencer) Current() *WorkingStyle {
	return si.shortTerm
}

func (si *StyleInferencer) ShouldUpdateBaseline() bool {
	return si.sessionCount >= si.sessionsForBaseline && si.shortTerm != nil
}

func (si *StyleInferencer) CommitSession() {
	si.sessionCount++
	if si.sessionCount >= si.sessionsForBaseline && si.shortTerm != nil {
		si.baseline = &WorkingStyle{
			Communication:      si.shortTerm.Communication,
			ResponseExpect:     si.shortTerm.ResponseExpect,
			FrustrationTrigger: si.shortTerm.FrustrationTrigger,
			Approach:           si.shortTerm.Approach,
			DomainTerms:        append([]string{}, si.shortTerm.DomainTerms...),
		}
		si.sessionCount = 0
	}
}

func (si *StyleInferencer) SetBaseline(style *WorkingStyle) {
	si.baseline = &WorkingStyle{
		Communication:      style.Communication,
		ResponseExpect:     style.ResponseExpect,
		FrustrationTrigger: style.FrustrationTrigger,
		Approach:           style.Approach,
		DomainTerms:        append([]string{}, style.DomainTerms...),
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
