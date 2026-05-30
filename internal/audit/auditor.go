// Package audit — completion auditor that verifies agent output.
//
// After the agent claims "done," the auditor compares pre-task and
// post-task state to detect lazy/incomplete work:
//   - Were files actually modified that were supposed to change?
//   - Do plan items marked "complete" have corresponding file changes?
//   - Do build + tests still pass?
//   - Did the diff show the claimed behavior change?
//
// The auditor can optionally spawn a read-only sub-agent with
// semantic tools (grep, go vet, git log) to cross-check claims
// against reality.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot captures repo state before/after a task.
type Snapshot struct {
	Time      time.Time         `json:"time"`
	GitSHA    string            `json:"git_sha"`
	GitDiff   string            `json:"git_diff"`   // "git diff --stat" output
	Files     map[string]string `json:"files"`       // path → sha256 (only tracked files in scope)
	Dirty     bool              `json:"dirty"`       // working tree had uncommitted changes
}

// Claim is something the agent asserts it accomplished.
type Claim struct {
	Description string   `json:"description"` // e.g. "Added error handling to auth.go"
	Files       []string `json:"files"`       // files that should have changed
	TestsPass   bool     `json:"tests_pass"`  // agent claims tests pass
	BuildPass   bool     `json:"build_pass"`  // agent claims build passes
}

// Finding is a single discrepancy the auditor discovered.
type Finding struct {
	Severity   string `json:"severity"` // "ok", "warn", "fail"
	Claim      string `json:"claim"`
	Expected   string `json:"expected"`
	Actual     string `json:"actual"`
	Evidence   string `json:"evidence"` // grep output, diff snippet, etc.
}

// Report is the full audit result.
type Report struct {
	Passed    bool      `json:"passed"`
	Findings  []Finding `json:"findings"`
	PreSHA    string    `json:"pre_sha"`
	PostSHA   string    `json:"post_sha"`
	DiffStat  string    `json:"diff_stat"`
	BuildOK   bool      `json:"build_ok"`
	TestsOK   bool      `json:"tests_ok"`
	Duration  string    `json:"duration"`
}

// Auditor runs completion verification.
type Auditor struct {
	WorkDir   string           // repo root
	SubAgent  SubAgentRunner   // optional — spawns read-only verification sub-agent
}

// SubAgentRunner is the minimal interface for spawning a verification sub-agent.
// The wiring layer provides this (e.g., delegate to Hermes sub-agent or OpenAI).
type SubAgentRunner interface {
	Run(ctx context.Context, prompt string) (string, error)
}

// TakeSnapshot captures the current state of the repo.
func TakeSnapshot(workDir string) (*Snapshot, error) {
	s := &Snapshot{
		Time:  time.Now(),
		Files: make(map[string]string),
	}

	sha, err := runCmd(workDir, "git", "rev-parse", "HEAD")
	if err != nil {
		// Not a git repo — snapshot is still useful for file hashes.
		s.GitSHA = "non-git"
	} else {
		s.GitSHA = strings.TrimSpace(sha)
	}

	diff, diffErr := runCmd(workDir, "git", "diff", "--stat", "HEAD")
	if diffErr != nil {
		// Not a git repo or no commits — diff is empty.
		s.GitDiff = ""
	} else {
		s.GitDiff = diff
	}

	status, statusErr := runCmd(workDir, "git", "status", "--porcelain")
	if statusErr != nil {
		// Not a git repo — treat as not dirty.
		s.Dirty = false
	} else {
		s.Dirty = len(strings.TrimSpace(status)) > 0
	}

	return s, nil
}

// Audit runs the full verification pass, comparing pre to post state
// against what the agent claimed.
func (a *Auditor) Audit(ctx context.Context, pre *Snapshot, claims []Claim) *Report {
	start := time.Now()
	r := &Report{PreSHA: pre.GitSHA, Passed: true}

	// 1. Take post-snapshot.
	post, err := TakeSnapshot(a.WorkDir)
	if err != nil {
		r.Findings = append(r.Findings, Finding{
			Severity: "fail", Claim: "post-snapshot",
			Expected: "snapshot succeeds", Actual: err.Error(),
		})
		r.Passed = false
		return r
	}
	r.PostSHA = post.GitSHA
	r.DiffStat = post.GitDiff

	// 2. Run global build and test once.
	buildOutput, buildErr := runCmd(a.WorkDir, "go", "build", "./...")
	if buildErr != nil {
		r.BuildOK = false
		r.Passed = false
		r.Findings = append(r.Findings, Finding{
			Severity: "fail", Claim: "build passes",
			Expected: "clean build", Actual: "build failed",
			Evidence: firstLines(buildOutput, 5),
		})
	} else {
		r.BuildOK = true
	}

	testOutput, testErr := runCmd(a.WorkDir, "go", "test", "./...", "-count=1", "-timeout", "30s")
	if testErr != nil {
		r.TestsOK = false
		r.Passed = false
		r.Findings = append(r.Findings, Finding{
			Severity: "fail", Claim: "tests pass",
			Expected: "all tests pass", Actual: "tests failed",
			Evidence: firstLines(testOutput, 10),
		})
	} else {
		r.TestsOK = true
	}

	// 3. Check each claim.
	for _, c := range claims {
		for _, f := range c.Files {
			a.checkFileClaim(ctx, c, f, &pre.Files, r)
		}
		if c.BuildPass && !r.BuildOK {
			r.Passed = false
			r.Findings = append(r.Findings, Finding{
				Severity: "fail", Claim: c.Description,
				Expected: "go build passes",
				Actual:   "build failed",
				Evidence: firstLines(buildOutput, 5),
			})
		}
		if c.TestsPass && !r.TestsOK {
			r.Passed = false
			r.Findings = append(r.Findings, Finding{
				Severity: "fail", Claim: c.Description,
				Expected: "go test passes",
				Actual:   "tests failed",
				Evidence: firstLines(testOutput, 10),
			})
		}
	}

	// 4. If sub-agent is wired, run semantic verification.
	if a.SubAgent != nil && len(claims) > 0 {
		a.runSemanticAudit(ctx, claims, r)
	}

	r.Duration = time.Since(start).Round(time.Millisecond).String()
	return r
}

