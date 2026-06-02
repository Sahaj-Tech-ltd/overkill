// Package verify — reward-hacking detector (paper #48 design input).
//
// Specific failure mode OpenAI's monitoring paper called out: agents
// editing test files to make them pass instead of fixing the bug.
// "All green" without the underlying code actually being fixed.
//
// Detection heuristic: when a test file is modified but the
// corresponding source file isn't modified in the same turn, flag
// it. This is intentionally a WARNING, not a block — false positives
// happen (adding a new test for already-working code; refactoring
// a flaky assertion; renaming a test). The model sees the warning
// and either justifies the change or fixes the actual bug.
//
// Heuristic IS heuristic — we treat it as a nudge, not a gate.
package verify

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RewardHackFinding describes one test-without-code pair detected
// in a turn's written paths.
type RewardHackFinding struct {
	TestPath     string
	ExpectedCode string // what we'd expect to see modified alongside
	Reason       string
}

// AuditPaths walks the list of paths written in a single agent turn
// and returns findings for each test file that lacks a corresponding
// code-file modification. paths should be the deduplicated absolute
// (or cwd-relative — both work, we just need a consistent set) list
// of every file the turn's tools touched.
//
// Returns empty slice when nothing's suspicious — including the
// common case where the agent legitimately added a new test for
// existing code. We only flag "test changed but code did NOT" with
// strong-enough confidence.
func AuditPaths(paths []string) []RewardHackFinding {
	if len(paths) == 0 {
		return nil
	}
	// Build a set for O(1) "did this file also get modified?" checks.
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[filepath.Clean(p)] = true
	}

	var out []RewardHackFinding
	for _, p := range paths {
		clean := filepath.Clean(p)
		if !looksLikeTestFile(clean) {
			continue
		}
		expected := expectedCodePath(clean)
		if expected == "" {
			// Unrecognized test layout — skip rather than false-flag.
			continue
		}
		if set[expected] {
			continue
		}
		// Also accept any path in the same directory as a near-match
		// (the agent might have refactored a related but differently-
		// named file). This is the conservative "give the model some
		// rope" exit before firing.
		if anyCodeInSameDir(set, clean) {
			continue
		}
		out = append(out, RewardHackFinding{
			TestPath:     p,
			ExpectedCode: expected,
			Reason:       "test file modified without its corresponding code file in the same turn — verify this isn't lowering the bar",
		})
	}
	return out
}

// looksLikeTestFile classifies a path as a test by extension /
// filename / directory conventions across the languages Overkill
// agents commonly touch.
func looksLikeTestFile(path string) bool {
	base := filepath.Base(path)
	lower := strings.ToLower(base)
	parts := strings.Split(filepath.ToSlash(path), "/")

	// Go
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	// JS/TS — .test. or .spec. infix
	if strings.HasSuffix(lower, ".test.ts") || strings.HasSuffix(lower, ".test.tsx") ||
		strings.HasSuffix(lower, ".test.js") || strings.HasSuffix(lower, ".test.jsx") ||
		strings.HasSuffix(lower, ".spec.ts") || strings.HasSuffix(lower, ".spec.tsx") ||
		strings.HasSuffix(lower, ".spec.js") || strings.HasSuffix(lower, ".spec.jsx") {
		return true
	}
	// Python — test_*.py or *_test.py
	if strings.HasSuffix(lower, ".py") &&
		(strings.HasPrefix(lower, "test_") || strings.HasSuffix(lower, "_test.py")) {
		return true
	}
	// Rust — *_test.rs OR anything under tests/
	if strings.HasSuffix(lower, "_test.rs") {
		return true
	}
	// Generic: path segment "test" or "tests" or "__tests__"
	for _, p := range parts {
		switch strings.ToLower(p) {
		case "test", "tests", "__tests__", "spec":
			return true
		}
	}
	return false
}

// expectedCodePath derives the "what should have been modified
// alongside this test" path. Returns "" when the test naming doesn't
// follow a convention we can invert reliably — the caller skips
// rather than false-flag.
func expectedCodePath(testPath string) string {
	dir := filepath.Dir(testPath)
	base := filepath.Base(testPath)
	lower := strings.ToLower(base)

	switch {
	case strings.HasSuffix(base, "_test.go"):
		return filepath.Join(dir, strings.TrimSuffix(base, "_test.go")+".go")
	case strings.HasSuffix(lower, ".test.ts"):
		return filepath.Join(dir, base[:len(base)-len(".test.ts")]+".ts")
	case strings.HasSuffix(lower, ".test.tsx"):
		return filepath.Join(dir, base[:len(base)-len(".test.tsx")]+".tsx")
	case strings.HasSuffix(lower, ".test.js"):
		return filepath.Join(dir, base[:len(base)-len(".test.js")]+".js")
	case strings.HasSuffix(lower, ".spec.ts"):
		return filepath.Join(dir, base[:len(base)-len(".spec.ts")]+".ts")
	case strings.HasSuffix(lower, ".spec.tsx"):
		return filepath.Join(dir, base[:len(base)-len(".spec.tsx")]+".tsx")
	case strings.HasSuffix(lower, ".spec.js"):
		return filepath.Join(dir, base[:len(base)-len(".spec.js")]+".js")
	case strings.HasSuffix(lower, "_test.py"):
		return filepath.Join(dir, strings.TrimSuffix(base, "_test.py")+".py")
	case strings.HasPrefix(lower, "test_") && strings.HasSuffix(lower, ".py"):
		return filepath.Join(dir, strings.TrimPrefix(base, "test_"))
	case strings.HasSuffix(lower, "_test.rs"):
		return filepath.Join(dir, strings.TrimSuffix(base, "_test.rs")+".rs")
	}
	return ""
}

// anyCodeInSameDir gives the agent some rope: if ANY non-test file
// in the same directory was modified, the test change probably
// correlates with the code change even if the exact filename pairing
// doesn't match. This catches the "I refactored utils.go and updated
// helpers_test.go" case without firing false positives on legitimate
// cross-file work.
func anyCodeInSameDir(set map[string]bool, testPath string) bool {
	dir := filepath.Dir(testPath)
	for p := range set {
		if p == testPath {
			continue
		}
		if filepath.Dir(p) != dir {
			continue
		}
		if !looksLikeTestFile(p) {
			return true
		}
	}
	return false
}

// FormatRewardHackMessage renders findings as a single tool-message
// body the agent sees on its next turn. Empty when no findings —
// caller skips emission.
func FormatRewardHackMessage(findings []RewardHackFinding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[reward-hack check] ")
	if len(findings) == 1 {
		b.WriteString("1 test file modified without its code:\n\n")
	} else {
		fmt.Fprintf(&b, "%d test files modified without their code:\n\n", len(findings))
	}
	for _, f := range findings {
		fmt.Fprintf(&b, "  %s\n", f.TestPath)
		fmt.Fprintf(&b, "    expected: %s also modified\n", f.ExpectedCode)
		fmt.Fprintf(&b, "    %s\n", f.Reason)
	}
	b.WriteString("\nIf this is legitimate (new test for existing code, renaming a flaky assertion, refactor), explain. If you're making the test pass by lowering the bar, fix the code instead.")
	return b.String()
}
