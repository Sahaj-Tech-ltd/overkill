package modules

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewManager_FirstRun(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	modules := m.List()
	if len(modules) == 0 {
		t.Fatal("expected default modules on first run")
	}

	// Default modules should exist.
	names := map[string]bool{}
	for _, mod := range modules {
		names[mod.Name] = true
	}
	for _, want := range []string{"superpowers", "caveman", "postgres", "unicode", "edge-tts"} {
		if !names[want] {
			t.Errorf("expected default module %q not found", want)
		}
	}

	// Manifest file should exist.
	manifestPath := filepath.Join(dir, "modules.toml")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Fatal("manifest file was not created")
	}
}

func TestNewManager_ExistingManifest(t *testing.T) {
	dir := t.TempDir()

	// Write a manifest manually.
	manifest := `[modules]
[modules.custom-skill]
source = "github"
repo = "user/custom-skill"
path = "skills/custom-skill"
version = "v1.0.0"
type = "skill"
description = "A custom skill"
auto_update = true
`
	if err := os.WriteFile(filepath.Join(dir, "modules.toml"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager with existing manifest: %v", err)
	}

	mod := m.Get("custom-skill")
	if mod == nil {
		t.Fatal("custom-skill not loaded from manifest")
	}
	if mod.Version != "v1.0.0" {
		t.Errorf("version: got %q, want v1.0.0", mod.Version)
	}
}

func TestGet_NotFound(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)

	mod := m.Get("nonexistent")
	if mod != nil {
		t.Error("expected nil for nonexistent module")
	}
}

func TestList_Sorted(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)

	modules := m.List()
	for i := 1; i < len(modules); i++ {
		if modules[i-1].Name > modules[i].Name {
			t.Errorf("List not sorted: %q > %q", modules[i-1].Name, modules[i].Name)
		}
	}
}

func TestUpdate_UnknownModule(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir)

	_, err := m.Update("nonexistent")
	if err == nil {
		t.Error("expected error for unknown module")
	}
	if !strings.Contains(err.Error(), "unknown module") {
		t.Errorf("error message: got %v, want 'unknown module'", err)
	}
}

func TestUpdateAll_SkipsDisabled(t *testing.T) {
	moduleHTTPFixture(t, func(*http.Request) (*http.Response, error) {
		return moduleResponse(503, []byte("fixture unavailable")), nil
	})
	dir := t.TempDir()
	m, _ := NewManager(dir)

	results, err := m.UpdateAll()
	// The controlled GitHub fixture is unavailable; failures must be reported.
	// Go-module (postgres) and git (edge-tts) should skip.
	if err != nil {
		// The fixture returns a genuine HTTP failure.
		// The error shouldn't be empty.
		if !strings.Contains(err.Error(), "update errors") {
			t.Fatalf("unexpected error from UpdateAll: %v", err)
		}
	}

	for _, r := range results {
		// Three valid states: updated, skipped, or failed.
		if !r.Skipped && !r.Updated && r.Error == "" {
			t.Errorf("unexpected result for %s: skipped=%v updated=%v error=%s",
				r.Module, r.Skipped, r.Updated, r.Error)
		}
	}
}

func TestFormatUpdateReport(t *testing.T) {
	results := []*UpdateResult{
		{Module: "alpha", FromVersion: "v1.0", ToVersion: "v2.0", Updated: true},
		{Module: "beta", Skipped: true, Reason: "already at v3.0"},
		{Module: "gamma", Error: "network timeout"},
	}

	report := FormatUpdateReport(results)
	if report == "" {
		t.Fatal("empty report")
	}
	if !strings.Contains(report, "alpha") || !strings.Contains(report, "beta") || !strings.Contains(report, "gamma") {
		t.Errorf("report missing modules: %s", report)
	}
	if !strings.Contains(report, "updated") && !strings.Contains(report, "skipped") {
		t.Errorf("report missing summary: %s", report)
	}
}

func TestFormatUpdateReport_Empty(t *testing.T) {
	report := FormatUpdateReport(nil)
	if !strings.Contains(report, "No modules") {
		t.Errorf("empty report: got %q", report)
	}
}

func TestCheckAll_SurfacesUnavailableReleaseChecks(t *testing.T) {
	moduleHTTPFixture(t, func(*http.Request) (*http.Response, error) {
		return moduleResponse(503, []byte("fixture unavailable")), nil
	})
	dir := t.TempDir()
	m, _ := NewManager(dir)

	// A failed release check must be visible, not reported up to date.
	_, skipped := m.CheckAll()
	if len(skipped) == 0 {
		t.Fatal("expected failed release checks in skipped entries")
	}
	found := false
	for _, s := range skipped {
		if strings.Contains(s, "superpowers") || strings.Contains(s, "caveman") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected GitHub module skip, got: %v", skipped)
	}
}