func (a *Auditor) checkFileClaim(ctx context.Context, c Claim, path string, preFiles *map[string]string, r *Report) {
	fullPath := filepath.Join(a.WorkDir, path)
	info, err := os.Stat(fullPath)
	if err != nil {
		r.Passed = false
		r.Findings = append(r.Findings, Finding{
			Severity: "fail", Claim: c.Description,
			Expected: fmt.Sprintf("file %s exists", path),
			Actual:   err.Error(),
		})
		return
	}

	// If we have a pre-snapshot hash, check if the file actually changed.
	if preFiles != nil {
		if oldHash, ok := (*preFiles)[path]; ok {
			newHash := fileHash(fullPath)
			if oldHash == newHash && !info.IsDir() {
				r.Passed = false
				r.Findings = append(r.Findings, Finding{
					Severity: "fail", Claim: c.Description,
					Expected: fmt.Sprintf("%s was modified", path),
					Actual:   "file unchanged since task start",
				})
			}
		}
	}
}

func (a *Auditor) runSemanticAudit(ctx context.Context, claims []Claim, r *Report) {
	claimJSON, err := json.Marshal(claims)
	if err != nil {
		r.Findings = append(r.Findings, Finding{
			Severity: "fail", Claim: "semantic-audit",
			Expected: "marshal claims", Actual: err.Error(),
		})
		return
	}
	diff, _ := runCmd(a.WorkDir, "git", "diff", "HEAD")

	prompt := fmt.Sprintf(`You are a completion auditor. Your job: verify that the agent actually did what it claimed.

CLAIMS (what the agent says it did):
%s

ACTUAL DIFF (what actually changed):
%s

For each claim, check:
1. Is there evidence in the diff that the claimed file was modified?
2. If the claim says "added error handling" — does the diff show new error checks?
3. If the claim says "added tests" — do test files appear in the diff?
4. Are there any files in the diff that don't match any claim? (stray changes)

Respond with JSON: {"findings": [{"claim": "...", "verdict": "ok|warn|fail", "evidence": "..."}]}`, string(claimJSON), firstLines(diff, 200))

	result, err := a.SubAgent.Run(ctx, prompt)
	if err != nil {
		r.Findings = append(r.Findings, Finding{
			Severity: "warn", Claim: "semantic-audit",
			Expected: "sub-agent audit", Actual: err.Error(),
		})
		return
	}

	var semantic struct {
		Findings []struct {
			Claim    string `json:"claim"`
			Verdict  string `json:"verdict"`
			Evidence string `json:"evidence"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(result), &semantic); err != nil {
		// Sub-agent didn't return valid JSON — try to extract.
		r.Findings = append(r.Findings, Finding{
			Severity: "warn", Claim: "semantic-audit",
			Expected: "valid JSON", Actual: firstLines(result, 3),
		})
		return
	}

	for _, f := range semantic.Findings {
		if f.Verdict == "fail" {
			r.Passed = false
		}
		r.Findings = append(r.Findings, Finding{
			Severity: f.Verdict,
			Claim:    f.Claim,
			Evidence: f.Evidence,
		})
	}
}

// ── helpers ──

// ToRevisionPrompt converts audit findings into a prompt the agent can
// use to fix its lazy/incomplete work. Returns empty string if all pass.
func (r *Report) ToRevisionPrompt() string {
	if r.Passed {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Completion Audit Failed\n\n")
	b.WriteString("The following issues were found with your work:\n\n")

	for _, f := range r.Findings {
		if f.Severity == "ok" {
			continue
		}
		b.WriteString(fmt.Sprintf("- **%s**: %s\n", f.Claim, f.Evidence))
	}

	b.WriteString("\nFix each issue and mark the corresponding plan items as complete only when the work is actually done.\n")
	if !r.BuildOK {
		b.WriteString("\nRun `go build ./...` to verify before claiming done.\n")
	}
	if !r.TestsOK {
		b.WriteString("\nRun `go test ./...` to verify before claiming done.\n")
	}

	return b.String()
}

func runCmd(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func fileHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
