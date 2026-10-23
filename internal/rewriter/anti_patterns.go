package rewriter

import (
	"regexp"
	"strings"
)

type antiPatternRule struct {
	pattern *regexp.Regexp
	note    string
}
type antiPatternGuard struct{ rules []antiPatternRule }

// The guard adds concrete advisory constraints; it does not authorize actions,
// enforce tool policy, reject a user task, or silently replace requested code.
func newAntiPatternGuard() *antiPatternGuard {
	return &antiPatternGuard{rules: []antiPatternRule{
		{regexp.MustCompile(`(?i)\b(?:skip|disable|omit)\s+(?:the\s+)?(?:tests?|verification)\b`), "[Rewrite guard: Verify relevant changes before claiming completion; report any checks that could not run.]"},
		{regexp.MustCompile(`(?i)\b(?:hardcode|hard-code|embed)\s+(?:the\s+)?(?:api[ -]?keys?|passwords?|secrets?|credentials)\b`), "[Rewrite guard: Keep credentials in secret or environment references rather than source literals.]"},
		{regexp.MustCompile(`(?i)\b(?:bypass|disable|remove)\s+(?:the\s+)?(?:input\s+)?(?:validation|authentication|authorization|auth checks)\b`), "[Rewrite guard: Preserve input validation and access controls; fix the failing behavior without bypassing those checks.]"},
	}}
}

func (g *antiPatternGuard) notes(input string) []string {
	// Do not turn explicit instructions to retain checks into the opposite claim.
	normalized := regexp.MustCompile(`(?i)\b(?:do not|don't|never)\s+(?:skip|disable|omit|hardcode|hard-code|embed|bypass|remove)\b[^.!?\n]*`).ReplaceAllString(input, "")
	var notes []string
	for _, rule := range g.rules {
		if rule.pattern.MatchString(normalized) {
			notes = append(notes, rule.note)
		}
	}
	return notes
}

func appendGuardNotes(input string, notes []string) string {
	for _, note := range notes {
		if !strings.Contains(input, note) {
			input += "\n\n" + note
		}
	}
	return input
}
