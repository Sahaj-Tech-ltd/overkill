// Package reflect — Reflexion-class self-correction (paper #51
// AlphaGRPO / Shinn 2023 Reflexion). Pairs with §4.14 self-aware
// error recovery: when a tool call fails, generate a STRUCTURED
// verbal reflection ("you tried X, it failed because Y, try Z") and
// inject it as a system message before the next model call.
//
// Design split:
//
//   - IsFailureOutput / classify the result. Errors are obvious; the
//     harder shape is "tool reported success but the body contains
//     'FAIL', 'error:', exit-code 1, etc." — test runners and
//     linters do this constantly.
//   - Reflector interface: Reflect(Failure) -> Reflection. Two
//     impls today:
//   - HeuristicReflector: deterministic, no LLM, instant.
//     Catches the obvious shapes (build error, test failure,
//     permission denied, not-found) by pattern. Cheap rope.
//   - LLM-backed impl is deliberately not in this package
//     yet — keeping the wiring above the LLM dep so callers
//     can pick. The interface is the contract.
//   - FormatSystemNote: prose the agent reads. Stays terse so the
//     model doesn't drown its own reasoning in the reflection.
package reflect

import (
	"fmt"
	"regexp"
	"strings"
)

// Failure is the input to the reflector. Output / Error may both be
// populated (a Go error AND a failure-shaped body); reflectors should
// read both.
type Failure struct {
	ToolName string
	Input    string // raw JSON argument, may be empty
	Output   string // stringified tool output
	Error    string // Go-level error, empty if none
}

// FailureMode is a coarse bucket the reflector tags the failure
// with. Used both for grouping in the failhypo store and for the
// HeuristicReflector to pick a retry plan.
type FailureMode string

const (
	FailureBuildError       FailureMode = "build_error"
	FailureTestFailure      FailureMode = "test_failure"
	FailureLintError        FailureMode = "lint_error"
	FailurePermissionDenied FailureMode = "permission_denied"
	FailureNotFound         FailureMode = "not_found"
	FailureTimeout          FailureMode = "timeout"
	FailureSyntax           FailureMode = "syntax_error"
	FailureGeneric          FailureMode = "generic"
)

// Reflection is what the reflector produces. RootCause is the
// one-sentence diagnosis; Hypothesis is what to try next; the agent
// sees both in the injected system note.
type Reflection struct {
	Mode       FailureMode
	RootCause  string
	Hypothesis string
	// Confidence is the reflector's own estimate (0..1). Below 0.5
	// the caller may choose to surface the failure to the user
	// instead of retrying silently.
	Confidence float64
}

// Reflector is the minimal surface the agent loop calls after a
// failed tool. Keep it tiny — single method, no error path
// (failure-mode reflection should be best-effort).
type Reflector interface {
	Reflect(f Failure) Reflection
}

// ── Failure classification ──────────────────────────────────────────

var (
	// reTestFail matches go test / pytest / jest / cargo test
	// failure shapes. Conservative — single anchor "FAIL" or
	// "failed" near a test-runner-shaped line.
	reTestFail = regexp.MustCompile(`(?im)^(?:--- FAIL|FAIL\s+|FAILED\s+|\d+\s+failed,|ok\s+\S+\s+\d+\.\d+s\s+FAIL)`)
	// reBuildErr matches compiler error shapes across Go / Rust / TS.
	reBuildErr = regexp.MustCompile(`(?i)(?:^|\b)(?:error:|error\[E\d+\]|cannot find|undeclared|undefined reference|TS\d{4,5}:|syntax error)`)
	reLintErr  = regexp.MustCompile(`(?im)^[^\s].*:\d+:\d+:\s+(error|warning):`)
	rePermErr  = regexp.MustCompile(`(?i)\bpermission denied\b|\beacces\b|\beperm\b`)
	reNotFound = regexp.MustCompile(`(?i)\b(?:no such file|not found|no such directory|cannot find file)\b`)
	reTimeout  = regexp.MustCompile(`(?i)\b(?:timed out|deadline exceeded|context cancell?ed)\b`)
	reSyntax   = regexp.MustCompile(`(?i)\b(?:syntaxerror|invalid syntax|unexpected token|unexpected EOF)\b`)
)

