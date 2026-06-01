// Package learning implements a lightweight learning-from-corrections system.
//
// When the user corrects the assistant ("no, that's wrong", "actually, ..."),
// the correction is detected and stored in a SQLite-backed store. Future
// agent runs query the store and inject relevant past corrections into the
// system prompt to improve responses.
package learning

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Correction represents a stored correction pair: what the assistant said
// that was wrong, and what the user corrected it to.
type Correction struct {
	Context   string `json:"context"`   // the user message that triggered the wrong response
	Wrong     string `json:"wrong"`     // the assistant's incorrect response
	Correct   string `json:"correct"`   // the user's correction message
	Timestamp int64  `json:"timestamp"` // Unix nano timestamp
}

// Key returns the hash key for this correction (sha256 of context + wrong).
func (c *Correction) Key() string {
	h := sha256.New()
	h.Write([]byte(c.Context))
	h.Write([]byte(c.Wrong))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Marshal serialises the correction to JSON bytes.
func (c *Correction) Marshal() ([]byte, error) {
	return json.Marshal(c)
}

// UnmarshalCorrection deserialises a correction from JSON bytes.
func UnmarshalCorrection(data []byte) (*Correction, error) {
	var c Correction
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// correctionPatterns is the set of regexps that signal a correction message.
// The patterns are case-insensitive and anchored at sentence starts or after
// punctuation to avoid false positives mid-sentence.
var correctionPatterns = []*regexp.Regexp{
	// Start-of-message or after preceding punctuation.
	// "no" / "nope" / "nah" followed by punctuation (comma, semicolon, etc.)
	// or end-of-line — NOT followed by another word like "worries" or "problem".
	regexp.MustCompile(`(?im)^\s*(?:no|nope|nah)[,;:!.]`),
	regexp.MustCompile(`(?im)^\s*(?:no|nope|nah)\s*$`),
	regexp.MustCompile(`(?im)^\s*wrong[,;:!.\s]`),
	regexp.MustCompile(`(?im)^\s*actually[,;:!.\s]`),
	regexp.MustCompile(`(?im)^\s*instead[,;:!.\s]`),
	regexp.MustCompile(`(?im)^\s*correct(?:ion)?\s*[:;]`),
	regexp.MustCompile(`(?im)^\s*(?:no\s+)?that(?:'s|\s+is)\s+wrong`),
	regexp.MustCompile(`(?im)^\s*don'?t\s+do\s+that`),
	regexp.MustCompile(`(?im)^\s*please\s+don'?t\b`),
	regexp.MustCompile(`(?im)^\s*i\s+(?:meant|said|wanted)\b`),
	regexp.MustCompile(`(?im)^\s*you\s+(?:misunderstand|misunderstood|misread|misinterpret)`),
	regexp.MustCompile(`(?im)^\s*that\s+(?:isn'?t|is\s+not)\s+(?:right|correct|what\s+i\s+(?:meant|asked|wanted))`),
	regexp.MustCompile(`(?im)^\s*(?:try|do)\s+(?:it\s+)?(?:again|differently)`),
	regexp.MustCompile(`(?im)^\s*(?:not|it'?s\s+not)\s+(?:that|like\s+that|what\s+i\s+meant)`),
}

// IsCorrection returns true if the user message looks like a correction.
func IsCorrection(msg string) bool {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return false
	}
	for _, pat := range correctionPatterns {
		if pat.MatchString(msg) {
			return true
		}
	}
	return false
}

// ExtractCorrect extracts the substantive part of a correction message
// (strips the correction prefix/signal words). Returns the corrected
// instruction text.
func ExtractCorrect(msg string) string {
	msg = strings.TrimSpace(msg)
	lower := strings.ToLower(msg)

	// Strip common correction prefixes
	prefixes := []string{
		"no,", "no;", "no:", "no.", "no ",
		"wrong,", "wrong;", "wrong:", "wrong.", "wrong ",
		"actually,", "actually;", "actually:", "actually.", "actually ",
		"instead,", "instead;", "instead:", "instead.", "instead ",
		"correct:", "correction:",
		"that's wrong,", "that's wrong.", "that is wrong,",
		"don't do that,", "don't do that.", "dont do that,",
		"please don't,", "please dont,",
		"i meant", "i wanted", "i said",
	}

	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			msg = strings.TrimSpace(msg[len(p):])
			// Remove leading punctuation that may remain
			msg = strings.TrimLeft(msg, ",;:.!? ")
			return msg
		}
	}

	// For more complex patterns, remove the first sentence if it's just
	// a correction signal
	sentences := splitSentences(msg)
	if len(sentences) > 1 && IsCorrection(sentences[0]) {
		return strings.TrimSpace(strings.Join(sentences[1:], " "))
	}

	return msg
}

