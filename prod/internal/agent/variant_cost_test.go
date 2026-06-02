package agent

import (
	"math"
	"testing"
)

func TestComputeVariantCost_KnownPricing(t *testing.T) {
	// Stub the model pricer so we don't depend on the live catalog.
	old := modelPricer
	defer func() { modelPricer = old }()
	modelPricer = func(id string) (float64, float64, bool) {
		if id == "test-model" {
			return 3.00, 15.00, true
		}
		return 0, 0, false
	}

	cost, note := computeVariantCost("test-model", 1_000_000, 500_000)
	if note != "" {
		t.Fatalf("expected empty note, got %q", note)
	}
	want := 3.00 + 0.5*15.00 // 10.50
	if math.Abs(cost-want) > 1e-9 {
		t.Fatalf("cost: got %.6f, want %.6f", cost, want)
	}
}

func TestComputeVariantCost_UnknownModelReturnsNote(t *testing.T) {
	old := modelPricer
	defer func() { modelPricer = old }()
	modelPricer = func(id string) (float64, float64, bool) { return 0, 0, false }

	cost, note := computeVariantCost("custom-unknown", 1000, 1000)
	if cost != 0 {
		t.Fatalf("expected zero cost for unknown model, got %v", cost)
	}
	if note != "no pricing data" {
		t.Fatalf("expected note, got %q", note)
	}
}

func TestComputeVariantCost_FractionalTokens(t *testing.T) {
	old := modelPricer
	defer func() { modelPricer = old }()
	modelPricer = func(id string) (float64, float64, bool) {
		return 2.00, 8.00, true
	}
	cost, _ := computeVariantCost("x", 250_000, 100_000)
	want := (250_000.0/1e6)*2.00 + (100_000.0/1e6)*8.00
	if math.Abs(cost-want) > 1e-9 {
		t.Fatalf("cost: got %.6f want %.6f", cost, want)
	}
}
