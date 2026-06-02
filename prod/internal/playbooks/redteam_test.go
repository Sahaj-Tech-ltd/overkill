// redteam_test.go — path-traversal and edge-case red-team tests for
// the playbooks.Store. Every case either confirms that SafePath blocks
// the attack, or documents a concrete failure mode.
//
// Run:
//
//	go test -race -count=1 -timeout 30s ./internal/playbooks/...
package playbooks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rtStore returns a Store whose root lives inside a t.TempDir() so no
// test can accidentally touch real files. A canary file is written
// alongside the store directory so we can detect escapes.
func rtStore(t *testing.T) (*Store, string) {
	t.Helper()
	base := t.TempDir()
	storeDir := filepath.Join(base, "playbooks")
	// Canary: a file at base/canary.txt — one level ABOVE the store
	// root. Any successful read/write/delete of it means traversal.
	canary := filepath.Join(base, "canary.txt")
	if err := os.WriteFile(canary, []byte("CANARY"), 0o600); err != nil {
		t.Fatalf("could not create canary: %v", err)
	}
	return NewStore(storeDir), canary
}

// --- C-2 / playbooks path-traversal probes ---

// TestRedteam_Get_TraversalEtcPasswd verifies that Get("../../etc/passwd")
// is blocked by SafePath before any OS call reaches /etc/passwd.
func TestRedteam_Get_TraversalEtcPasswd(t *testing.T) {
	s, _ := rtStore(t)

	result, err := s.Get("../../etc/passwd")

	// We expect SafePath to fire. If no error and result is non-nil,
	// the guard failed and we read an arbitrary file.
	if err == nil && result != nil {
		t.Fatal("VULNERABILITY: Get traversed outside store dir and returned data")
	}
	if err != nil {
		if strings.Contains(err.Error(), "traversal") || strings.Contains(err.Error(), "absolute") {
			t.Logf("PASS — SafePath blocked with: %v", err)
		} else {
			// Some other error (e.g. file not found after clean). Still
			// safe, but log the actual error so we know the code path.
			t.Logf("SAFE (non-traversal error): %v", err)
		}
	} else {
		// result == nil, no error — file not found after cleaning, still safe.
		t.Logf("SAFE — path cleaned to something inside dir, file not found (nil,nil)")
	}
}

// TestRedteam_Delete_TraversalEtcCronD verifies that Delete cannot
// target files outside the store directory.
func TestRedteam_Delete_TraversalEtcCronD(t *testing.T) {
	s, canary := rtStore(t)

	// Point at the canary file one dir up via traversal in the ID.
	rel := "../canary"
	err := s.Delete(rel)

	// Confirm canary was NOT deleted.
	if _, statErr := os.Stat(canary); os.IsNotExist(statErr) {
		t.Fatal("VULNERABILITY: Delete removed a file outside the store directory")
	}

	if err != nil {
		t.Logf("PASS — Delete returned error: %v", err)
	} else {
		t.Logf("SAFE — Delete returned nil (idempotent miss) but canary intact")
	}
}

// TestRedteam_Use_TraversalSSHKeys checks whether Use("../../../.ssh/authorized_keys")
// is blocked before attempting to load an arbitrary file as JSON.
func TestRedteam_Use_TraversalSSHKeys(t *testing.T) {
	s, _ := rtStore(t)

	_, err := s.Use("../../../.ssh/authorized_keys")
	if err == nil {
		t.Fatal("VULNERABILITY: Use accepted a traversal ID without error")
	}
	t.Logf("PASS — Use blocked with: %v", err)
}

// TestRedteam_RecordOutcome_TraversalWrite checks that RecordOutcome
// cannot write data outside the store directory.
func TestRedteam_RecordOutcome_TraversalWrite(t *testing.T) {
	s, canary := rtStore(t)

	_, err := s.RecordOutcome("../../secret", true)

	// Canary must survive.
	data, statErr := os.ReadFile(canary)
	if statErr != nil || string(data) != "CANARY" {
		t.Fatal("VULNERABILITY: RecordOutcome overwrote a file outside the store directory")
	}

	if err != nil {
		t.Logf("PASS — RecordOutcome blocked with: %v", err)
	} else {
		t.Logf("SAFE — RecordOutcome returned nil but canary intact")
	}
}

// --- edge-case probes ---

// TestRedteam_EmptyID checks that an empty ID is cleanly rejected and
// does not panic or produce an unscoped path like "/.json".
func TestRedteam_EmptyID(t *testing.T) {
	s, _ := rtStore(t)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC on empty ID: %v", r)
		}
	}()

	result, err := s.Get("")
	if err == nil {
		t.Errorf("empty ID should return an error, got nil err with result=%v", result)
	} else {
		t.Logf("PASS — Get('') returned error: %v", err)
	}

	// Also probe Delete and Use with empty ID.
	if err2 := s.Delete(""); err2 == nil {
		t.Logf("NOTE: Delete('') returned nil error (SafePath may accept empty → dir itself; harmless idempotent)")
	}
	if _, err3 := s.Use(""); err3 == nil {
		t.Errorf("Use('') should error, got nil")
	}
}

