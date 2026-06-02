package skills

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestLoader_LoadsBundledSkills verifies the six plan-mandated bundled skills
// (red-team, code-review, humanizer, understand-anything, frontend-design,
// mutation-test) all load from the repo's skills/ directory through the
// subdirectory-recursing LoadDir path.
func TestLoader_LoadsBundledSkills(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..")
	bundledDir := filepath.Join(repoRoot, "skills")

	loader := NewLoader(bundledDir, "")
	all, err := loader.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}

	want := map[string]bool{
		"red-team":            false,
		"code-review":         false,
		"humanizer":           false,
		"understand-anything": false,
		"frontend-design":     false,
		"mutation-test":       false,
	}
	for _, s := range all {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
	}
	missing := []string{}
	for name, found := range want {
		if !found {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing bundled skills: %v (loaded %d total)", missing, len(all))
	}
	if len(all) < 6 {
		t.Fatalf("expected at least 6 bundled skills, got %d", len(all))
	}
}
