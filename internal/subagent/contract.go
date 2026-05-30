// Package subagent — Contract type and validation/enforcement primitives.
//
// A Contract is the structured handoff between a parent agent and a child
// sub-agent. The parent freezes it before spawning; the child runs to
// completion against it without further user/parent intervention. See
// master plan §5.3 for the autonomous-execution rationale.
package subagent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OnContextFullAction tells the autonomous runner what to do when its child's
// context budget approaches the configured cap.
type OnContextFullAction string

const (
	OnContextFullCompactAndContinue OnContextFullAction = "compact_and_continue"
	OnContextFullHandoffToParent    OnContextFullAction = "handoff_to_parent"
	OnContextFullFail               OnContextFullAction = "fail"
)

// ContextRefKind classifies an Inputs entry the parent passes down.
type ContextRefKind string

const (
	CtxRefFile        ContextRefKind = "file"
	CtxRefPriorResult ContextRefKind = "prior_result"
	CtxRefSpec        ContextRefKind = "spec"
	CtxRefNote        ContextRefKind = "note"
)

// ContextRef is one input the parent passes to the child.
type ContextRef struct {
	Type  ContextRefKind `json:"type"`
	Value string         `json:"value"`
}

// OutputKind classifies an ExpectedOutputs entry.
type OutputKind string

const (
	OutFile     OutputKind = "file"
	OutSymbol   OutputKind = "symbol"
	OutBehavior OutputKind = "behavior"
	OutTest     OutputKind = "test"
)

// Output is one concrete deliverable the child must produce.
type Output struct {
	Kind OutputKind `json:"kind"`
	Spec string     `json:"spec"`
}

// Integration captures an interface the child's output must conform to.
type Integration struct {
	Description string `json:"description"`
	Reference   string `json:"reference,omitempty"`
}

// AcceptanceCheck is a testable command + expectation pair.
type AcceptanceCheck struct {
	Name                 string `json:"name"`
	Cmd                  string `json:"cmd"`
	ExpectExit           int    `json:"expect_exit"`
	ExpectStdoutContains string `json:"expect_stdout_contains,omitempty"`
	TimeoutSeconds       int    `json:"timeout_seconds,omitempty"`
}

// AcceptanceResult is the outcome of running one AcceptanceCheck.
type AcceptanceResult struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ContractBudget caps a child's resource consumption.
type ContractBudget struct {
	Tokens      int           `json:"tokens"`
	Steps       int           `json:"steps"`
	WallSeconds time.Duration `json:"wall_seconds"`
}

// Contract is the frozen agreement between parent and child.
type Contract struct {
	ID                string              `json:"id"`
	ParentSession     string              `json:"parent_session"`
	Goal              string              `json:"goal"`
	Scope             []string            `json:"scope"`
	OutOfScope        []string            `json:"out_of_scope"`
	Inputs            []ContextRef        `json:"inputs"`
	ExpectedOutputs   []Output            `json:"expected_outputs"`
	IntegrationPoints []Integration       `json:"integration_points,omitempty"`
	Acceptance        []AcceptanceCheck   `json:"acceptance"`
	Budget            ContractBudget      `json:"budget"`
	OnContextFull     OnContextFullAction `json:"on_context_full"`
}

// ContractViolation describes a criterion the child failed to satisfy.
type ContractViolation struct {
	Criterion string `json:"criterion"`
	Reason    string `json:"reason"`
	Evidence  string `json:"evidence,omitempty"`
}

func (v ContractViolation) Error() string {
	return fmt.Sprintf("contract violation: %s — %s", v.Criterion, v.Reason)
}

