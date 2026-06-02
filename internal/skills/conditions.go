// Package skills — context-aware activation conditions (Phase 1.5 #7).
//
// Triggers are still the cheap fast path: substring match on user input.
// Conditions are the contextual gate: same skill, restricted to specific
// projects, languages, time windows, or post-failure recovery moments.
//
// Evaluation is AND across all populated fields. Empty / unset fields
// are wildcards. The whole Conditions struct empty = behave like the
// pre-#7 trigger-only matcher.
package skills

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MatchContext is the per-turn snapshot the Registry uses to evaluate
// Conditions. Callers (the agent's renderSkillSection) build this once
// per turn from agent state + the working directory. Zero-valued fields
// disable the corresponding condition checks for that turn.
type MatchContext struct {
	// Cwd is the current working directory the user is in (or the
	// directory the agent is operating against). Empty = no cwd gate
	// fires.
	Cwd string
	// RepoLanguage is the dominant language detected for Cwd ("go",
	// "python", "typescript", "rust"...). Empty = no language gate
	// fires. Inferred cheaply: presence of go.mod, package.json,
	// requirements.txt, Cargo.toml.
	RepoLanguage string
	// Now is the wall-clock used for TimeWindow comparison. Zero
	// value (time.Time{}) skips the time-window check entirely so
	// tests don't depend on the wall clock.
	Now time.Time
	// PriorOutput is the most recent tool output content the agent
	// saw. Empty = the PriorOutputContains gate is treated as not
	// fired.
	PriorOutput string
}

// MatchesPublic is the exported wrapper around matches. Callers
// outside this package use this when evaluating always-on skills
// without going through the trigger index.
func (c Conditions) MatchesPublic(ctx MatchContext) bool { return c.matches(ctx) }

// matches returns true when every populated condition holds against ctx.
// Empty Conditions struct → always true.
func (c Conditions) matches(ctx MatchContext) bool {
	if c.CwdGlob != "" {
		if ctx.Cwd == "" {
			return false
		}
		ok, err := matchCwdGlob(c.CwdGlob, ctx.Cwd)
		if err != nil || !ok {
			return false
		}
	}
	if len(c.FilePresent) > 0 {
		if ctx.Cwd == "" {
			return false
		}
		if !anyFilePresent(ctx.Cwd, c.FilePresent) {
			return false
		}
	}
	if c.RepoLanguage != "" {
		if !strings.EqualFold(c.RepoLanguage, ctx.RepoLanguage) {
			return false
		}
	}
	if c.TimeWindow != "" && !ctx.Now.IsZero() {
		ok, err := timeInWindow(c.TimeWindow, ctx.Now)
		if err != nil || !ok {
			return false
		}
	}
	if c.PriorOutputContains != "" {
		if !strings.Contains(strings.ToLower(ctx.PriorOutput),
			strings.ToLower(c.PriorOutputContains)) {
			return false
		}
	}
	return true
}

