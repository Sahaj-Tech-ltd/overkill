// redteam_segments_test.go — path-traversal and edge-case red-team
// tests for memory.SegmentStore.
//
// Run:
//
//	go test -race -count=1 -timeout 30s ./internal/memory/...
package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rtSegStore returns a SegmentStore whose store dir and defaultRoot
// both live inside fresh t.TempDir() trees. A canary file is placed
// one level above the store dir so we can detect escapes.
func rtSegStore(t *testing.T) (*SegmentStore, string, string) {
	t.Helper()
	base := t.TempDir()
	storeDir := filepath.Join(base, "segments")
	defaultRoot := filepath.Join(base, "project") // the project root segments point at
	if err := os.MkdirAll(defaultRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(base, "canary.txt")
	if err := os.WriteFile(canary, []byte("CANARY"), 0o600); err != nil {
		t.Fatalf("canary write: %v", err)
	}
	return NewSegmentStore(storeDir, defaultRoot), canary, defaultRoot
}

// --- C-3 / segments path-traversal probes ---

// TestRedteam_Seg_Get_Traversal checks that Get("../../etc/passwd") is
// blocked before reading an out-of-bounds file.
func TestRedteam_Seg_Get_Traversal(t *testing.T) {
	s, _, _ := rtSegStore(t)

	result, err := s.Get("../../etc/passwd")
	if err == nil && result != nil {
		t.Fatal("VULNERABILITY: Get returned data for traversal ID")
	}
	if err != nil {
		t.Logf("PASS — Get blocked with: %v", err)
	} else {
		t.Logf("SAFE — path cleaned, file not found (nil, nil)")
	}
}

// TestRedteam_Seg_Touch_Traversal checks that Touch cannot write to a
// path outside the store dir.
func TestRedteam_Seg_Touch_Traversal(t *testing.T) {
	s, canary, _ := rtSegStore(t)

	err := s.Touch("../../etc/cron.d/evil")

	// Canary must be intact.
	data, statErr := os.ReadFile(canary)
	if statErr != nil || string(data) != "CANARY" {
		t.Fatal("VULNERABILITY: Touch modified a file outside store directory")
	}
	if err != nil {
		t.Logf("PASS — Touch blocked with: %v", err)
	} else {
		t.Logf("SAFE — Touch returned nil but canary intact")
	}
}

// TestRedteam_Seg_Delete_Traversal checks that Delete cannot remove
// files outside the store dir.
func TestRedteam_Seg_Delete_Traversal(t *testing.T) {
	s, canary, _ := rtSegStore(t)

	err := s.Delete("../../etc/hosts")

	// Canary must survive.
	if _, statErr := os.Stat(canary); os.IsNotExist(statErr) {
		t.Fatal("VULNERABILITY: Delete removed a file outside the store directory")
	}
	if err != nil {
		t.Logf("PASS — Delete blocked with: %v", err)
	} else {
		t.Logf("SAFE — Delete returned nil (idempotent miss) but canary intact")
	}
}

// TestRedteam_Seg_LoadFiles_GlobEscape checks what happens when a
// Segment is created with Glob: "../../**". The glob expansion is
// handled by expandGlob(root, pattern) where root = defaultRoot.
// A malicious glob of "../../**" would walk TWO levels above
// defaultRoot (i.e. above base/project → into base → into system).
//
// We create a canary file inside base/ (outside defaultRoot) and
// verify it does NOT appear in the LoadFiles result.
func TestRedteam_Seg_LoadFiles_GlobEscape(t *testing.T) {
	s, canary, defaultRoot := rtSegStore(t)

	// Place a regular file inside defaultRoot so the segment is valid.
	legit := filepath.Join(defaultRoot, "legit.go")
	if err := os.WriteFile(legit, []byte("package p"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create segment with an escaping glob.
	seg, err := s.Create(&Segment{
		Name:  "escape-glob",
		Globs: []string{"../../**"},
	})
	if err != nil {
		t.Logf("SAFE — Create rejected escaping glob with error: %v", err)
		return
	}

	files, err := s.LoadFiles(seg.ID)
	if err != nil {
		t.Logf("SAFE — LoadFiles returned error for escaping glob: %v", err)
		return
	}

	// Check whether canary appears in results.
	for _, f := range files {
		if f == canary {
			t.Errorf("VULNERABILITY: LoadFiles returned canary file %q via glob escape ../../**", canary)
		}
	}

	// Broader: check whether any path escapes defaultRoot.
	base := filepath.Dir(defaultRoot)
	for _, f := range files {
		absF, _ := filepath.Abs(f)
		absDefault, _ := filepath.Abs(defaultRoot)
		if !strings.HasPrefix(absF, absDefault) {
			t.Errorf("VULNERABILITY: LoadFiles returned out-of-bounds path %q (base=%q defaultRoot=%q)",
				f, base, defaultRoot)
		}
	}
	t.Logf("files returned by escaping glob (%d): %v", len(files), files)
}

// TestRedteam_Seg_EmptyID verifies that an empty segment ID is
// cleanly rejected and does not panic.
func TestRedteam_Seg_EmptyID(t *testing.T) {
	s, _, _ := rtSegStore(t)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC on empty ID: %v", r)
		}
	}()

	result, err := s.Get("")
	if err == nil {
		t.Errorf("Get('') should error, got result=%v", result)
	} else {
		t.Logf("PASS — Get('') blocked: %v", err)
	}

	if err2 := s.Touch(""); err2 == nil {
		t.Logf("NOTE: Touch('') returned nil (may be loadLocked error converted)")
	} else {
		t.Logf("PASS — Touch('') blocked: %v", err2)
	}

	if err3 := s.Delete(""); err3 == nil {
		t.Logf("NOTE: Delete('') returned nil (idempotent)")
	}
}

// TestRedteam_Seg_NullByteID verifies null bytes in segment IDs do not
// bypass path guards or panic.
func TestRedteam_Seg_NullByteID(t *testing.T) {
	s, _, _ := rtSegStore(t)
	malicious := "valid\x00../../etc/passwd"

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC on null-byte ID: %v", r)
		}
	}()

	result, err := s.Get(malicious)
	if err == nil && result != nil {
		t.Fatal("VULNERABILITY: null-byte ID returned data")
	}
	t.Logf("SAFE — Get(null-byte) → result=%v err=%v", result, err)
}

