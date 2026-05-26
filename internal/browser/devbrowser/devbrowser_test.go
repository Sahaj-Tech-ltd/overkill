package devbrowser

import (
	"strings"
	"testing"
)

func TestNewManager_HasSensibleDefaults(t *testing.T) {
	m := NewManager()
	if m.PageTimeout == 0 {
		t.Error("PageTimeout should default non-zero")
	}
	if m.IdleTimeout == 0 {
		t.Error("IdleTimeout should default non-zero")
	}
	if len(m.ListPages()) != 0 {
		t.Errorf("fresh manager should have no pages, got %v", m.ListPages())
	}
}

func TestOpen_RequiresName(t *testing.T) {
	m := NewManager()
	defer m.Shutdown()
	_, err := m.Open("", "https://example.com")
	if err == nil {
		t.Error("empty name should error")
	}
}

func TestOpen_RejectsUnsafeURL(t *testing.T) {
	m := NewManager()
	defer m.Shutdown()
	cases := []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"http://localhost/",
		"http://169.254.169.254/",
		"http://10.0.0.1/",
	}
	for _, raw := range cases {
		if _, err := m.Open("test", raw); err == nil {
			t.Errorf("Open should reject unsafe URL %q before spawning chrome", raw)
		}
	}
	// We never opened a page (chrome never spawned because validate
	// failed first); confirm no leaks.
	if len(m.ListPages()) != 0 {
		t.Errorf("rejected URLs must not create pages: %v", m.ListPages())
	}
}

func TestSnapshot_UnknownPageErrors(t *testing.T) {
	m := NewManager()
	defer m.Shutdown()
	_, err := m.Snapshot("never-opened")
	if err == nil {
		t.Fatal("snapshot on unknown page must error")
	}
	if !strings.Contains(err.Error(), "unknown page") {
		t.Errorf("error should explain: %v", err)
	}
}

func TestClick_RequiresSelector(t *testing.T) {
	m := NewManager()
	defer m.Shutdown()
	_, err := m.Click("any-page", "")
	if err == nil {
		t.Error("empty selector should error")
	}
}

func TestType_RequiresSelector(t *testing.T) {
	m := NewManager()
	defer m.Shutdown()
	_, err := m.Type("any-page", "", "text")
	if err == nil {
		t.Error("empty selector should error")
	}
}

func TestClose_UnknownPageIsNoOp(t *testing.T) {
	m := NewManager()
	defer m.Shutdown()
	// Should not panic.
	m.Close("never-opened")
}

func TestSnapshotJS_ContainsExpectedCaps(t *testing.T) {
	// Pin that the JS template baked the Go-side caps in. If the
	// caps drift apart, this fires.
	if !strings.Contains(snapshotJS, "50") {
		t.Errorf("snapshot JS should contain heading/link cap (50)")
	}
	if !strings.Contains(snapshotJS, "6000") {
		t.Errorf("snapshot JS should contain text cap (6000)")
	}
	// And that it's a function expression returning structured data.
	if !strings.Contains(snapshotJS, "document.location.href") {
		t.Errorf("snapshot JS should reference document.location: %s", snapshotJS[:200])
	}
}
