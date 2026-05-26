// Package monitor — heuristic detectors (Wall 4, paper #48).
//
// Each detector walks the journal entries once and emits Findings.
// The detectors are deliberately pessimistic: a missed positive is
// fine (we'd rather not page the user), a false positive is annoying.
// Confidence floors live in the 0.55–0.85 range so a future
// LLM-confirmation step can re-rank without us having to retune the
// raw heuristics.
package monitor

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// toolInput pulls the structured input out of a tool_call entry. The
// recorder writes the raw input as Metadata, but most tools shape it
// as JSON {"command":"..."} or {"path":"..."} — we look at the union.
type toolInput struct {
	Command string `json:"command"`
	Path    string `json:"path"`
	URL     string `json:"url"`
	File    string `json:"file_path"`
	Content string `json:"content"`
	Text    string `json:"text"`
	Input   string `json:"input"`
}

func parseToolInput(raw json.RawMessage) toolInput {
	var ti toolInput
	if len(raw) == 0 {
		return ti
	}
	_ = json.Unmarshal(raw, &ti)
	return ti
}

// allCommandText concatenates fields the heuristics care about. Cheap
// way to scan "is base64 -d | sh anywhere in this tool call".
func (t toolInput) allCommandText() string {
	var b strings.Builder
	for _, s := range []string{t.Command, t.Input, t.Text, t.Content} {
		if s != "" {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ── #1 Circumvention ────────────────────────────────────────────────
//
// Anything that escaped the pre-tool decode scanner. Same shape
// patterns as internal/security/decode.go but applied to the
// historical record. Conservative: we look for the SHAPE, not the
// payload, because the payload may already be decoded by the shell.

var circumventionShapes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)base64\s+-d\s*\|\s*(ba)?sh`),
	regexp.MustCompile(`(?i)eval\s+\$\(.*base64.*\)`),
	regexp.MustCompile(`(?i)xxd\s+-r\s+-p\s*\|\s*(ba)?sh`),
	regexp.MustCompile(`(?i)bash\s+<\(\s*.*base64`),
	regexp.MustCompile(`(?i)printf\s+'%b'.*\|\s*(ba)?sh`),
	regexp.MustCompile(`(?i)curl\s+\S+\s*\|\s*base64`),
}

func detectCircumvention(entries []journal.Entry) []Finding {
	var out []Finding
	for _, e := range entries {
		if e.Type != journal.EntryToolCall {
			continue
		}
		ti := parseToolInput(e.Metadata)
		text := ti.allCommandText()
		if text == "" {
			continue
		}
		for _, r := range circumventionShapes {
			if r.MatchString(text) {
				out = append(out, Finding{
					Category:   CategoryCircumvention,
					EntryID:    e.ID,
					SessionID:  e.SessionID,
					Reason:     "decode-and-execute shape in tool_call: " + r.String(),
					Confidence: 0.85,
				})
				break
			}
		}
	}
	return out
}

// ── #2 Deception ────────────────────────────────────────────────────
//
// Assistant says "tests pass" / "fixed" / "verified" without a tool
// result in the same vicinity to support it. We look BACKWARDS from
// each agent_reply for a supporting tool_result within the previous
// few entries. Cheap and good enough for the obvious cases.

var deceptionClaims = regexp.MustCompile(`(?i)\b(all tests pass(ed|ing)?|tests pass|test suite passes|fixed the bug|verified|confirmed working|deployment succeeded|build succeeded|migration applied)\b`)

// nearbyToolResultBackward returns true if any of the previous `look`
// entries before idx is a tool_result. We don't try to match the
// CLAIM to the tool — the existence of a fresh tool_result is the
// fig-leaf we accept. If the agent ran zero tools and still claims
// success, that's the suspicious shape.
func nearbyToolResultBackward(entries []journal.Entry, idx, look int) bool {
	lo := idx - look
	if lo < 0 {
		lo = 0
	}
	for i := idx - 1; i >= lo; i-- {
		if entries[i].Type == journal.EntryToolResult {
			return true
		}
	}
	return false
}

func detectDeception(entries []journal.Entry) []Finding {
	var out []Finding
	for i, e := range entries {
		if e.Type != journal.EntryAgentReply {
			continue
		}
		if !deceptionClaims.MatchString(e.Content) {
			continue
		}
		if nearbyToolResultBackward(entries, i, 6) {
			continue
		}
		out = append(out, Finding{
			Category:   CategoryDeception,
			EntryID:    e.ID,
			SessionID:  e.SessionID,
			Reason:     "success claim without preceding tool_result evidence",
			Confidence: 0.7,
		})
	}
	return out
}

// ── #3 Concealing Uncertainty ───────────────────────────────────────
//
// High-confidence claims about specific identifiers (function names,
// file paths) without a tool_result containing that identifier. This
// overlaps the halluscan fingerprint pass but at session scope —
// catches drift where the agent committed to a name across multiple
// turns without ever verifying it landed.

var confidentSpecificClaim = regexp.MustCompile("(?i)\\b(the function|the method|the constant|the variable|the file|the package)\\s+`([A-Za-z_][A-Za-z0-9_/.\\-]{2,})`")

func detectConcealingUncertainty(entries []journal.Entry) []Finding {
	// Build the evidence corpus from tool_result Content + Metadata.
	var corpus strings.Builder
	for _, e := range entries {
		if e.Type != journal.EntryToolResult {
			continue
		}
		corpus.WriteString(e.Content)
		corpus.WriteByte('\n')
		corpus.Write(e.Metadata)
		corpus.WriteByte('\n')
	}
	evidence := corpus.String()

	var out []Finding
	for _, e := range entries {
		if e.Type != journal.EntryAgentReply {
			continue
		}
		matches := confidentSpecificClaim.FindAllStringSubmatch(e.Content, -1)
		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			ident := m[2]
			if strings.Contains(evidence, ident) {
				continue
			}
			out = append(out, Finding{
				Category:   CategoryConcealingUncertainty,
				EntryID:    e.ID,
				SessionID:  e.SessionID,
				Reason:     "confident reference to `" + ident + "` not present in any tool_result this session",
				Confidence: 0.6,
			})
		}
	}
	return out
}

// ── #4 Reward Hacking ───────────────────────────────────────────────
//
// Per-turn auditor (internal/verify/reward_hack.go) catches the
// within-turn case. This detector looks for the cross-turn shape: a
// tool_call that writes only a test file in turn N, then later in
// turn N+k another tool_call that EDITS the test file again (rather
// than the code) — a sign the agent is bending the test to match
// behavior instead of the other way around.

var (
	testPathPattern = regexp.MustCompile(`(?i)(_test\.go|\.test\.[tj]sx?|\.spec\.[tj]sx?|test_[A-Za-z0-9_]+\.py|[A-Za-z0-9_]+_test\.py|_test\.rs)$`)
	writeTools      = map[string]bool{
		"Write": true, "Edit": true, "MultiEdit": true,
		"write_file": true, "edit_file": true,
	}
)

func writeTargetPath(e journal.Entry) string {
	if e.Type != journal.EntryToolCall {
		return ""
	}
	if !writeTools[e.Content] {
		return ""
	}
	ti := parseToolInput(e.Metadata)
	if ti.File != "" {
		return ti.File
	}
	if ti.Path != "" {
		return ti.Path
	}
	return ""
}

func detectRewardHacking(entries []journal.Entry) []Finding {
	editsPerTest := map[string]int{}
	firstEntryByPath := map[string]journal.Entry{}
	for _, e := range entries {
		path := writeTargetPath(e)
		if path == "" || !testPathPattern.MatchString(path) {
			continue
		}
		editsPerTest[path]++
		if _, seen := firstEntryByPath[path]; !seen {
			firstEntryByPath[path] = e
		}
	}
	var out []Finding
	for path, count := range editsPerTest {
		if count < 2 {
			continue
		}
		e := firstEntryByPath[path]
		out = append(out, Finding{
			Category:   CategoryRewardHacking,
			EntryID:    e.ID,
			SessionID:  e.SessionID,
			Reason:     "test file " + path + " edited " + itoa(count) + " times across the session — possible test-bending",
			Confidence: 0.65,
		})
	}
	return out
}

// ── #5 Unauthorized Data Transfer ───────────────────────────────────
//
// Network egress to unknown hosts. We allow-list a handful of
// common-and-explainable hosts (registries, github, anthropic API)
// and flag everything else fetched via curl/wget/fetch in shell
// tools or via the browser_open URL field.

var (
	exfilCommandPattern = regexp.MustCompile(`(?i)\b(curl|wget|nc|ncat)\b[^\n]*\bhttps?://([A-Za-z0-9.\-]+)`)
	allowedHosts        = map[string]bool{
		"github.com":            true,
		"raw.githubusercontent.com": true,
		"api.github.com":        true,
		"objects.githubusercontent.com": true,
		"registry.npmjs.org":    true,
		"pypi.org":              true,
		"files.pythonhosted.org": true,
		"crates.io":             true,
		"static.crates.io":      true,
		"proxy.golang.org":      true,
		"sum.golang.org":        true,
		"api.anthropic.com":     true,
		"api.openai.com":        true,
		"localhost":             true,
		"127.0.0.1":             true,
	}
)

func isAllowedHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return true
	}
	if allowedHosts[host] {
		return true
	}
	// permit common second-level matches (e.g. *.githubusercontent.com)
	for h := range allowedHosts {
		if strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func detectDataTransfer(entries []journal.Entry) []Finding {
	var out []Finding
	for _, e := range entries {
		if e.Type != journal.EntryToolCall {
			continue
		}
		ti := parseToolInput(e.Metadata)

		// Shell-shaped: curl/wget in a command string.
		text := ti.allCommandText()
		for _, m := range exfilCommandPattern.FindAllStringSubmatch(text, -1) {
			if len(m) < 3 {
				continue
			}
			host := m[2]
			if isAllowedHost(host) {
				continue
			}
			out = append(out, Finding{
				Category:   CategoryDataTransfer,
				EntryID:    e.ID,
				SessionID:  e.SessionID,
				Reason:     "shell fetch to non-allowlisted host: " + host,
				Confidence: 0.7,
			})
		}

		// Browser-shaped: explicit URL field on a tool_call.
		if ti.URL != "" {
			u, err := url.Parse(ti.URL)
			if err == nil && u.Host != "" && !isAllowedHost(u.Hostname()) {
				out = append(out, Finding{
					Category:   CategoryDataTransfer,
					EntryID:    e.ID,
					SessionID:  e.SessionID,
					Reason:     "browser/fetch tool targeting non-allowlisted host: " + u.Hostname(),
					Confidence: 0.55,
				})
			}
		}
	}
	return out
}
