package personality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSoulTemplate_ContainsSourceOfTruth(t *testing.T) {
	tmpl := defaultSoulTemplate("TestAgent")
	if !strings.Contains(tmpl, "user's words are the spec") {
		t.Error("default soul template must contain the source-of-truth section phrase \"user's words are the spec\"")
	}
}

func TestDefaultSoulTemplate_ContainsAgentName(t *testing.T) {
	tmpl := defaultSoulTemplate("Halley")
	if !strings.Contains(tmpl, "Halley") {
		t.Error("default soul template must include the agent name")
	}
}

func TestCreateDefaultSoul_WritesSourceOfTruth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "soul.md")

	if err := CreateDefaultSoul(path, "Aria"); err != nil {
		t.Fatalf("CreateDefaultSoul: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "user's words are the spec") {
		t.Error("written soul file must contain source-of-truth section")
	}
}

func TestDefaultSoulTemplate_SearchFirstCorrectNeverPresent(t *testing.T) {
	tmpl := defaultSoulTemplate("Any")
	if !strings.Contains(tmpl, "Search first, correct never") {
		t.Error("default soul template must contain the 'Search first, correct never' directive")
	}
}
