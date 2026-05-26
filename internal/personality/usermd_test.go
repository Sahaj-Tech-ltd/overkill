package personality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedUserMD_WritesProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.md")
	p := &ColdStartProfile{
		UserName:            "Harsh",
		Timezone:            "EST",
		CommunicationStyle:  "direct",
		VerbosityPreference: "terse",
		TechnicalDepth:      "high",
		ToneTolerance:       "casual",
		UrgencyBaseline:     "high",
	}
	wrote, err := SeedUserMD(path, p)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Error("expected wrote=true for fresh file")
	}
	data, _ := os.ReadFile(path)
	body := string(data)
	for _, want := range []string{"# User", "Harsh", "EST", "direct", "terse", "high"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

func TestSeedUserMD_DoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.md")
	if err := os.WriteFile(path, []byte("USER EDITED CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err := SeedUserMD(path, &ColdStartProfile{UserName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("should not overwrite existing file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "USER EDITED CONTENT" {
		t.Errorf("file content modified: %s", string(data))
	}
}

func TestSeedUserMD_NilProfileNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.md")
	wrote, err := SeedUserMD(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("nil profile should not write")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should not exist: %v", err)
	}
}

func TestSeedUserMD_UnknownFieldsRendered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.md")
	_, err := SeedUserMD(path, &ColdStartProfile{UserName: "Harsh"}) // most fields empty
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "unknown") {
		t.Errorf("empty fields should render as unknown: %s", string(data))
	}
}
