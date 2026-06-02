package diagnostic

import (
	"errors"
	"strings"
	"testing"
)

func TestLadder_AllTiers(t *testing.T) {
	tiers := AllTiers()
	if len(tiers) != 10 {
		t.Fatalf("expected 10 tiers, got %d", len(tiers))
	}
	for i, tier := range tiers {
		if int(tier) != i+1 {
			t.Errorf("tier ordering off at %d: %v", i, tier)
		}
		if tier.Name() == "" {
			t.Errorf("tier %v has no name", tier)
		}
		if tier.Description() == "" {
			t.Errorf("tier %v has no description", tier)
		}
	}
}

func TestLadder_ClimbAndExhaust(t *testing.T) {
	l := NewLadder()
	if l.Current() != TierCompile {
		t.Fatalf("start = %v want compile", l.Current())
	}
	for i := 0; i < 9; i++ {
		if _, err := l.Climb(); err != nil {
			t.Fatalf("climb %d returned err early: %v", i, err)
		}
	}
	if l.Current() != TierHITLBash {
		t.Fatalf("top = %v want hitl-bash", l.Current())
	}
	_, err := l.Climb()
	if !errors.Is(err, ErrLadderExhausted) {
		t.Fatalf("expected ErrLadderExhausted, got %v", err)
	}
}

func TestLadder_FromTierJumpsAhead(t *testing.T) {
	l := FromTier(TierCurl)
	if l.Current() != TierCurl {
		t.Fatalf("got %v want curl", l.Current())
	}
}

func TestTier_SuggestedCommand(t *testing.T) {
	if cmd := TierUnitTest.SuggestedCommand("go"); !strings.Contains(cmd, "go test") {
		t.Errorf("unit-test go: got %q", cmd)
	}
	if cmd := TierLint.SuggestedCommand("rust"); !strings.Contains(cmd, "clippy") {
		t.Errorf("lint rust: got %q", cmd)
	}
	if cmd := TierCurl.SuggestedCommand(""); !strings.Contains(cmd, "curl") {
		t.Errorf("curl: got %q", cmd)
	}
}

func TestLadder_Reset(t *testing.T) {
	l := FromTier(TierProperty)
	l.Reset()
	if l.Current() != TierCompile {
		t.Fatalf("reset got %v", l.Current())
	}
}
