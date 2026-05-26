// Package speculative — prefetch heuristics that decide what to
// warm in the cache when the agent reads a file.
//
// All heuristics are read-only filesystem scans + name shaping.
// They never block (caller invokes them async via the Prefetcher).
// The goal is "good guess" not "perfect" — a wrong prefetch costs
// one os.ReadFile and gets evicted later; missing a right one
// costs the agent a synchronous read it could have skipped.

package speculative

import (
	"os"
	"path/filepath"
	"strings"
)

// HeuristicFn returns the list of paths to prefetch when the agent
// just read `path`. Return order doesn't matter — the prefetcher
// processes them via its worker pool.
type HeuristicFn func(path string) []string

// CombineHeuristics chains multiple heuristics; dedupes by path so
// a sibling that's also a test-pair doesn't get queued twice.
func CombineHeuristics(fns ...HeuristicFn) HeuristicFn {
	return func(path string) []string {
		seen := map[string]bool{path: true}
		var out []string
		for _, fn := range fns {
			for _, p := range fn(path) {
				if seen[p] {
					continue
				}
				seen[p] = true
				out = append(out, p)
			}
		}
		return out
	}
}

// TestPairHeuristic suggests the test file for a code file (and
// vice versa) based on common naming conventions across Go, Python,
// TypeScript, Rust. Only suggests paths that exist on disk.
func TestPairHeuristic(path string) []string {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	candidates := []string{}
	switch ext {
	case ".go":
		if strings.HasSuffix(stem, "_test") {
			// xxx_test.go → xxx.go
			candidates = append(candidates, filepath.Join(dir, strings.TrimSuffix(stem, "_test")+".go"))
		} else {
			candidates = append(candidates, filepath.Join(dir, stem+"_test.go"))
		}
	case ".py":
		if strings.HasSuffix(stem, "_test") {
			candidates = append(candidates, filepath.Join(dir, strings.TrimSuffix(stem, "_test")+".py"))
		} else if strings.HasPrefix(stem, "test_") {
			candidates = append(candidates, filepath.Join(dir, strings.TrimPrefix(stem, "test_")+".py"))
		} else {
			candidates = append(candidates, filepath.Join(dir, "test_"+stem+".py"))
			candidates = append(candidates, filepath.Join(dir, stem+"_test.py"))
		}
	case ".ts", ".tsx", ".js", ".jsx":
		// foo.test.ts ↔ foo.ts
		if strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") {
			plain := strings.TrimSuffix(strings.TrimSuffix(stem, ".test"), ".spec")
			candidates = append(candidates, filepath.Join(dir, plain+ext))
		} else {
			candidates = append(candidates, filepath.Join(dir, stem+".test"+ext))
			candidates = append(candidates, filepath.Join(dir, stem+".spec"+ext))
		}
	case ".rs":
		// In Rust, tests live in the same file via #[cfg(test)] —
		// the heuristic is "look for a tests/ sibling".
		candidates = append(candidates, filepath.Join(dir, "..", "tests", stem+".rs"))
	}
	return filterExisting(candidates)
}

// PackageNeighborHeuristic suggests other files in the same dir
// with the same extension — typical "this module has 4 files,
// agent will probably read at least one more" pattern. Capped at
// the first 5 alphabetical neighbors to avoid prefetching huge
// directories.
func PackageNeighborHeuristic(path string) []string {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	if ext == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var siblings []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if full == path {
			continue
		}
		if filepath.Ext(e.Name()) != ext {
			continue
		}
		siblings = append(siblings, full)
		if len(siblings) >= 5 {
			break
		}
	}
	return siblings
}

// DocHeuristic suggests README / CHANGELOG / doc files in the same
// directory tree when the agent reads a code file. Useful for
// "agent's about to make a change; remind it of the docs".
func DocHeuristic(path string) []string {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	docs := []string{
		filepath.Join(dir, "README.md"),
		filepath.Join(dir, "CHANGELOG.md"),
		filepath.Join(dir, "doc.go"), // Go convention
	}
	return filterExisting(docs)
}

// filterExisting keeps only paths that resolve to an actual file.
// Prevents queueing prefetches for hypothetical-but-nonexistent
// targets like the test-pair of a file that has no tests yet.
func filterExisting(paths []string) []string {
	out := paths[:0]
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}
