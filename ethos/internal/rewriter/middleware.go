package rewriter

import (
	"regexp"
	"strings"
	"unicode"
)

type AnalysisResult struct {
	Complexity     Complexity `json:"complexity"`
	HasFiller      bool      `json:"has_filler"`
	HasVagueness   bool      `json:"has_vagueness"`
	HasSpecificity bool      `json:"has_specificity"`
	WordCount      int       `json:"word_count"`
	Confidence     float64   `json:"confidence"`
	Issues         []string  `json:"issues"`
}

type Middleware struct {
	stripPatterns       []*regexp.Regexp
	fillerWords         []string
	specificityTriggers map[string]string
	vaguePatterns       []*regexp.Regexp
	pathPattern         *regexp.Regexp
	lineRefPattern      *regexp.Regexp
	simpleVerbPattern   *regexp.Regexp
	complexVerbPattern  *regexp.Regexp
}

func NewMiddleware() *Middleware {
	m := &Middleware{
		stripPatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bplease\b\s*`),
			regexp.MustCompile(`(?i)\bcould you\b\s*`),
			regexp.MustCompile(`(?i)\bwould you mind\b\s*`),
			regexp.MustCompile(`(?i)\bcan you maybe\b\s*`),
			regexp.MustCompile(`(?i)\bI was wondering if\b\s*`),
			regexp.MustCompile(`(?i)\bif you could\b\s*`),
			regexp.MustCompile(`(?i)\bit would be great if\b\s*`),
		},
		fillerWords: []string{
			"please",
			"could you",
			"would you mind",
			"can you maybe",
			"i was wondering if",
			"if you could",
			"it would be great if",
		},
		specificityTriggers: map[string]string{
			"fix":      " (specify which file or function)",
			"test":     " (which module/file?)",
			"refactor": " (what scope? function/file/module?)",
		},
		vaguePatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bthe thing\b`),
			regexp.MustCompile(`(?i)\bthat bug\b`),
			regexp.MustCompile(`(?i)\bthe issue\b`),
			regexp.MustCompile(`(?i)\bit\b`),
			regexp.MustCompile(`(?i)\bthe problem\b`),
			regexp.MustCompile(`(?i)\bthat error\b`),
		},
		pathPattern: regexp.MustCompile(`[\w./\-]+\.\w+`),
		lineRefPattern: regexp.MustCompile(`(?i)\bline\s*\d+`),
		simpleVerbPattern: regexp.MustCompile(`(?i)^(fix|add|remove|update|delete|rename|move|change)\b`),
		complexVerbPattern: regexp.MustCompile(`(?i)\b(build|implement|design|architect|create|develop|construct|engineer)\b`),
	}
	return m
}

func (m *Middleware) Analyze(input string) *AnalysisResult {
	trimmed := strings.TrimSpace(input)
	words := countWords(trimmed)
	result := &AnalysisResult{
		WordCount: words,
		Issues:    []string{},
	}

	stripped, strippedItems := m.Strip(input)
	_ = stripped
	result.HasFiller = len(strippedItems) > 0

	hasPath := m.pathPattern.MatchString(trimmed)
	hasLineRef := m.lineRefPattern.MatchString(trimmed)
	result.HasSpecificity = hasPath || hasLineRef

	vagueMatches := m.findVagueMatches(trimmed)
	result.HasVagueness = len(vagueMatches) > 0

	if result.HasVagueness {
		result.Issues = append(result.Issues, vagueMatches...)
	}

	if result.HasVagueness && !hasPath && !hasLineRef {
		result.Complexity = ComplexityAmbiguous
		result.Confidence = 0.75
		return result
	}

	if words < 10 && m.simpleVerbPattern.MatchString(trimmed) {
		result.Complexity = ComplexitySimple
		result.Confidence = 0.9
		return result
	}

	if hasPath || hasLineRef {
		result.Complexity = ComplexitySimple
		result.Confidence = 0.85
		return result
	}

	if words < 15 && !result.HasVagueness && !m.complexVerbPattern.MatchString(trimmed) {
		result.Complexity = ComplexitySimple
		result.Confidence = 0.7
		return result
	}

	if m.complexVerbPattern.MatchString(trimmed) {
		result.Complexity = ComplexityComplex
		result.Confidence = 0.8
		return result
	}

	if words > 30 && !hasPath {
		result.Complexity = ComplexityComplex
		result.Confidence = 0.7
		return result
	}

	sentenceCount := strings.Count(trimmed, ".") + strings.Count(trimmed, "!") + strings.Count(trimmed, "?")
	if sentenceCount > 1 {
		result.Complexity = ComplexityComplex
		result.Confidence = 0.6
		return result
	}

	result.Complexity = ComplexityAmbiguous
	result.Confidence = 0.5
	return result
}

func (m *Middleware) Strip(input string) (string, []string) {
	var stripped []string
	result := input

	for _, pattern := range m.stripPatterns {
		if pattern.MatchString(result) {
			matches := pattern.FindAllString(result, -1)
			for _, match := range matches {
				stripped = append(stripped, strings.TrimSpace(match))
			}
			result = pattern.ReplaceAllString(result, "")
		}
	}

	result = strings.TrimSpace(result)
	result = collapseSpaces(result)

	return result, stripped
}

func (m *Middleware) InjectSpecificity(input string) (string, []string) {
	var injections []string
	lower := strings.ToLower(input)

	hasPath := m.pathPattern.MatchString(input)

	for trigger, prompt := range m.specificityTriggers {
		if strings.Contains(lower, trigger) && !hasPath {
			input = input + prompt
			injections = append(injections, trigger)
		}
	}

	return input, injections
}

func (m *Middleware) findVagueMatches(input string) []string {
	var matches []string
	seen := make(map[string]bool)

	for _, p := range m.vaguePatterns {
		if p.MatchString(input) {
			match := strings.TrimSpace(p.FindString(input))
			if !seen[match] {
				seen[match] = true
				matches = append(matches, "vague reference: "+match)
			}
		}
	}

	return matches
}

func countWords(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inWord = false
		} else if !inWord {
			inWord = true
			count++
		}
	}
	return count
}

func collapseSpaces(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
	}
	return b.String()
}
