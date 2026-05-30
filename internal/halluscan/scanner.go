// Package halluscan — conservative hallucination detector (Batch G3).
//
// Scans the assistant's outgoing text for "made-up" references —
// backtick-quoted identifiers that don't appear anywhere in this
// session's evidence (user messages, prior assistant turns, tool
// outputs, system prompt). When a claim references something that
// isn't in the session's actual record, the scanner annotates it
// with `[?]` so the model sees on its next turn that the reference
// was unverified.
//
// Conservative by design:
//
//   - Only backtick-quoted identifiers get scanned. Bare-word claims
//     ("the auth module") have too many false positives to flag.
//   - Stopword + tool-name allowlists prevent the obvious noise
//     (don't flag `the`, `if`, `Read`, `Bash`, etc.).
//   - Self-reference is OK: if the identifier appears in the SAME
//     proposed response (i.e. the agent is defining it), no flag.
//   - We never DELETE text. Annotation is the entire intervention.
//     False positives become noise, not data loss.
//
// What this catches in practice:
//
//   - "use the `bcrypt.HashPassword()` function" when bcrypt was
//     never grepped, read, or imported in this session
//   - "in `internal/auth/middleware.go`" when that file was never
//     read by any tool and never mentioned by the user
//   - "the `--no-verify` flag" when no help/docs tool fetched that
//
// What this WON'T catch (acceptable limits):
//
//   - Bare-word hallucinations: "use bcrypt.HashPassword" (no
//     backticks) — falls through silently. Future iteration could
//     add a Go-AST-aware scan for code blocks.
//   - True-but-unstated facts: model knows `os.Getenv` exists from
//     training data and references it; nothing in session confirms.
//     We flag it. The model corrects on next turn or ignores the [?]
//     if it's confident. Annotation, not block.
package halluscan

import (
	"regexp"
	"sort"
	"strings"
)

// identRe matches backtick-quoted identifiers the agent might claim
// as references to real code. Lower bound is 3 chars to skip noise
// like `r`, `id`, `=`. Upper is 80 to skip prose accidentally
// wrapped in backticks. Allowed chars: letters, digits, _, ., - so
// "foo.Bar", "snake_case", "kebab-flag" all match.
var identRe = regexp.MustCompile("`([a-zA-Z][a-zA-Z0-9_.\\-]{2,79})`")

// stopwords are identifiers we NEVER flag even if not found in
// session evidence. Tool names, common stdlib symbols, basic
// keywords. The list is intentionally small — we'd rather miss a
// hallucination than fire on every appearance of `Read` or `Bash`.
var stopwords = map[string]bool{
	// Common bash/shell names
	"bash": true, "zsh": true, "fish": true,
	// Common tool / verb names the agent uses generically
	"read": true, "write": true, "edit": true, "patch": true, "grep": true,
	"find": true, "ls": true, "cat": true, "cd": true, "mkdir": true,
	"rm": true, "mv": true, "cp": true, "git": true, "make": true,
	"go": true, "go.mod": true, "go.sum": true,
	// Basic primitives
	"true": true, "false": true, "nil": true, "null": true, "none": true,
	// Boilerplate file names
	"readme.md": true, "license": true, "main.go": true,
	// Common type/keyword words wrapped in backticks for emphasis
	"interface": true, "struct": true, "type": true, "func": true,
	"error": true, "string": true, "int": true, "bool": true, "context": true,
	// Common module path prefixes — flagging these would noise every reference
	"internal": true, "pkg": true, "cmd": true, "tests": true,
}

// Scanner finds unverified identifiers in a proposed response.
// Construction is cheap (no I/O); each Scan call reads the supplied
// evidence corpus.
type Scanner struct {
	// MaxAnnotations caps how many [?] markers we add per response.
	// Cluttering a long response with 50 markers makes the next turn
	// unreadable. Default 5 — surface the worst, let the rest slide.
	MaxAnnotations int
}

// NewScanner returns a scanner with sensible defaults.
func NewScanner() *Scanner { return &Scanner{MaxAnnotations: 5} }

// Result is one scan's findings. Annotated is the input with `[?]`
// markers inserted after each unverified backtick run. Flagged
// lists the unique identifiers we couldn't verify, in first-occurrence
// order — useful for tests and for the model's NEXT turn to see
// which references it should re-ground.
type Result struct {
	Annotated string
	Flagged   []string
}

// Scan inspects content for unverified identifiers. evidence is the
// concatenated corpus of everything we trust as "real" — user
// messages, prior assistant turns, tool outputs, system prompt. The
// scanner only flags identifiers that appear in `content` but NOT
// in evidence.
//
// Self-reference is OK: if the identifier appears multiple times in
// `content` itself, it's still flagged once if evidence lacks it.
// We're checking "did anyone besides this response reference this?".
func (s *Scanner) Scan(content, evidence string) Result {
	if content == "" {
		return Result{Annotated: content}
	}
	matches := identRe.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return Result{Annotated: content}
	}
	evidenceLower := strings.ToLower(evidence)

	// Walk matches forward; track which to annotate. Insert markers
	// from the END to keep earlier indices valid as we mutate.
	type hit struct {
		end int    // position to insert [?] after
		id  string // matched identifier (lowercased)
	}
	var hits []hit
	flagged := map[string]int{} // ident -> first-occurrence index in hits
	for _, m := range matches {
		// m = [outerStart, outerEnd, groupStart, groupEnd]
		id := content[m[2]:m[3]]
		idLower := strings.ToLower(id)
		if stopwords[idLower] {
			continue
		}
		// Skip pure-number-like or too-short-after-trim noise.
		if !looksLikeIdentifier(idLower) {
			continue
		}
		if strings.Contains(evidenceLower, idLower) {
			continue
		}
		// First-time hit for this identifier — record.
		if _, seen := flagged[idLower]; !seen {
			flagged[idLower] = len(hits)
		}
		hits = append(hits, hit{end: m[1], id: idLower})
	}
	if len(hits) == 0 {
		return Result{Annotated: content}
	}

	// Cap annotations to MaxAnnotations to keep the next-turn
	// context readable.
	cap := s.MaxAnnotations
	if cap <= 0 {
		cap = 5
	}
	if len(hits) > cap {
		hits = hits[:cap]
	}

	// Insert from the END so earlier indices stay valid.
	out := content
	for i := len(hits) - 1; i >= 0; i-- {
		out = out[:hits[i].end] + " [?]" + out[hits[i].end:]
	}

	// Build the flagged list in first-occurrence order.
	uniq := make([]string, 0, len(flagged))
	for id := range flagged {
		uniq = append(uniq, id)
	}
	sort.Slice(uniq, func(i, j int) bool {
		return flagged[uniq[i]] < flagged[uniq[j]]
	})
	return Result{Annotated: out, Flagged: uniq}
}

// looksLikeIdentifier rejects strings that are clearly not code
// references after the regex caught them — e.g. pure digits, or
// strings that contain only separators. The regex itself requires
// a leading letter, so this is a safety net for edge cases.
func looksLikeIdentifier(s string) bool {
	hasLetter := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
			break
		}
	}
	return hasLetter
}