// IsFailureOutput returns true when the tool's output body — even
// without a Go-level error — looks like a failure the agent should
// reflect on. Conservative: we err on the side of NOT reflecting
// since each reflection injects a system message and costs model
// attention.
func IsFailureOutput(toolName, output string) bool {
	if output == "" {
		return false
	}
	return reTestFail.MatchString(output) ||
		reBuildErr.MatchString(output) ||
		rePermErr.MatchString(output) ||
		reNotFound.MatchString(output) ||
		reTimeout.MatchString(output) ||
		reSyntax.MatchString(output)
}

// classify picks the FailureMode for a given failure. Order matters
// — more specific patterns first.
func classify(f Failure) FailureMode {
	probe := f.Error + "\n" + f.Output
	switch {
	case reTestFail.MatchString(probe):
		return FailureTestFailure
	case reBuildErr.MatchString(probe):
		return FailureBuildError
	case reSyntax.MatchString(probe):
		return FailureSyntax
	case reLintErr.MatchString(probe):
		return FailureLintError
	case rePermErr.MatchString(probe):
		return FailurePermissionDenied
	case reNotFound.MatchString(probe):
		return FailureNotFound
	case reTimeout.MatchString(probe):
		return FailureTimeout
	default:
		return FailureGeneric
	}
}

// ── HeuristicReflector ──────────────────────────────────────────────

// HeuristicReflector is the no-LLM baseline. It classifies the
// failure mode and picks a canned root-cause + hypothesis from a
// small table. The point: even without spending a model call, the
// agent sees a STRUCTURED reflection on the next turn instead of
// just the raw error — that alone moves it off the "retry the same
// thing" attractor.
type HeuristicReflector struct{}

func (HeuristicReflector) Reflect(f Failure) Reflection {
	mode := classify(f)
	root, hyp := canned(mode, f)
	return Reflection{
		Mode:       mode,
		RootCause:  root,
		Hypothesis: hyp,
		Confidence: 0.6,
	}
}

// canned maps FailureMode → (root cause, retry hypothesis). Kept
// terse on purpose; the agent expands on these in its next turn.
func canned(mode FailureMode, f Failure) (string, string) {
	excerpt := firstLine(f.Output)
	if excerpt == "" {
		excerpt = firstLine(f.Error)
	}
	switch mode {
	case FailureBuildError:
		return "compiler rejected the change: " + excerpt,
			"read the error span, fix the type/symbol referenced, do not re-run the same edit"
	case FailureTestFailure:
		return "test failed: " + excerpt,
			"open the failing assertion, decide if the test or the code is wrong — do not edit the test to make it pass without reason"
	case FailureLintError:
		return "linter flagged: " + excerpt,
			"address the lint warning at its source, not by silencing the linter"
	case FailurePermissionDenied:
		return "permission denied: " + excerpt,
			"check filesystem ownership / sudo requirements before retrying; if the path is intentional, ask the user before chmod"
	case FailureNotFound:
		return "target not found: " + excerpt,
			"verify the path with ls / git ls-files; the file may have been renamed or never created"
	case FailureTimeout:
		return "operation timed out: " + excerpt,
			"narrow the scope or split into smaller calls — retrying the same long-running command will fail the same way"
	case FailureSyntax:
		return "syntax error: " + excerpt,
			"re-read the surrounding code; the previous edit likely introduced an unbalanced delimiter or stray character"
	default:
		return "tool reported failure: " + excerpt,
			"slow down — re-read the error before retrying, and don't repeat the exact same call"
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// ── Rendering ───────────────────────────────────────────────────────

// FormatSystemNote produces the prose injected as a system message
// before the next model turn. Kept short so it doesn't dominate the
// context — the model still has the raw tool result above; this is
// the structured cue, not a replacement.
func FormatSystemNote(toolName string, r Reflection) string {
	return fmt.Sprintf(
		"[reflexion] %s failed (%s).\n  cause: %s\n  next step: %s",
		toolName, r.Mode, r.RootCause, r.Hypothesis,
	)
}
