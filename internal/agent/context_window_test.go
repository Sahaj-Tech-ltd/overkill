package agent

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"testing"
)

func TestContextCapacityIsIndependentOfProviderOutputLimit(t *testing.T) {
	for _, capacity := range []int{128000, 0} {
		child := New(Config{Model: "test", MaxTokens: 4096, ContextWindow: capacity, Tokenizer: tokenizer.NewEstimator()})
		report := child.BudgetReport()
		expected := capacity
		if expected == 0 {
			expected = 4096
		}
		if report == nil || report.MaxTokens != expected {
			t.Fatalf("capacity=%d report=%+v", capacity, report)
		}
		request := child.buildRequest()
		if request.MaxTokens != 4096 {
			t.Fatalf("context capacity replaced provider output limit: %d", request.MaxTokens)
		}
		child.Shutdown()
	}
}
