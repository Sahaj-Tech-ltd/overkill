package tui

import (
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
)

// newPersonalityTestModel builds the minimum *appModel needed to invoke
// buildPersonalityProvider: a Personality at the lowest non-off level so
// InjectPersonality returns deterministic baseline, and an App with the
// transparency / blindspot engines pre-seeded.
func newPersonalityTestModel(t *testing.T) (*appModel, *personality.TransparencyEngine, *personality.BlindSpotDetector) {
	t.Helper()
	te := personality.NewTransparencyEngine("test-model")
	// Seed a failure pattern past the count >=2 threshold so NextWarning
	// has something undeniable to surface.
	te.RecordFailure("refactoring", "test-model")
	te.RecordFailure("refactoring", "test-model")

	bs := personality.NewBlindSpotDetector()
	// Threshold defaults to 4 — push past it so NextWarning will fire.
	for i := 0; i < bs.Threshold; i++ {
		bs.Observe("fix")
	}

	m := &appModel{
		person: personality.New(personality.Config{
			AgentName: "overkill",
			Level:     personality.LevelSubtle,
		}),
		app: &App{
			Transparency: te,
			BlindSpot:    bs,
		},
	}
	return m, te, bs
}

func TestBuildPersonalityProvider_SurfacesTransparencyWarning(t *testing.T) {
	m, _, _ := newPersonalityTestModel(t)
	fn := m.buildPersonalityProvider()
	out := fn()
	if !strings.Contains(out, "[heads-up]") {
		t.Fatalf("expected [heads-up] prefix in output, got: %q", out)
	}
	if !strings.Contains(out, "refactoring") {
		t.Fatalf("expected transparency warning content, got: %q", out)
	}
}

func TestBuildPersonalityProvider_TransparencyRateLimited(t *testing.T) {
	// Build a model with ONLY transparency seeded (no blindspot) so we
	// can isolate transparency's rate-limit behaviour.
	te := personality.NewTransparencyEngine("test-model")
	te.RecordFailure("debugging", "test-model")
	te.RecordFailure("debugging", "test-model")
	m := &appModel{
		person: personality.New(personality.Config{
			AgentName: "overkill",
			Level:     personality.LevelSubtle,
		}),
		app: &App{Transparency: te},
	}
	fn := m.buildPersonalityProvider()

	first := fn()
	second := fn()

	if !strings.Contains(first, "debugging") {
		t.Fatalf("first call: expected warning about 'debugging', got: %q", first)
	}
	if strings.Contains(second, "debugging") {
		t.Fatalf("second call: transparency warning should be rate-limited to once per session, but was repeated: %q", second)
	}
}

func TestBuildPersonalityProvider_BlindSpotSurfacedAndRateLimited(t *testing.T) {
	bs := personality.NewBlindSpotDetector()
	for i := 0; i < bs.Threshold; i++ {
		bs.Observe("refactor")
	}
	m := &appModel{
		person: personality.New(personality.Config{
			AgentName: "overkill",
			Level:     personality.LevelSubtle,
		}),
		app: &App{BlindSpot: bs},
	}
	fn := m.buildPersonalityProvider()

	first := fn()
	second := fn()

	if !strings.Contains(first, "refactor") {
		t.Fatalf("first call: expected blindspot warning mentioning 'refactor', got: %q", first)
	}
	if strings.Contains(second, "refactor") {
		t.Fatalf("second call: blindspot warning should be rate-limited, got repeat: %q", second)
	}
}

func TestBuildPersonalityProvider_NilEnginesAreSafe(t *testing.T) {
	m := &appModel{
		person: personality.New(personality.Config{
			AgentName: "overkill",
			Level:     personality.LevelSubtle,
		}),
		app: &App{}, // no Transparency, no BlindSpot, no Frustration
	}
	fn := m.buildPersonalityProvider()
	// Must not panic. Output may be empty or baseline personality text.
	_ = fn()
}
