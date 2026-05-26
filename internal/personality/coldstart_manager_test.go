package personality

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestColdStartManager_NewDirIsColdStart(t *testing.T) {
	dir := t.TempDir()
	m := NewColdStartManager(dir)
	if !m.IsColdStart() {
		t.Error("empty memories dir should be cold start")
	}
}

func TestColdStartManager_ProcessFirstResponsePersists(t *testing.T) {
	dir := t.TempDir()
	m := NewColdStartManager(dir)
	if !m.IsColdStart() {
		t.Fatal("expected cold start before answer")
	}
	profile, err := m.ProcessFirstResponse("I'm Harsh, working on Go agent code in EST. Need a refactor done quickly.")
	if err != nil {
		t.Fatalf("ProcessFirstResponse: %v", err)
	}
	if profile == nil {
		t.Fatal("expected profile, got nil")
	}
	if profile.UserName != "Harsh" {
		t.Errorf("expected UserName Harsh, got %q", profile.UserName)
	}
	// File should now exist and parse.
	data, err := os.ReadFile(filepath.Join(dir, "relationship.json"))
	if err != nil {
		t.Fatalf("relationship.json not written: %v", err)
	}
	var loaded ColdStartProfile
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("relationship.json malformed: %v", err)
	}
	if loaded.UserName != profile.UserName {
		t.Errorf("persisted UserName = %q, want %q", loaded.UserName, profile.UserName)
	}
}

func TestColdStartManager_NotColdStartAfterAnswer(t *testing.T) {
	dir := t.TempDir()
	m := NewColdStartManager(dir)
	_, err := m.ProcessFirstResponse("hi i'm Bob")
	if err != nil {
		t.Fatal(err)
	}
	// New manager pointed at the same dir should NOT be cold start.
	m2 := NewColdStartManager(dir)
	if m2.IsColdStart() {
		t.Error("after answer, fresh manager pointed at same dir should NOT be cold start")
	}
}

func TestColdStartManager_SecondAnswerIsNoOp(t *testing.T) {
	dir := t.TempDir()
	m := NewColdStartManager(dir)
	p1, _ := m.ProcessFirstResponse("first answer, I am Alice")
	p2, _ := m.ProcessFirstResponse("second answer, I am Bob")
	if p1 == nil {
		t.Fatal("first call should return profile")
	}
	if p2 != nil {
		t.Error("second call should be a no-op (idempotent)")
	}
}

func TestColdStartManager_LoadExistingProfile(t *testing.T) {
	dir := t.TempDir()
	m := NewColdStartManager(dir)
	if p, _ := m.LoadExistingProfile(); p != nil {
		t.Error("LoadExistingProfile on empty dir should return nil")
	}
	_, _ = m.ProcessFirstResponse("hi, urgent stuff, I'm Carol")
	p, err := m.LoadExistingProfile()
	if err != nil {
		t.Fatalf("LoadExistingProfile: %v", err)
	}
	if p == nil {
		t.Fatal("LoadExistingProfile should return profile after persist")
	}
	if p.UserName != "Carol" {
		t.Errorf("loaded UserName = %q, want Carol", p.UserName)
	}
}

func TestColdStartManager_EmptyFileTreatedAsCold(t *testing.T) {
	dir := t.TempDir()
	// Touch empty relationship.json.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "relationship.json"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewColdStartManager(dir)
	if !m.IsColdStart() {
		t.Error("empty file should be treated as cold start")
	}
}
