package routing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/models"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// providersModel is a local alias so the legacy-fallback test reads as
// composable struct literals instead of the long providers.Model{...}
// form.
type providersModel = providers.Model

// writeModelTOML is a tiny test helper mirroring the one in internal/models.
func writeModelTOML(t *testing.T, root, relpath, body string) {
	t.Helper()
	full := filepath.Join(root, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func loadTestCatalog(t *testing.T) *models.Catalog {
	t.Helper()
	root := t.TempDir()

	writeModelTOML(t, root, "anthropic/claude-opus-4.toml", `
family = "claude-opus"
display_name = "Claude Opus 4"
context_window = 200000
[capabilities]
reasoning = true
tool_call = true
attachment = true
[cost]
input = 15.0
output = 75.0
[modalities]
input = ["text", "image"]
`)
	writeModelTOML(t, root, "anthropic/claude-haiku-4.toml", `
family = "claude-haiku"
context_window = 200000
[capabilities]
tool_call = true
[cost]
input = 0.25
output = 1.25
`)
	writeModelTOML(t, root, "anthropic/claude-opus-3.toml", `
family = "claude-opus"
context_window = 200000
deprecated = true
[capabilities]
tool_call = true
[cost]
input = 30.0
output = 150.0
`)
	writeModelTOML(t, root, "openai/gpt-5.toml", `
family = "gpt-5"
context_window = 128000
[capabilities]
reasoning = true
tool_call = true
[cost]
input = 5.0
output = 15.0
`)

	cat, err := models.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func TestProviderModelsFromCatalog_Empty(t *testing.T) {
	cat, _ := models.Load("")
	if got := ProviderModelsFromCatalog(cat); got != nil {
		t.Errorf("empty catalog should produce nil ProviderModels, got %v", got)
	}
}

func TestProviderModelsFromCatalog_GroupsByProvider(t *testing.T) {
	cat := loadTestCatalog(t)
	pms := ProviderModelsFromCatalog(cat)
	if len(pms) != 2 {
		t.Fatalf("expected 2 providers (anthropic + openai), got %d", len(pms))
	}
	// Order is alphabetical.
	if pms[0].ProviderName != "anthropic" || pms[1].ProviderName != "openai" {
		t.Errorf("provider order: %s, %s", pms[0].ProviderName, pms[1].ProviderName)
	}
	// Anthropic should have 3 models (opus-4, opus-3, haiku).
	if len(pms[0].Models) != 3 {
		t.Errorf("expected 3 anthropic models, got %d", len(pms[0].Models))
	}
}

func TestModelInFamily_PicksCheapest(t *testing.T) {
	cat := loadTestCatalog(t)
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), nil, "")
	r.WithCatalog(cat)

	id, prov, err := r.ModelInFamily("claude-opus")
	if err != nil {
		t.Fatalf("ModelInFamily: %v", err)
	}
	if id != "anthropic/claude-opus-4" {
		t.Errorf("expected opus-4 (output=75, non-deprecated), got %s", id)
	}
	if prov != "anthropic" {
		t.Errorf("provider derivation failed: %s", prov)
	}
}

func TestModelInFamily_SkipsDeprecated(t *testing.T) {
	cat := loadTestCatalog(t)
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), nil, "")
	r.WithCatalog(cat)
	id, _, err := r.ModelInFamily("claude-opus")
	if err != nil {
		t.Fatal(err)
	}
	if id == "anthropic/claude-opus-3" {
		t.Error("deprecated opus-3 should be skipped despite presence in family")
	}
}

func TestModelInFamily_UnknownFamily(t *testing.T) {
	cat := loadTestCatalog(t)
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), nil, "")
	r.WithCatalog(cat)
	if _, _, err := r.ModelInFamily("nope"); err == nil {
		t.Error("unknown family should error")
	}
}

func TestModelWithCapabilities_Reasoning(t *testing.T) {
	cat := loadTestCatalog(t)
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), nil, "")
	r.WithCatalog(cat)

	id, _, err := r.ModelWithCapabilities(models.Capabilities{Reasoning: true})
	if err != nil {
		t.Fatalf("ModelWithCapabilities: %v", err)
	}
	// Cheapest reasoning model: gpt-5 (output=15) vs opus-4 (output=75).
	if id != "openai/gpt-5" {
		t.Errorf("expected gpt-5 as cheapest reasoning model, got %s", id)
	}
}

func TestModelWithCapabilities_AttachmentExcludesGpt5(t *testing.T) {
	cat := loadTestCatalog(t)
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), nil, "")
	r.WithCatalog(cat)

	id, _, err := r.ModelWithCapabilities(models.Capabilities{Attachment: true})
	if err != nil {
		t.Fatalf("ModelWithCapabilities: %v", err)
	}
	// Only opus-4 has attachment=true in the fixture.
	if id != "anthropic/claude-opus-4" {
		t.Errorf("expected opus-4 (only attachment-capable in fixture), got %s", id)
	}
}

func TestFailoverInFamily_OrderedByCost(t *testing.T) {
	cat := loadTestCatalog(t)
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), nil, "")
	r.WithCatalog(cat)

	chain := r.FailoverInFamily("claude-opus")
	// opus-3 is deprecated → excluded. Only opus-4 remains.
	if len(chain) != 1 {
		t.Fatalf("expected 1 non-deprecated opus in failover, got %v", chain)
	}
	if chain[0] != "anthropic/claude-opus-4" {
		t.Errorf("failover[0] = %s", chain[0])
	}
}

func TestFailoverInFamily_EmptyForUnknown(t *testing.T) {
	cat := loadTestCatalog(t)
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), nil, "")
	r.WithCatalog(cat)
	if got := r.FailoverInFamily("does-not-exist"); len(got) != 0 {
		t.Errorf("unknown family should give empty failover, got %v", got)
	}
}

func TestModelInFamily_LegacyFallback(t *testing.T) {
	// No catalog attached — should use the providers slice.
	pms := []ProviderModels{{
		ProviderName: "fake",
		Models: []providersModel{
			{ID: "fake/cheap", Family: "claude-opus", CostOut: 5.0},
			{ID: "fake/pricey", Family: "claude-opus", CostOut: 20.0},
		},
	}}
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), pms, "")
	id, _, err := r.ModelInFamily("claude-opus")
	if err != nil {
		t.Fatal(err)
	}
	if id != "fake/cheap" {
		t.Errorf("legacy fallback should pick cheapest, got %s", id)
	}
}
