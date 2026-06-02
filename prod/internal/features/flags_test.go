package features

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnabled_UnknownIsFalse(t *testing.T) {
	m := NewManager()
	if m.Enabled("nope", EvalContext{}) {
		t.Error("unknown flag must default to false")
	}
}

func TestEnabled_DefaultOnly(t *testing.T) {
	m := NewManager()
	m.Register(&Flag{Name: "x", Default: true})
	if !m.Enabled("x", EvalContext{}) {
		t.Error("default-true should enable when nothing else applies")
	}
	m.Register(&Flag{Name: "y", Default: false})
	if m.Enabled("y", EvalContext{}) {
		t.Error("default-false should disable")
	}
}

func TestEnabled_UserOverrideWins(t *testing.T) {
	m := NewManager()
	m.Register(&Flag{
		Name:    "f",
		Default: false,
		UserOverrides: map[string]bool{
			"alice": true,
			"bob":   false,
		},
	})
	if !m.Enabled("f", EvalContext{UserID: "alice"}) {
		t.Error("user override should beat default-false")
	}
	if m.Enabled("f", EvalContext{UserID: "bob"}) {
		t.Error("explicit user=false should beat default-false (still false)")
	}
	if m.Enabled("f", EvalContext{UserID: "stranger"}) {
		t.Error("non-overridden user should fall through to default")
	}
}

func TestEnabled_ChannelOverrideWhenNoUserMatch(t *testing.T) {
	m := NewManager()
	m.Register(&Flag{
		Name:             "f",
		Default:          false,
		ChannelOverrides: map[string]bool{"slack": true},
	})
	if !m.Enabled("f", EvalContext{Channel: "slack"}) {
		t.Error("channel override should beat default")
	}
	if m.Enabled("f", EvalContext{Channel: "discord"}) {
		t.Error("unmatched channel falls through to default")
	}
}

func TestEnabled_UserOverridePrecedesChannelOverride(t *testing.T) {
	m := NewManager()
	m.Register(&Flag{
		Name:             "f",
		Default:          false,
		UserOverrides:    map[string]bool{"alice": false},
		ChannelOverrides: map[string]bool{"slack": true},
	})
	got := m.Enabled("f", EvalContext{UserID: "alice", Channel: "slack"})
	if got {
		t.Error("user override (false) should beat channel override (true)")
	}
}

func TestEnabled_PercentRolloutStable(t *testing.T) {
	m := NewManager()
	m.Register(&Flag{Name: "f", Default: false, Percent: 50})

	// A specific subject's decision must be stable across many calls.
	first := m.Enabled("f", EvalContext{Subject: "session-123"})
	for i := 0; i < 50; i++ {
		got := m.Enabled("f", EvalContext{Subject: "session-123"})
		if got != first {
			t.Fatalf("percent rollout decision flipped for same subject (iter %d)", i)
		}
	}
}

func TestEnabled_PercentRolloutSpread(t *testing.T) {
	m := NewManager()
	m.Register(&Flag{Name: "f", Default: false, Percent: 50})

	on := 0
	total := 1000
	for i := 0; i < total; i++ {
		if m.Enabled("f", EvalContext{Subject: subjectN(i)}) {
			on++
		}
	}
	// 50% rollout over 1000 subjects: should land near 500.
	if on < 400 || on > 600 {
		t.Errorf("percent=50 rollout enabled %d/%d subjects; expected ~500", on, total)
	}
}

func subjectN(n int) string {
	const base = "subject-"
	// Cheap unique strings.
	return base + string(rune('a'+(n%26))) + string(rune('a'+((n/26)%26))) + intStr(n)
}

func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestEnabled_PercentZeroIsDisabled(t *testing.T) {
	m := NewManager()
	m.Register(&Flag{Name: "f", Default: false, Percent: 0})
	for i := 0; i < 50; i++ {
		if m.Enabled("f", EvalContext{Subject: subjectN(i)}) {
			t.Fatal("percent=0 must NEVER enable")
		}
	}
}

func TestEnabled_PercentRequiresSubject(t *testing.T) {
	m := NewManager()
	m.Register(&Flag{Name: "f", Default: false, Percent: 100})
	// No subject AND no user → no stable bucket → don't enable
	// (safe default).
	if m.Enabled("f", EvalContext{}) {
		t.Error("percent rollout without subject should fall through to default")
	}
}

func TestEnabled_PercentSubjectFallsBackToUserID(t *testing.T) {
	m := NewManager()
	m.Register(&Flag{Name: "f", Default: false, Percent: 100})
	// percent=100 with a subject derived from UserID should always
	// enable.
	if !m.Enabled("f", EvalContext{UserID: "alice"}) {
		t.Error("percent=100 with UserID subject should enable")
	}
}

func TestLoadFromTOML_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "features.toml")
	content := `
[experimental_steering]
default = false
percent = 25
[experimental_steering.users]
"alice" = true

[show_per_command_metadata]
default = true
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	if err := m.LoadFromTOML(path); err != nil {
		t.Fatalf("LoadFromTOML: %v", err)
	}
	if got := m.Get("experimental_steering"); got == nil {
		t.Fatal("expected experimental_steering flag to load")
	}
	if !m.Enabled("experimental_steering", EvalContext{UserID: "alice"}) {
		t.Error("user override should fire")
	}
	if !m.Enabled("show_per_command_metadata", EvalContext{}) {
		t.Error("default=true flag should be enabled with no context")
	}
}

func TestLoadFromTOML_BadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "features.toml")
	_ = os.WriteFile(path, []byte("not valid ["), 0o600)
	m := NewManager()
	if err := m.LoadFromTOML(path); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected parse error, got %v", err)
	}
}

func TestLoadFromTOML_MissingFileOK(t *testing.T) {
	m := NewManager()
	if err := m.LoadFromTOML("/nonexistent/features.toml"); err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
}