// matchCwdGlob implements doublestar-style matching where `**` spans any
// number of path segments. Falls back to filepath.Match's per-segment
// semantics for non-`**` patterns. Cwd is normalised to use `/` so the
// matcher behaves the same on Linux and Windows.
func matchCwdGlob(pattern, cwd string) (bool, error) {
	cwd = filepath.ToSlash(cwd)
	if !strings.Contains(pattern, "**") {
		return filepath.Match(pattern, cwd)
	}
	// Replace `**` with a regex-like greedy any-segment match. We do
	// this without compiling a regex: split on `**`, anchor each
	// non-empty piece with filepath.Match, and allow any path-segment
	// fill between them.
	parts := strings.Split(pattern, "**")
	// Trim slashes that flank `**` so empty fills are accepted.
	for i := range parts {
		parts[i] = strings.Trim(parts[i], "/")
	}
	pos := 0
	first := true
	for _, p := range parts {
		if p == "" {
			continue
		}
		// Find the earliest occurrence of `p` (as a per-segment glob)
		// in cwd[pos:].
		found := -1
		for scan := pos; scan <= len(cwd); scan++ {
			rest := cwd[scan:]
			// Try to match the prefix `p` against an initial slice
			// of `rest` whose length equals the number of segments
			// `p` declares. Easier: match `p` against the path up
			// to and including the matched segments.
			pSegments := strings.Count(p, "/") + 1
			restSegments := strings.Split(rest, "/")
			if len(restSegments) < pSegments {
				break
			}
			candidate := strings.Join(restSegments[:pSegments], "/")
			ok, _ := filepath.Match(p, candidate)
			if ok {
				found = scan + len(candidate)
				if first && scan > 0 {
					// First piece must anchor at start unless the
					// pattern began with `**`.
					if strings.HasPrefix(pattern, "**") {
						break
					}
					found = -1
				}
				break
			}
			if !strings.Contains(rest, "/") {
				break
			}
		}
		if found < 0 {
			return false, nil
		}
		pos = found
		first = false
	}
	// If the pattern ends without `**`, the last piece must end at the
	// end of cwd.
	if !strings.HasSuffix(pattern, "**") {
		return pos == len(cwd), nil
	}
	return true, nil
}

// anyFilePresent returns true when any glob in patterns matches at
// least one file directly under cwd. Globs are evaluated relative to
// cwd; absolute paths or `..` traversals are rejected.
func anyFilePresent(cwd string, patterns []string) bool {
	for _, p := range patterns {
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
			continue
		}
		full := filepath.Join(cwd, p)
		matches, err := filepath.Glob(full)
		if err == nil && len(matches) > 0 {
			return true
		}
	}
	return false
}

// timeInWindow parses "HH:MM-HH:MM" (24-hour) and reports whether t
// falls within. Windows that cross midnight (`22:00-06:00`) are
// supported: the second time is interpreted as the END of the window
// on the next day.
func timeInWindow(spec string, t time.Time) (bool, error) {
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return false, errBadWindow
	}
	start, err := parseHHMM(strings.TrimSpace(parts[0]))
	if err != nil {
		return false, err
	}
	end, err := parseHHMM(strings.TrimSpace(parts[1]))
	if err != nil {
		return false, err
	}
	cur := t.Hour()*60 + t.Minute()
	if start <= end {
		return cur >= start && cur <= end, nil
	}
	// Window crosses midnight.
	return cur >= start || cur <= end, nil
}

func parseHHMM(s string) (int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, errBadWindow
	}
	h, err := atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, errBadWindow
	}
	m, err := atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, errBadWindow
	}
	return h*60 + m, nil
}

func atoi(s string) (int, error) {
	if s == "" {
		return 0, errBadWindow
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadWindow
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// errBadWindow is returned by timeInWindow when the spec doesn't parse.
// Wrapped in a fmt.Errorf at the call site would be nicer, but we
// intentionally avoid pulling fmt into this hot path.
var errBadWindow = &windowErr{msg: "skills: invalid time window (expected HH:MM-HH:MM)"}

type windowErr struct{ msg string }

func (e *windowErr) Error() string { return e.msg }

// DetectRepoLanguage cheaply infers the dominant language of a
// directory by looking for canonical marker files. Returns "" when
// no marker is recognised. Helper for callers building MatchContext.
func DetectRepoLanguage(cwd string) string {
	if cwd == "" {
		return ""
	}
	checks := []struct {
		file string
		lang string
	}{
		{"go.mod", "go"},
		{"Cargo.toml", "rust"},
		{"package.json", "typescript"}, // tsconfig overrides below
		{"tsconfig.json", "typescript"},
		{"requirements.txt", "python"},
		{"pyproject.toml", "python"},
		{"Gemfile", "ruby"},
		{"composer.json", "php"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"build.gradle.kts", "kotlin"},
	}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(cwd, c.file)); err == nil {
			return c.lang
		}
	}
	return ""
}
