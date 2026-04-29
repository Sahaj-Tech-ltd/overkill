package rewriter

import (
	"fmt"
	"regexp"
	"strings"
)

type SycophancyReport struct {
	Detected bool     `json:"detected"`
	Patterns []string `json:"patterns"`
	Severity float64  `json:"severity"`
}

type SycophancyReducer struct {
	patterns           []*regexp.Regexp
	hedgingPattern     *regexp.Regexp
	standalonePattern  *regexp.Regexp
	enthusiasmPattern  *regexp.Regexp
}

func NewSycophancyReducer() *SycophancyReducer {
	return &SycophancyReducer{
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bgreat idea[!]?`),
			regexp.MustCompile(`(?i)\bexcellent choice[!]?`),
			regexp.MustCompile(`(?i)\bbrilliant[!]?`),
			regexp.MustCompile(`(?i)\bfantastic[!]?`),
			regexp.MustCompile(`(?i)\byou're absolutely right[!]?`),
			regexp.MustCompile(`(?i)\byou are absolutely right[!]?`),
			regexp.MustCompile(`(?i)\bthat's correct[!]?`),
			regexp.MustCompile(`(?i)\bthat is correct[!]?`),
			regexp.MustCompile(`(?i)\bI completely agree[!]?`),
			regexp.MustCompile(`(?i)\babsolutely\b`),
			regexp.MustCompile(`(?i)\bof course!`),
			regexp.MustCompile(`(?i)\bsure thing!`),
			regexp.MustCompile(`(?i)\bhappy to help!`),
			regexp.MustCompile(`(?i)\bgreat question[!]?`),
			regexp.MustCompile(`(?i)\bgood question[!]?`),
		},
		hedgingPattern: regexp.MustCompile(`(?i)(great idea|excellent choice|brilliant|fantastic|you're absolutely right|you are absolutely right|that's correct|that is correct|I completely agree|absolutely)[^.!?\n]*\b(however|but|actually)\b`),
		standalonePattern: regexp.MustCompile(`(?i)^(absolutely|of course!|sure thing!|happy to help!|great question|good question)[\s.!?]*$`),
		enthusiasmPattern: regexp.MustCompile(`(?i)^(of course!|sure thing!|happy to help!)\s*`),
	}
}

func (s *SycophancyReducer) Check(response string) *SycophancyReport {
	report := &SycophancyReport{
		Patterns: []string{},
	}

	seen := make(map[string]bool)

	for _, p := range s.patterns {
		matches := p.FindAllString(response, -1)
		for _, match := range matches {
			normalized := strings.ToLower(strings.TrimSpace(match))
			if !seen[normalized] {
				seen[normalized] = true
				report.Patterns = append(report.Patterns, match)
			}
		}
	}

	hedgingMatches := s.hedgingPattern.FindAllString(response, -1)
	for _, match := range hedgingMatches {
		normalized := strings.ToLower(strings.TrimSpace(match))
		if !seen[normalized] {
			seen[normalized] = true
			report.Patterns = append(report.Patterns, match)
		}
	}

	report.Detected = len(report.Patterns) > 0

	if report.Detected {
		count := float64(len(report.Patterns))
		hedgingBonus := 0.0
		if len(hedgingMatches) > 0 {
			hedgingBonus = 0.2
		}

		positionPenalty := 0.0
		lower := strings.ToLower(response)
		for _, p := range s.patterns {
			loc := p.FindStringIndex(lower)
			if loc != nil && loc[0] < len(lower)/4 {
				positionPenalty = 0.1
				break
			}
		}

		report.Severity = min(1.0, count*0.2+hedgingBonus+positionPenalty)
	}

	return report
}

func (s *SycophancyReducer) Strip(response string) string {
	result := response

	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bgreat idea!?\s*`),
		regexp.MustCompile(`(?i)\bexcellent choice!?\s*`),
		regexp.MustCompile(`(?i)\bbrilliant!?\s*`),
		regexp.MustCompile(`(?i)\bfantastic!?\s*`),
		regexp.MustCompile(`(?i)\byou're absolutely right!?\s*`),
		regexp.MustCompile(`(?i)\byou are absolutely right!?\s*`),
		regexp.MustCompile(`(?i)\bthat's correct!?\s*`),
		regexp.MustCompile(`(?i)\bthat is correct!?\s*`),
		regexp.MustCompile(`(?i)\bI completely agree!?\s*`),
		regexp.MustCompile(`(?i)\bof course!\s*`),
		regexp.MustCompile(`(?i)\bsure thing!\s*`),
		regexp.MustCompile(`(?i)\bhappy to help!\s*`),
		regexp.MustCompile(`(?i)\bgreat question!?\s*`),
		regexp.MustCompile(`(?i)\bgood question!?\s*`),
	}

	for _, p := range patterns {
		result = p.ReplaceAllString(result, "")
	}

	result = s.enthusiasmPattern.ReplaceAllString(result, "")

	result = strings.TrimSpace(result)
	result = collapseSpaces(result)

	if strings.HasPrefix(result, ". ") || strings.HasPrefix(result, "! ") || strings.HasPrefix(result, "? ") {
		result = result[2:]
	}
	if strings.HasPrefix(result, ".") || strings.HasPrefix(result, "!") || strings.HasPrefix(result, "?") {
		if len(result) > 1 && result[1] == ' ' {
			result = result[2:]
		} else if len(result) > 1 {
			result = result[1:]
		}
	}

	result = strings.TrimSpace(result)

	return result
}

func formatPattern(match string) string {
	return fmt.Sprintf("sycophantic: %s", strings.TrimSpace(match))
}