// splitSentences naively splits text on sentence boundaries.
func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder
	inWord := false
	for _, r := range text {
		current.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			sentences = append(sentences, strings.TrimSpace(current.String()))
			current.Reset()
			inWord = false
			continue
		}
		if !unicode.IsSpace(r) {
			inWord = true
		}
	}
	if current.Len() > 0 && inWord {
		sentences = append(sentences, strings.TrimSpace(current.String()))
	}
	return sentences
}

// NewCorrection creates a Correction with the current timestamp.
func NewCorrection(context, wrong, correct string) *Correction {
	return &Correction{
		Context:   context,
		Wrong:     wrong,
		Correct:   correct,
		Timestamp: time.Now().UnixNano(),
	}
}

// tokenize splits text into lowercase tokens, stripping punctuation.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	// Replace punctuation with spaces, keep alphanumerics
	var cleaned strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '\n' || r == '\t' {
			cleaned.WriteRune(r)
		} else {
			cleaned.WriteRune(' ')
		}
	}
	fields := strings.Fields(cleaned.String())
	// Deduplicate
	seen := make(map[string]bool, len(fields))
	unique := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue // skip single-char tokens
		}
		if !seen[f] {
			seen[f] = true
			unique = append(unique, f)
		}
	}
	return unique
}

// termFrequency computes the TF vector for the given tokens.
func termFrequency(tokens []string) map[string]float64 {
	tf := make(map[string]float64, len(tokens))
	for _, t := range tokens {
		tf[t]++
	}
	// Normalise by document length
	n := float64(len(tokens))
	if n > 0 {
		for k := range tf {
			tf[k] /= n
		}
	}
	return tf
}

// similarity computes cosine-like overlap between two token sets.
// Uses TF vectors and returns a score between 0 and 1.
func similarity(qTokens, dTokens []string) float64 {
	qTF := termFrequency(qTokens)
	dTF := termFrequency(dTokens)

	// Build union of keys
	keys := make(map[string]bool)
	for k := range qTF {
		keys[k] = true
	}
	for k := range dTF {
		keys[k] = true
	}

	if len(keys) == 0 {
		return 0
	}

	var dot, qNorm, dNorm float64
	for k := range keys {
		dot += qTF[k] * dTF[k]
		qNorm += qTF[k] * qTF[k]
		dNorm += dTF[k] * dTF[k]
	}

	if qNorm == 0 || dNorm == 0 {
		return 0
	}
	return dot / (sqrtF(qNorm) * sqrtF(dNorm))
}

func sqrtF(f float64) float64 {
	// Simple Newton method — good enough for this use case
	if f <= 0 {
		return 0
	}
	x := f
	for i := 0; i < 10; i++ {
		x = (x + f/x) / 2
	}
	return x
}

// MatchScore returns the relevance score between a query and a correction.
// It scores against both the context and the wrong response.
func (c *Correction) MatchScore(queryTokens []string) float64 {
	ctxScore := similarity(queryTokens, tokenize(c.Context))
	wrongScore := similarity(queryTokens, tokenize(c.Wrong))
	// Weight context slightly higher — the user query typically
	// resembles the context more than the wrong response.
	return 0.6*ctxScore + 0.4*wrongScore
}

// FormatPrompt formats corrections into a prompt snippet for injection.
func FormatPrompt(corrections []*Correction) string {
	if len(corrections) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Relevant past corrections from the user:\n")
	for i, c := range corrections {
		b.WriteString(fmt.Sprintf("%d. When the user said something like: %q\n", i+1, truncate(c.Context, 150)))
		b.WriteString(fmt.Sprintf("   And you responded: %q\n", truncate(c.Wrong, 150)))
		b.WriteString(fmt.Sprintf("   The user corrected you with: %q\n", truncate(c.Correct, 150)))
	}
	b.WriteString("Take these into account — the user has strong preferences.\n")
	return b.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
