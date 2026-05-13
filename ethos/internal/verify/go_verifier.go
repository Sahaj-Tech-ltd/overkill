// Package verify — Go build verifier.
//
// Runs `go build ./...` rooted at the modified file's package. Catches
// the failure modes that matter:
//   - hallucinated function names ("undefined: foo")
//   - hallucinated method signatures ("too many arguments")
//   - imports of packages that don't exist
//   - syntax errors from sloppy edits
//
// We deliberately don't run tests here — that's Batch G2's follow-on
// (test-on-write), too slow for the inline-after-Edit case.
package verify

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GoVerifier runs `go build` on the package containing the modified
// file. Build runs from the package directory so module resolution
// uses the right go.mod.
type GoVerifier struct {
	// MaxTime caps build duration. Default 20s — enough for most
	// packages, short enough that an agent loop doesn't visibly
	// stall. Builds that exceed return skipped=true (NOT failure).
	MaxTime time.Duration
}

// NewGoVerifier returns a GoVerifier with sane defaults.
func NewGoVerifier() *GoVerifier {
	return &GoVerifier{MaxTime: 20 * time.Second}
}

func (g *GoVerifier) Name() string { return "go build" }

func (g *GoVerifier) Timeout() time.Duration {
	if g.MaxTime <= 0 {
		return 20 * time.Second
	}
	return g.MaxTime
}

// Verify runs `go build .` in the file's directory. ok=true when the
// build succeeds; detail captures stderr on failure.
//
// content is ignored — `go build` works against the on-disk file,
// not in-memory bytes. The agent's Edit/Write tool must have flushed
// before calling us.
func (g *GoVerifier) Verify(ctx context.Context, absPath string, content []byte) (bool, string, bool) {
	dir := filepath.Dir(absPath)
	cmd := exec.CommandContext(ctx, "go", "build", ".")
	cmd.Dir = dir
	// Capture stderr+stdout combined — `go build` errors land on
	// stderr but some go versions split warnings to stdout.
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false, fmt.Sprintf("go build timed out after %s", g.Timeout()), true
	}
	if err != nil {
		// Surface the build output verbatim — it's the model's most
		// useful signal. Cap at 2KB so a giant cascading error doesn't
		// blow out the next turn's context budget.
		detail := strings.TrimSpace(string(out))
		if len(detail) > 2000 {
			detail = detail[:2000] + "\n... (truncated)"
		}
		if detail == "" {
			detail = err.Error()
		}
		return false, detail, false
	}
	return true, "", false
}