// Validate enforces well-formedness invariants. Returns the first failure
// encountered; callers should fix one error at a time.
func (c *Contract) Validate() error {
	if c == nil {
		return errors.New("contract: nil")
	}
	if strings.TrimSpace(c.Goal) == "" {
		return errors.New("contract: goal is required")
	}
	if c.ID == "" {
		return errors.New("contract: id is required")
	}
	if len(c.ExpectedOutputs) == 0 {
		return errors.New("contract: at least one expected output is required")
	}
	for i, o := range c.ExpectedOutputs {
		if strings.TrimSpace(o.Spec) == "" {
			return fmt.Errorf("contract: expected_outputs[%d].spec is empty", i)
		}
		switch o.Kind {
		case OutFile, OutSymbol, OutBehavior, OutTest:
		default:
			return fmt.Errorf("contract: expected_outputs[%d].kind invalid: %q", i, o.Kind)
		}
	}

	// Scope/out-of-scope contradiction check: any literal scope path that
	// also matches an out-of-scope glob is a configuration bug.
	for _, s := range c.Scope {
		for _, x := range c.OutOfScope {
			if matched, _ := filepath.Match(x, s); matched {
				return fmt.Errorf("contract: scope %q overlaps out_of_scope pattern %q", s, x)
			}
		}
	}

	// Every ExpectedOutput needs a way to be verified. We accept either a
	// matching acceptance check OR an OutFile spec (which we can stat).
	for i, o := range c.ExpectedOutputs {
		if o.Kind == OutFile {
			continue // verifiable via filesystem
		}
		if hasNamedAcceptance(c.Acceptance, o.Spec) {
			continue
		}
		// fallback: any acceptance check at all is enough; otherwise error.
		if len(c.Acceptance) == 0 {
			return fmt.Errorf("contract: expected_outputs[%d] (%s) has no way to verify (add an acceptance check)", i, o.Spec)
		}
	}

	switch c.OnContextFull {
	case "", OnContextFullCompactAndContinue, OnContextFullHandoffToParent, OnContextFullFail:
	default:
		return fmt.Errorf("contract: invalid on_context_full %q", c.OnContextFull)
	}

	if c.Budget.Steps < 0 || c.Budget.Tokens < 0 || c.Budget.WallSeconds < 0 {
		return errors.New("contract: budget fields must be >= 0")
	}

	return nil
}

func hasNamedAcceptance(checks []AcceptanceCheck, name string) bool {
	for _, c := range checks {
		if c.Name == name {
			return true
		}
	}
	return false
}

