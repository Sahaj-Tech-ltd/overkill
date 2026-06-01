package skills

import (
	"time"
)

type Skill struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Description  string            `json:"description"`
	Author       string            `json:"author"`
	Category     string            `json:"category"`
	Tags         []string          `json:"tags"`
	Triggers     []string          `json:"triggers"`
	Instructions string            `json:"instructions"`
	FilePath     string            `json:"file_path"`
	Bundled      bool              `json:"bundled"`
	Enabled      bool              `json:"enabled"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Metadata     map[string]string `json:"metadata"`
	// Conditions adds context-aware activation gates beyond simple
	// substring Triggers. All declared conditions must hold (AND) for
	// the skill to be considered active. Missing/zero fields are
	// treated as "no constraint" — skills with no Conditions behave
	// identically to before (Triggers-only matching). Phase 1.5 #7.
	Conditions Conditions `json:"conditions,omitempty" yaml:"conditions,omitempty"`
}

// Conditions are optional activation gates evaluated alongside Triggers.
// Each populated field adds an AND-constraint; an empty field is a wildcard.
// See Registry.MatchWithContext for evaluation semantics.
type Conditions struct {
	// CwdGlob restricts the skill to working directories matching this
	// glob (`**/auth/**`, `*/api`, etc.). Uses doublestar-style globs:
	// `**` matches any number of path segments, `*` matches one segment.
	// Empty = wildcard. Matched against MatchContext.Cwd.
	CwdGlob string `json:"cwd_glob,omitempty" yaml:"cwd_glob,omitempty"`
	// FilePresent activates the skill only when at least one of the
	// listed files (relative to cwd) exists. Use globs like `*.tsx` or
	// concrete names like `go.mod`. Empty slice = wildcard.
	FilePresent []string `json:"file_present,omitempty" yaml:"file_present,omitempty"`
	// RepoLanguage matches the inferred primary language of the cwd
	// (`go`, `python`, `typescript`, `rust`...). Compared
	// case-insensitively against MatchContext.RepoLanguage. Empty =
	// wildcard.
	RepoLanguage string `json:"repo_language,omitempty" yaml:"repo_language,omitempty"`
	// TimeWindow is a "HH:MM-HH:MM" local-clock range. A window that
	// crosses midnight (e.g. "22:00-06:00") is supported. Empty =
	// wildcard. Compared against MatchContext.Now (caller's clock).
	TimeWindow string `json:"time_window,omitempty" yaml:"time_window,omitempty"`
	// PriorOutputContains matches when the most recent tool output (as
	// passed via MatchContext.PriorOutput) contains this substring,
	// case-insensitive. Useful for "fire when last test run failed" or
	// "fire when last command mentioned ECONNREFUSED". Empty = wildcard.
	PriorOutputContains string `json:"prior_output_contains,omitempty" yaml:"prior_output_contains,omitempty"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
