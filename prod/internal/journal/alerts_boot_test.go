package journal

import (
	"testing"
)

// TestAlertStore_BootReaderFlow simulates the boot path: a producer creates an
// alert, the reader (boot) calls Pending() and DismissAll(). After dismiss,
// Pending() must be empty. Mirrors what pkg/tui Init does on launch.
func TestAlertStore_BootReaderFlow(t *testing.T) {
	dir := t.TempDir()
	store := NewAlertStore(dir)

	// Producer side.
	if err := store.Create(AlertFrustration, "task X failed twice", "sid"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Create(AlertPatternDetected, "fix x4", "sid"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Boot reader.
	pending := store.Pending()
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending alerts, got %d", len(pending))
	}

	// Verify presented as toast-able strings.
	seen := map[AlertType]bool{}
	for _, a := range pending {
		seen[a.Type] = true
	}
	if !seen[AlertFrustration] || !seen[AlertPatternDetected] {
		t.Fatalf("missing expected alert types, got %v", seen)
	}

	// Dismiss after surfacing.
	if err := store.DismissAll(); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if rem := store.Pending(); len(rem) != 0 {
		t.Fatalf("expected 0 pending after dismiss, got %d", len(rem))
	}
}