// CheckScope reports whether the given filesystem path is allowed for write
// under this contract. Out-of-scope patterns take precedence over scope.
// An empty Scope means "no scope restriction" (everything allowed except
// out_of_scope). If both are empty, all writes are allowed.
func (c *Contract) CheckScope(path string) (allowed bool, reason string) {
	if c == nil {
		return true, ""
	}
	clean := filepath.Clean(path)

	// Out-of-scope wins.
	for _, pat := range c.OutOfScope {
		if matchPathOrGlob(pat, clean) {
			return false, fmt.Sprintf("path %q matches out_of_scope pattern %q", clean, pat)
		}
	}

	if len(c.Scope) == 0 {
		return true, ""
	}

	for _, pat := range c.Scope {
		if matchPathOrGlob(pat, clean) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("path %q is outside contract scope", clean)
}

// matchPathOrGlob accepts both a literal directory prefix ("internal/foo")
// and a filepath.Match-style glob ("internal/foo/**", "*.go"). For directory
// prefixes we also accept any descendant.
func matchPathOrGlob(pattern, path string) bool {
	if pattern == "" {
		return false
	}
	if matched, err := filepath.Match(pattern, path); err == nil && matched {
		return true
	}
	// Recursive glob support: "foo/**" matches "foo/anything".
	if strings.HasSuffix(pattern, "/**") {
		base := strings.TrimSuffix(pattern, "/**")
		if path == base || strings.HasPrefix(path, base+string(filepath.Separator)) {
			return true
		}
	}
	// Plain prefix match (treat scope entry as a directory prefix).
	if path == pattern || strings.HasPrefix(path, pattern+string(filepath.Separator)) {
		return true
	}
	return false
}

// AcceptanceRunner is the surface RunAcceptance uses to invoke commands.
// Defaults to the OS shell; tests inject a fake.
type AcceptanceRunner func(ctx context.Context, cmd string, timeout time.Duration, workdir string) (exitCode int, stdout, stderr string, err error)

// DefaultAcceptanceRunner runs the command with `sh -c` in workdir.
//
// WARNING: This executes arbitrary shell commands from AcceptanceCheck.Cmd,
// which may be LLM-generated or user-authored. Callers should prefer
// ScannedAcceptanceRunner with a command scanner to block known-dangerous
// patterns. This raw runner is provided for cases where no scanner is
// configured.
func DefaultAcceptanceRunner(ctx context.Context, cmd string, timeout time.Duration, workdir string) (int, string, string, error) {
	if cmd == "" {
		return -1, "", "", errors.New("acceptance: empty command")
	}
	// Reject commands containing shell metacharacters that suggest
	// injection attempts (command chaining, subshells, etc.).
	if containsShellMetachar(cmd) {
		return -1, "", "", fmt.Errorf("acceptance: command contains potentially dangerous shell metacharacters: %q", cmd)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(cctx, "sh", "-c", cmd)
	if workdir != "" {
		c.Dir = workdir
	}
	var so, se strings.Builder
	c.Stdout = &so
	c.Stderr = &se
	runErr := c.Run()
	exit := 0
	if c.ProcessState != nil {
		exit = c.ProcessState.ExitCode()
	}
	if runErr != nil && c.ProcessState == nil {
		return -1, so.String(), se.String(), runErr
	}
	return exit, so.String(), se.String(), nil
}

// AcceptanceScanner is a minimal command-scanning interface. Accept any
// implementation that has a Scan method returning a blocked boolean and
// error — no import dependency on internal/security required.
type AcceptanceScanner interface {
	Scan(cmd string) (blocked bool, err error)
}

// ScannedAcceptanceRunner wraps DefaultAcceptanceRunner with a command
// scanner. When scanner is nil, it delegates to DefaultAcceptanceRunner
// directly. When a scanner is provided, commands are checked before
// execution and blocked if the scanner returns blocked=true (B108).
func ScannedAcceptanceRunner(scanner AcceptanceScanner) AcceptanceRunner {
	if scanner == nil {
		return DefaultAcceptanceRunner
	}
	return func(ctx context.Context, cmd string, timeout time.Duration, workdir string) (int, string, string, error) {
		blocked, err := scanner.Scan(cmd)
		if err != nil {
			return -1, "", "", fmt.Errorf("acceptance: scanner error: %w", err)
		}
		if blocked {
			return -1, "", "", fmt.Errorf("acceptance: command blocked by scanner: %s", cmd)
		}
		return DefaultAcceptanceRunner(ctx, cmd, timeout, workdir)
	}
}

// RunAcceptance executes every acceptance check against workdir and reports
// per-check outcomes. Never returns nil; an empty slice means no checks.
func (c *Contract) RunAcceptance(ctx context.Context, workdir string, runner AcceptanceRunner) []AcceptanceResult {
	if c == nil || len(c.Acceptance) == 0 {
		return []AcceptanceResult{}
	}
	if runner == nil {
		runner = DefaultAcceptanceRunner
	}
	out := make([]AcceptanceResult, 0, len(c.Acceptance))
	for _, ck := range c.Acceptance {
		timeout := time.Duration(ck.TimeoutSeconds) * time.Second
		exit, so, se, err := runner(ctx, ck.Cmd, timeout, workdir)
		r := AcceptanceResult{
			Name:     ck.Name,
			ExitCode: exit,
			Stdout:   so,
			Stderr:   se,
		}
		if err != nil {
			r.Passed = false
			r.Reason = "runner error: " + err.Error()
			out = append(out, r)
			continue
		}
		if exit != ck.ExpectExit {
			r.Passed = false
			r.Reason = fmt.Sprintf("exit %d != expected %d", exit, ck.ExpectExit)
			out = append(out, r)
			continue
		}
		if ck.ExpectStdoutContains != "" && !strings.Contains(so, ck.ExpectStdoutContains) {
			r.Passed = false
			r.Reason = fmt.Sprintf("stdout missing expected substring %q", ck.ExpectStdoutContains)
			out = append(out, r)
			continue
		}
		r.Passed = true
		out = append(out, r)
	}
	return out
}

// containsShellMetachar checks a command string for common shell injection
// patterns: command chaining (&&, ||, ;, |), subshells ($(), ``), redirection
// quirks, and newline injection. This is a pragmatic first line of defence,
// not a complete shell parser. Commands that need these characters should
// use an explicit shell script or be rewritten as individual argv elements.
func containsShellMetachar(cmd string) bool {
	for _, r := range cmd {
		switch r {
		case '&', '|', ';', '$', '`', '\n', '\r':
			return true
		}
	}
	return false
}
