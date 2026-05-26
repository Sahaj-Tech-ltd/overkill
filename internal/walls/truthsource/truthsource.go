// Package truthsource — "user is source of truth" behavioral wall (§8.7.6).
//
// Threat: the model redirects or corrects the user based on its training
// prior rather than treating the user's stated fact as ground truth. This
// produces phrases like "I think you might mean X" when the user mentioned
// a product, person, or event the model's training data doesn't contain.
//
// This package scans model *response* text for those redirect patterns and
// returns structured findings. Callers decide what to do — log, surface, or
// count toward a behavioral regression metric. The wall never blocks or
// modifies the response text.
package truthsource

import (
	"regexp"
	"strings"
)

// Finding is one matched redirect pattern.
type Finding struct {
	Pattern  string
	Excerpt  string
	Severity string // "high" | "medium"
}

// Result is the outcome of a Check call.
type Result struct {
	Findings []Finding
	HasIssue bool
}

// redirectPattern bundles a name, severity, and compiled regex.
type redirectPattern struct {
	name     string
	severity string
	re       *regexp.Regexp
}

// patterns is the detection set for model-over-user redirect phrases.
// Each entry is case-insensitive. Patterns are scoped to avoid firing on
// benign uses of similar phrases (e.g. "I'm not aware of any issues").
var patterns = []redirectPattern{
	{
		name:     "i_think_you_might_mean",
		severity: "high",
		// "I think you might mean", "I think you may mean"
		re: regexp.MustCompile(`(?i)\bi\s+think\s+you\s+mi?ght\s+(mean|be\s+thinking\s+of|be\s+referring\s+to)\b`),
	},
	{
		name:     "you_may_be_referring_to",
		severity: "high",
		// "you may be referring to", "you might be referring to"
		re: regexp.MustCompile(`(?i)\byou\s+(may|might)\s+be\s+referring\s+to\b`),
	},
	{
		name:     "perhaps_you_meant",
		severity: "high",
		// "perhaps you meant", "maybe you meant"
		re: regexp.MustCompile(`(?i)\b(perhaps|maybe)\s+you\s+(meant|mean|are\s+thinking\s+of)\b`),
	},
	{
		name:     "knowledge_cutoff",
		severity: "high",
		// "as of my knowledge cutoff", "as of my training cutoff"
		re: regexp.MustCompile(`(?i)\bas\s+of\s+my\s+(knowledge|training)\s+cutoff\b`),
	},
	{
		name:     "dont_have_info_but",
		severity: "medium",
		// "I don't have information about X, but Y might be" — the "but"
		// continuation signals the redirect. Guard: require "but" or
		// "however" within 120 chars to distinguish from plain "I don't
		// have information about that".
		re: regexp.MustCompile(`(?i)\bi\s+(don't|do\s+not)\s+have\s+information\s+about\b.{0,120}\b(but|however)\b`),
	},
	{
		name:     "not_aware_redirect",
		severity: "medium",
		// "I'm not aware of X" followed by a near-correction pivot.
		// Must have "but" or "however" or "instead" close by to avoid
		// firing on "I'm not aware of any issues with your approach".
		re: regexp.MustCompile(`(?i)\bi('m|\s+am)\s+not\s+aware\s+of\b.{0,80}\b(but|however|instead|you\s+might\s+mean)\b`),
	},
}

// Check scans responseText for redirect-over-user patterns and returns a
// Result. The caller decides what to do with the findings.
func Check(responseText string) Result {
	if strings.TrimSpace(responseText) == "" {
		return Result{}
	}
	var findings []Finding
	for _, p := range patterns {
		locs := p.re.FindAllStringIndex(responseText, -1)
		for _, loc := range locs {
			excerpt := responseText[loc[0]:loc[1]]
			if len(excerpt) > 200 {
				excerpt = excerpt[:200] + "…"
			}
			findings = append(findings, Finding{
				Pattern:  p.name,
				Excerpt:  excerpt,
				Severity: p.severity,
			})
		}
	}
	return Result{
		Findings: findings,
		HasIssue: len(findings) > 0,
	}
}

// HasHighSeverity returns true if any finding is severity "high".
func HasHighSeverity(r Result) bool {
	for _, f := range r.Findings {
		if f.Severity == "high" {
			return true
		}
	}
	return false
}