// TestRedteam_Seg_ConstructedPaths logs the exact filesystem paths
// SafePath would produce for each attack vector.
func TestRedteam_Seg_ConstructedPaths(t *testing.T) {
	base := t.TempDir()
	storeDir := filepath.Join(base, "segments")

	type probe struct {
		label string
		id    string
	}
	probes := []probe{
		{"double-dot etc/passwd", "../../etc/passwd"},
		{"cron.d evil", "../../etc/cron.d/evil"},
		{"etc/hosts", "../../etc/hosts"},
		{"empty", ""},
		{"dot", "."},
		{"dotdot", ".."},
		{"null-byte", "valid\x00../../etc/passwd"},
	}

	for _, p := range probes {
		p := p
		t.Run(p.label, func(t *testing.T) {
			name := p.id + ".json"
			cleaned := filepath.Clean(name)
			joined := filepath.Join(storeDir, cleaned)
			absDir, _ := filepath.Abs(storeDir)
			absJoined, _ := filepath.Abs(joined)
			escapesDir := !strings.HasPrefix(absJoined, absDir+string(os.PathSeparator)) && absJoined != absDir

			t.Logf("id=%q  →  cleaned=%q  →  full=%q  →  wouldEscape=%v",
				p.id, cleaned, joined, escapesDir)
		})
	}
}

// TestRedteam_Seg_GlobEscape_WithRootDir checks whether a segment with
// an explicit RootDir pointing outside defaultRoot can use LoadFiles
// to escape. expandGlob walks from RootDir, not from the store dir,
// so the question is whether RootDir is validated.
func TestRedteam_Seg_GlobEscape_WithRootDir(t *testing.T) {
	s, _, defaultRoot := rtSegStore(t)

	// Place a file we should NOT be able to reach inside /tmp directly
	// (not inside defaultRoot).
	outsideRoot := filepath.Dir(defaultRoot) // base/, one level up
	targetFile := filepath.Join(outsideRoot, "should_not_read.txt")
	if err := os.WriteFile(targetFile, []byte("SECRET"), 0o600); err != nil {
		t.Skipf("cannot create target file %q: %v", targetFile, err)
	}
	t.Cleanup(func() { os.Remove(targetFile) })

	// Directly manipulate a segment's RootDir to point outside.
	// We create a valid segment first, then patch it.
	seg, err := s.Create(&Segment{
		Name:  "rootdir-escape",
		Globs: []string{"*.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Patch RootDir to escape defaultRoot.
	seg.RootDir = outsideRoot
	s.mu.Lock()
	saveErr := s.saveLocked(seg)
	s.mu.Unlock()
	if saveErr != nil {
		t.Fatalf("save patched segment: %v", saveErr)
	}

	files, loadErr := s.LoadFiles(seg.ID)
	if loadErr != nil {
		t.Logf("SAFE — LoadFiles errored when RootDir escaped defaultRoot: %v", loadErr)
		return
	}
	for _, f := range files {
		if f == targetFile {
			t.Errorf("VULNERABILITY: LoadFiles traversed outside defaultRoot via RootDir override, returned %q", f)
		}
	}
	t.Logf("NOTE: RootDir override did not raise an error; files returned (%d): %v", len(files), files)
	t.Logf("  (LoadFiles does not validate RootDir against defaultRoot — see findings)")
}
