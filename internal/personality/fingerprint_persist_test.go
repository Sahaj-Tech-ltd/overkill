package personality

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprint_PersistRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fingerprint.json")

	ft := NewFingerprintTracker()
	fp := ft.Detect("claude-opus-4-20260101")
	ft.Update(fp)
	if err := ft.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	loaded := NewFingerprintTracker()
	if err := loaded.LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	// Load installs into CURRENT; subsequent Update will shift to previous.
	if loaded.current == nil {
		t.Fatal("expected current to be populated after load")
	}
	if loaded.current.Family != fp.Family {
		t.Errorf("family roundtrip: got %s, want %s", loaded.current.Family, fp.Family)
	}
}

func TestFingerprint_LoadMissingFileIsOK(t *testing.T) {
	ft := NewFingerprintTracker()
	if err := ft.LoadFromFile("/nonexistent/path/x.json"); err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
}

func TestFingerprint_BootCheck_NoPreviousIsNoChange(t *testing.T) {
	dir := t.TempDir()
	ft := NewFingerprintTracker()
	notice, err := ft.BootCheck(filepath.Join(dir, "x.json"), "claude-opus-4")
	if err != nil {
		t.Fatal(err)
	}
	if notice != "" {
		t.Errorf("no previous record should yield no notice, got %q", notice)
	}
}

func TestFingerprint_BootCheck_FamilyChangeProducesNotice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")

	// Seed previous as opus.
	first := NewFingerprintTracker()
	first.Update(first.Detect("claude-opus-4"))
	if err := first.SaveToFile(path); err != nil {
		t.Fatal(err)
	}

	// Boot with sonnet — different family.
	ft := NewFingerprintTracker()
	notice, err := ft.BootCheck(path, "claude-sonnet-4")
	if err != nil {
		t.Fatal(err)
	}
	if notice == "" {
		t.Error("expected calibration notice on family change")
	}
}

func TestFingerprint_BootCheck_SameFamilyNoNotice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")

	first := NewFingerprintTracker()
	first.Update(first.Detect("claude-opus-4"))
	_ = first.SaveToFile(path)

	ft := NewFingerprintTracker()
	notice, err := ft.BootCheck(path, "claude-opus-5") // same family, new version
	if err != nil {
		t.Fatal(err)
	}
	if notice != "" {
		t.Errorf("same family should not produce notice, got %q", notice)
	}
}

func TestFingerprint_SaveAtomicCleanupOnRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	ft := NewFingerprintTracker()
	ft.Update(ft.Detect("gpt-4o"))
	if err := ft.SaveToFile(path); err != nil {
		t.Fatal(err)
	}
	// .tmp sibling should not linger.
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error(".tmp file should be removed after rename")
	}
}