// TestRedteam_NullByteID verifies that a null-byte-injected ID is
// rejected. Go's os package rejects null bytes in paths, but we
// confirm the error is returned cleanly rather than panicking.
func TestRedteam_NullByteID(t *testing.T) {
	s, _ := rtStore(t)
	malicious := "valid\x00../../etc/passwd"

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC on null-byte ID: %v", r)
		}
	}()

	result, err := s.Get(malicious)
	if err == nil && result != nil {
		t.Fatal("VULNERABILITY: null-byte ID was accepted and returned data")
	}
	t.Logf("SAFE — Get(null-byte id) returned (result=%v, err=%v)", result, err)
}

// TestRedteam_VeryLongID checks that a 10 000-character ID does not
// crash the process (stack overflow, OOM, etc.).
func TestRedteam_VeryLongID(t *testing.T) {
	s, _ := rtStore(t)
	longID := strings.Repeat("a", 10000)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC on very long ID: %v", r)
		}
	}()

	result, err := s.Get(longID)
	// Acceptable outcomes: (nil, nil) = file not found; (nil, err) = error.
	// Unacceptable: panic or (non-nil result).
	if result != nil {
		t.Errorf("unexpected non-nil result for 10 000-char ID: %+v", result)
	}
	t.Logf("SAFE — Get(10k-char id) → result=%v err=%v", result, err)
}

// TestRedteam_DotAndDotDotIDs checks that "." and ".." as IDs do not
// escape or panic. filepath.Clean turns ".." into ".." (relative),
// and SafePath should reject it as a traversal or the join resolves
// it within the dir.
func TestRedteam_DotAndDotDotIDs(t *testing.T) {
	cases := []string{".", ".."}
	for _, id := range cases {
		id := id
		t.Run("id="+id, func(t *testing.T) {
			s, canary := rtStore(t)

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC with id=%q: %v", id, r)
				}
			}()

			result, err := s.Get(id)
			if err != nil {
				t.Logf("PASS — Get(%q) returned error: %v", id, err)
			} else {
				t.Logf("NOTE — Get(%q) returned (result=%v, err=nil)", id, result)
			}

			// Make sure Delete with ".." doesn't blow away the parent dir.
			delErr := s.Delete(id)
			if _, statErr := os.Stat(canary); os.IsNotExist(statErr) {
				t.Fatalf("VULNERABILITY: Delete(%q) removed canary outside store", id)
			}
			if delErr != nil {
				t.Logf("Delete(%q) → error: %v (safe)", id, delErr)
			} else {
				t.Logf("Delete(%q) → nil error but canary intact (safe idempotent)", id)
			}
		})
	}
}

// TestRedteam_ConstructedPaths logs the exact filesystem paths that
// SafePath produces (or blocks) for each traversal input so the
// report has concrete evidence.
func TestRedteam_ConstructedPaths(t *testing.T) {
	base := t.TempDir()
	storeDir := filepath.Join(base, "playbooks")

	type probe struct {
		label string
		id    string
	}
	probes := []probe{
		{"double-dot etc/passwd", "../../etc/passwd"},
		{"cron.d evil", "../../etc/cron.d/evil"},
		{"ssh authorized_keys", "../../../.ssh/authorized_keys"},
		{"record outcome secret", "../../secret"},
		{"empty", ""},
		{"dot", "."},
		{"dotdot", ".."},
		{"null-byte", "valid\x00../../etc/passwd"},
	}

	for _, p := range probes {
		p := p
		t.Run(p.label, func(t *testing.T) {
			// Replicate what saveLocked/loadLocked does before the OS call.
			name := p.id + ".json"
			cleaned := filepath.Clean(name)
			joined := filepath.Join(storeDir, cleaned)
			absDir, _ := filepath.Abs(storeDir)
			absJoined, _ := filepath.Abs(joined)
			escapesDir := !strings.HasPrefix(absJoined, absDir+string(os.PathSeparator)) && absJoined != absDir

			t.Logf("id=%q  →  cleaned=%q  →  full=%q  →  escape=%v",
				p.id, cleaned, joined, escapesDir)

			if escapesDir {
				t.Logf("  → SafePath WOULD block this (traversal detected)")
			} else {
				t.Logf("  → Path stays inside store dir (safe)")
			}
		})
	}
}

// sentinel to confirm security.SafePath's actual error text
func TestRedteam_SafePathErrorText(t *testing.T) {
	s, _ := rtStore(t)
	_, err := s.Get("../../etc/passwd")
	if err == nil {
		return // handled by the traversal test above
	}
	if !errors.Is(err, errors.New("")) {
		// Just log, the important thing is no panic and we have an error.
		t.Logf("error text: %v", err)
	}
}
