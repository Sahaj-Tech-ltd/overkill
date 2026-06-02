// Package verify — post-write verifiers (Batch G2 hallucination
// catching).
//
// After every successful Write/Edit/Patch the agent runs the affected
// files through the matching verifier. A failure becomes a new tool
// message the model sees on its NEXT turn — "you wrote code that
// doesn't compile, here's the error" — closing the loop on hallucinated
// function calls / syntax errors / corrupt config files inside the
// same turn.
//
// Design choices:
//
//   - Verifiers are per-file-extension. Picking the right verifier is
//     a hash lookup; we don't sniff content because file extensions
//     are reliable enough in agent workflows.
//   - Every verifier declares its own timeout. A slow build mustn't
//     stall the agent's loop. Timeout = "skipped" (NOT failure) —
//     "we couldn't check" is not the same as "we found a problem".
//   - Verifiers run sequentially per turn. A typical Edit touches
//     1–3 files; parallelizing would complicate timeout accounting
//     for negligible win.
//   - The dispatcher returns a SINGLE consolidated message rather
//     than firing one per file — fewer tokens per turn.
package verify

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Verifier checks a single file. Returns ok=true when the content is
// well-formed for this file type; ok=false when something is wrong.
// Detail is the human-readable error (build stderr, parse error) the
// model reads on its next turn.
//
// Verifiers run with a context bounded by Timeout(). ctx.Done firing
// during execution should bail and return (false, "<timeout>", true)
// — the third return value `skipped=true` signals "treat as pass".
// We don't fail a turn because the user's machine is slow.
type Verifier interface {
	Name() string
	Timeout() time.Duration
	// Verify the file at absPath. Most verifiers read from disk
	// because tools like `go build` need to see the package as a
	// whole, not just the modified bytes. content (the in-memory
	// proposed bytes) is provided for verifiers that prefer to
	// validate-before-write semantics; most ignore it.
	Verify(ctx context.Context, absPath string, content []byte) (ok bool, detail string, skipped bool)
}

// Registry maps file extensions (lowercase, leading dot) to a
// verifier. Lookups are case-insensitive. Last write wins so plugins
// can override built-ins.
type Registry struct {
	mu   sync.RWMutex
	exts map[string]Verifier
}

// NewRegistry returns an empty registry. Callers wire built-ins via
// Register or via DefaultRegistry().
func NewRegistry() *Registry {
	return &Registry{exts: map[string]Verifier{}}
}

// Register associates a file extension with a verifier. Pass the
// leading dot (".go", ".toml"). An empty key registers the
// "fallback" verifier used when no extension matches.
func (r *Registry) Register(ext string, v Verifier) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exts[strings.ToLower(ext)] = v
}

// Lookup returns the verifier for path, or nil when none matches.
// Resolves via filepath.Ext; multi-dot names like foo.tar.gz key on
// the LAST component (.gz). Agent workflows don't write binary
// archives so this isn't a real limitation.
func (r *Registry) Lookup(path string) Verifier {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ext := strings.ToLower(filepath.Ext(path))
	if v, ok := r.exts[ext]; ok {
		return v
	}
	return r.exts[""]
}

// VerifyResult is one file's outcome. Skipped tracks "couldn't run"
// distinctly from "ran and passed" so a summary can report
// "3 ok, 1 skipped (no verifier)".
type VerifyResult struct {
	Path     string
	Ok       bool
	Detail   string
	Skipped  bool
	Verifier string
}

// VerifyPaths runs the matching verifier for each path. Returns one
// result per path (in input order). nil registry => empty results.
func VerifyPaths(ctx context.Context, reg *Registry, paths []string) []VerifyResult {
	if reg == nil || len(paths) == 0 {
		return nil
	}
	out := make([]VerifyResult, 0, len(paths))
	for _, p := range paths {
		v := reg.Lookup(p)
		if v == nil {
			out = append(out, VerifyResult{Path: p, Ok: true, Skipped: true, Verifier: "none"})
			continue
		}
		vctx, cancel := context.WithTimeout(ctx, v.Timeout())
		ok, detail, skipped := v.Verify(vctx, p, nil)
		cancel()
		out = append(out, VerifyResult{
			Path:     p,
			Ok:       ok,
			Detail:   detail,
			Skipped:  skipped,
			Verifier: v.Name(),
		})
	}
	return out
}

// FormatToolMessage condenses verifier results into a single string
// the agent emits as a "tool"-role message on the next turn. Empty
// return ("") means "nothing to surface" — every file passed or
// was skipped. The dispatcher decides whether to emit.
//
// Passing files are intentionally omitted from the output. The
// model doesn't need a "✓ everything fine" wall; it needs to know
// what broke.
func FormatToolMessage(results []VerifyResult) string {
	var failures []VerifyResult
	for _, r := range results {
		if !r.Ok && !r.Skipped {
			failures = append(failures, r)
		}
	}
	if len(failures) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[verify] post-write check found ")
	if len(failures) == 1 {
		b.WriteString("1 problem:\n\n")
	} else {
		fmt.Fprintf(&b, "%d problems:\n\n", len(failures))
	}
	for i, f := range failures {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "  %s (%s)\n", f.Path, f.Verifier)
		// Indent detail so multi-line build output stays readable
		// when concatenated.
		for _, line := range strings.Split(strings.TrimRight(f.Detail, "\n"), "\n") {
			b.WriteString("    ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nFix before the next step, or explain why the failure is expected.")
	return b.String()
}
