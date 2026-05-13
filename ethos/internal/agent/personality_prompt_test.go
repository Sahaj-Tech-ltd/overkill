package agent

import (
	"strings"
	"testing"
)

func TestPersonalitySection_NoProvider(t *testing.T) {
	a := &Agent{}
	if got := a.personalitySection(); got != "" {
		t.Errorf("personalitySection without provider = %q, want empty", got)
	}
}

func TestPersonalitySection_Provider(t *testing.T) {
	a := &Agent{}
	a.SetPersonalityProvider(func() string {
		return "be a colleague, not a servant"
	})
	got := a.personalitySection()
	if !strings.Contains(got, "colleague") {
		t.Errorf("personalitySection = %q, want directive content", got)
	}
}

func TestPersonalitySection_PanicRecovery(t *testing.T) {
	a := &Agent{}
	a.SetPersonalityProvider(func() string {
		panic("intentional")
	})
	if got := a.personalitySection(); got != "" {
		t.Errorf("personalitySection on panic = %q, want empty", got)
	}
}

func TestPersonalitySection_NilClears(t *testing.T) {
	a := &Agent{}
	a.SetPersonalityProvider(func() string { return "hello" })
	if a.personalitySection() == "" {
		t.Fatal("expected provider output, got empty")
	}
	a.SetPersonalityProvider(nil)
	if got := a.personalitySection(); got != "" {
		t.Errorf("personalitySection after nil = %q, want empty", got)
	}
}
