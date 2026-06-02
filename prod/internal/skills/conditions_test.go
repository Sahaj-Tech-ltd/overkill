package skills

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConditions_EmptyMatchesAnything(t *testing.T) {
	c := Conditions{}
	if !c.matches(MatchContext{}) {
		t.Error("empty conditions should match empty context")
	}
	if !c.matches(MatchContext{Cwd: "/anywhere", RepoLanguage: "rust"}) {
		t.Error("empty conditions should match populated context")
	}
}

func TestConditions_CwdGlob(t *testing.T) {
	cases := []struct {
		pattern string
		cwd     string
		want    bool
	}{
		{"**/auth/**", "/home/u/repo/auth/handlers", true},
		{"**/auth/**", "/home/u/repo/api/handlers", false},
		{"**/api", "/home/u/repo/api", true},
		{"**/api", "/home/u/repo/api/v1", false},
		{"/exact/path", "/exact/path", true},
		{"/exact/path", "/exact/path/sub", false},
		{"**", "/anything/at/all", true},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"_"+tc.cwd, func(t *testing.T) {
			c := Conditions{CwdGlob: tc.pattern}
			got := c.matches(MatchContext{Cwd: tc.cwd})
			if got != tc.want {
				t.Errorf("Conditions{CwdGlob:%q}.matches(%q) = %v, want %v",
					tc.pattern, tc.cwd, got, tc.want)
			}
		})
	}
}

func TestConditions_CwdGlobRequiresCwd(t *testing.T) {
	c := Conditions{CwdGlob: "**/anywhere"}
	if c.matches(MatchContext{}) {
		t.Error("cwd-globbed condition should fail without ctx.Cwd")
	}
}

func TestConditions_FilePresent(t *testing.T) {
	dir := t.TempDir()
	// Create go.mod and a .tsx file.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "App.tsx"), []byte("//"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		files []string
		want  bool
	}{
		{[]string{"go.mod"}, true},
		{[]string{"*.tsx"}, true},
		{[]string{"missing.toml"}, false},
		{[]string{"missing.toml", "go.mod"}, true}, // any-of
		{[]string{}, true},                         // empty = wildcard
		{[]string{"/etc/passwd"}, false},           // absolute rejected
		{[]string{"../escape"}, false},             // traversal rejected
	}
	for _, tc := range cases {
		c := Conditions{FilePresent: tc.files}
		got := c.matches(MatchContext{Cwd: dir})
		if got != tc.want {
			t.Errorf("FilePresent=%v matches(cwd=%s) = %v, want %v",
				tc.files, dir, got, tc.want)
		}
	}
}

func TestConditions_RepoLanguage(t *testing.T) {
	c := Conditions{RepoLanguage: "go"}
	if !c.matches(MatchContext{RepoLanguage: "go"}) {
		t.Error("exact match should succeed")
	}
	if !c.matches(MatchContext{RepoLanguage: "GO"}) {
		t.Error("language match is case-insensitive")
	}
	if c.matches(MatchContext{RepoLanguage: "python"}) {
		t.Error("language mismatch should fail")
	}
	if c.matches(MatchContext{}) {
		t.Error("language gate should fail when ctx.RepoLanguage missing")
	}
}

func TestConditions_TimeWindow(t *testing.T) {
	cases := []struct {
		spec   string
		hour   int
		minute int
		want   bool
	}{
		{"09:00-17:00", 10, 0, true},
		{"09:00-17:00", 18, 0, false},
		{"09:00-17:00", 9, 0, true},  // inclusive start
		{"09:00-17:00", 17, 0, true}, // inclusive end
		// Window that crosses midnight.
		{"22:00-06:00", 23, 30, true},
		{"22:00-06:00", 4, 0, true},
		{"22:00-06:00", 12, 0, false},
	}
	for _, tc := range cases {
		now := time.Date(2026, 5, 13, tc.hour, tc.minute, 0, 0, time.UTC)
		c := Conditions{TimeWindow: tc.spec}
		got := c.matches(MatchContext{Now: now})
		if got != tc.want {
			t.Errorf("TimeWindow=%q at %02d:%02d = %v, want %v",
				tc.spec, tc.hour, tc.minute, got, tc.want)
		}
	}
}

func TestConditions_TimeWindowZeroNowSkips(t *testing.T) {
	// Zero Now should skip the time check entirely.
	c := Conditions{TimeWindow: "09:00-17:00"}
	if !c.matches(MatchContext{Now: time.Time{}}) {
		t.Error("zero Now should skip time check (wildcard)")
	}
}

func TestConditions_PriorOutputContains(t *testing.T) {
	c := Conditions{PriorOutputContains: "ECONNREFUSED"}
	if !c.matches(MatchContext{PriorOutput: "Error: ECONNREFUSED on port 8080"}) {
		t.Error("substring should match")
	}
	if !c.matches(MatchContext{PriorOutput: "econnrefused"}) {
		t.Error("substring match should be case-insensitive")
	}
	if c.matches(MatchContext{PriorOutput: "Connection reset"}) {
		t.Error("non-matching output should fail")
	}
	if c.matches(MatchContext{}) {
		t.Error("missing PriorOutput should fail when condition set")
	}
}

func TestConditions_AllGatesMustHold(t *testing.T) {
	c := Conditions{
		CwdGlob:      "**/api/**",
		RepoLanguage: "go",
	}
	good := MatchContext{Cwd: "/home/u/repo/api/v1", RepoLanguage: "go"}
	if !c.matches(good) {
		t.Error("both gates satisfied should match")
	}
	badLang := MatchContext{Cwd: "/home/u/repo/api/v1", RepoLanguage: "rust"}
	if c.matches(badLang) {
		t.Error("language mismatch should fail even when cwd matches")
	}
	badCwd := MatchContext{Cwd: "/home/u/repo/web", RepoLanguage: "go"}
	if c.matches(badCwd) {
		t.Error("cwd mismatch should fail even when language matches")
	}
}

func TestDetectRepoLanguage(t *testing.T) {
	cases := []struct {
		marker string
		want   string
	}{
		{"go.mod", "go"},
		{"Cargo.toml", "rust"},
		{"pyproject.toml", "python"},
		{"package.json", "typescript"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, tc.marker), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := DetectRepoLanguage(dir)
		if got != tc.want {
			t.Errorf("DetectRepoLanguage(%s present) = %q, want %q", tc.marker, got, tc.want)
		}
	}
	if got := DetectRepoLanguage(""); got != "" {
		t.Errorf("empty cwd should return empty, got %q", got)
	}
	if got := DetectRepoLanguage(t.TempDir()); got != "" {
		t.Errorf("empty dir with no markers should return empty, got %q", got)
	}
}
