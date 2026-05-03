package animation

import (
	"os"
	"testing"
)

func TestEnabled_Default(t *testing.T) {
	SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")
	os.Unsetenv("TERM")
	if !Enabled(80) {
		t.Fatal("animations should be enabled by default at width 80")
	}
}

func TestEnabled_ConfigOff(t *testing.T) {
	SetEnabled(false)
	defer SetEnabled(true)
	if Enabled(120) {
		t.Fatal("config-disabled animations should report disabled")
	}
}

func TestEnabled_EnvKill(t *testing.T) {
	SetEnabled(true)
	os.Setenv("ETHOS_NO_ANIMATIONS", "1")
	defer os.Unsetenv("ETHOS_NO_ANIMATIONS")
	if Enabled(120) {
		t.Fatal("ETHOS_NO_ANIMATIONS=1 should disable")
	}
}

func TestEnabled_DumbTerm(t *testing.T) {
	SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")
	old := os.Getenv("TERM")
	os.Setenv("TERM", "dumb")
	defer os.Setenv("TERM", old)
	if Enabled(120) {
		t.Fatal("dumb terminal should disable animations")
	}
}

func TestEnabled_NarrowTerm(t *testing.T) {
	SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")
	os.Unsetenv("TERM")
	if Enabled(40) {
		t.Fatal("narrow terminal should disable animations")
	}
	if Enabled(MinTermWidth) != true {
		t.Fatal("MinTermWidth should be allowed")
	}
}

func TestEnabled_ZeroWidthBypassesWidthCheck(t *testing.T) {
	SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")
	os.Unsetenv("TERM")
	// width=0 means "unknown" — treat as enabled so first frame can render
	// before WindowSizeMsg arrives.
	if !Enabled(0) {
		t.Fatal("unknown width (0) should be treated as enabled")
	}
}
