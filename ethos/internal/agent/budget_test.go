package agent

import (
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tokenizer"
)

func TestBudgetEstimator_BasicEstimate(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 4096)

	history := []providers.Message{
		{Role: "user", Content: "Hello, how are you?"},
		{Role: "assistant", Content: "I'm doing well, thanks for asking!"},
	}
	systemPrompt := "You are a helpful assistant."
	toolDefs := []providers.Tool{
		{Name: "search", Description: "Search the web"},
		{Name: "calculator", Description: "Perform calculations"},
	}

	report := be.Estimate(history, systemPrompt, toolDefs)

	if report.SystemPromptTokens <= 0 {
		t.Errorf("SystemPromptTokens = %d, want > 0", report.SystemPromptTokens)
	}
	if report.HistoryTokens <= 0 {
		t.Errorf("HistoryTokens = %d, want > 0", report.HistoryTokens)
	}
	if report.ToolDefTokens <= 0 {
		t.Errorf("ToolDefTokens = %d, want > 0", report.ToolDefTokens)
	}
	if report.EstimatedResponse != 1024 {
		t.Errorf("EstimatedResponse = %d, want 1024", report.EstimatedResponse)
	}
	if report.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", report.MaxTokens)
	}

	expectedTotal := report.SystemPromptTokens + report.HistoryTokens + report.ToolDefTokens + report.EstimatedResponse
	if report.TotalEstimate != expectedTotal {
		t.Errorf("TotalEstimate = %d, want %d", report.TotalEstimate, expectedTotal)
	}

	expectedUtilization := float64(expectedTotal) / 4096.0
	if report.Utilization != expectedUtilization {
		t.Errorf("Utilization = %f, want %f", report.Utilization, expectedUtilization)
	}
}

func TestBudgetEstimator_HistoryOverhead(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 4096)

	content := "hello world"
	contentTokens := est.Estimate(content)

	singleMsg := []providers.Message{{Role: "user", Content: content}}
	report := be.Estimate(singleMsg, "", nil)

	if report.HistoryTokens != contentTokens+4 {
		t.Errorf("HistoryTokens = %d, want %d (content %d + 4 overhead)", report.HistoryTokens, contentTokens+4, contentTokens)
	}
}

func TestBudgetEstimator_ToolDefOverhead(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 4096)

	tool := providers.Tool{Name: "search", Description: "Search the web"}
	combined := tool.Name + tool.Description
	combinedTokens := est.Estimate(combined)

	toolDefs := []providers.Tool{tool}
	report := be.Estimate(nil, "", toolDefs)

	if report.ToolDefTokens != combinedTokens+10 {
		t.Errorf("ToolDefTokens = %d, want %d (combined %d + 10 overhead)", report.ToolDefTokens, combinedTokens+10, combinedTokens)
	}
}

func TestBudgetEstimator_EmptyInputs(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 4096)

	report := be.Estimate(nil, "", nil)

	if report.SystemPromptTokens != 0 {
		t.Errorf("SystemPromptTokens = %d, want 0 for empty string", report.SystemPromptTokens)
	}
	if report.HistoryTokens != 0 {
		t.Errorf("HistoryTokens = %d, want 0 for nil history", report.HistoryTokens)
	}
	if report.ToolDefTokens != 0 {
		t.Errorf("ToolDefTokens = %d, want 0 for nil tool defs", report.ToolDefTokens)
	}
	if report.TotalEstimate != 1024 {
		t.Errorf("TotalEstimate = %d, want 1024 (response only)", report.TotalEstimate)
	}
}

func TestBudgetEstimator_ShouldCompactBelowThreshold(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 100000)

	report := be.Estimate(nil, "hi", nil)

	if report.ShouldCompact {
		t.Error("ShouldCompact = true for low utilization, want false")
	}
	if report.ShouldWarn {
		t.Error("ShouldWarn = true for low utilization, want false")
	}
}

func TestBudgetEstimator_ShouldWarn(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 5000)

	var history []providers.Message
	for i := 0; i < 149; i++ {
		history = append(history, providers.Message{
			Role:    "user",
			Content: "message to drive utilization into warning zone but not critical",
		})
	}

	report := be.Estimate(history, "system", nil)

	if !report.ShouldWarn {
		t.Errorf("ShouldWarn = false with utilization %.4f, want true (>= 0.8)", report.Utilization)
	}
}

func TestBudgetEstimator_ShouldCompact(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 100)

	var history []providers.Message
	for i := 0; i < 50; i++ {
		history = append(history, providers.Message{
			Role:    "user",
			Content: "This is a test message that consumes tokens.",
		})
	}

	report := be.Estimate(history, "You are a helpful assistant.", nil)

	if !report.ShouldCompact {
		t.Errorf("ShouldCompact = false with utilization %.2f, want true (>= 0.5)", report.Utilization)
	}
}

func TestBudgetEstimator_ZeroMaxTokens(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 0)

	report := be.Estimate([]providers.Message{{Role: "user", Content: "hello"}}, "system", nil)

	if report.Utilization != 0.0 {
		t.Errorf("Utilization = %f, want 0.0 for zero maxTokens", report.Utilization)
	}
	if report.ShouldCompact {
		t.Error("ShouldCompact should be false when maxTokens is 0")
	}
}

func TestBudgetEstimator_CheckAndWarnCritical(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 100)

	var history []providers.Message
	for i := 0; i < 100; i++ {
		history = append(history, providers.Message{
			Role:    "user",
			Content: "padding message content to drive utilization over the hard threshold",
		})
	}

	report := be.Estimate(history, "system prompt", nil)
	warning := be.CheckAndWarn(report)

	if warning == "" {
		t.Error("CheckAndWarn returned empty string for critical utilization")
	}
	if report.Utilization < be.hardThreshold {
		t.Errorf("expected utilization >= %.2f, got %.2f", be.hardThreshold, report.Utilization)
	}
}

func TestBudgetEstimator_CheckAndWarnWarning(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 5000)

	var history []providers.Message
	for i := 0; i < 149; i++ {
		history = append(history, providers.Message{
			Role:    "user",
			Content: "message to drive utilization into warning zone but not critical",
		})
	}

	report := be.Estimate(history, "system", nil)

	if !report.ShouldWarn {
		t.Errorf("expected ShouldWarn=true, got utilization=%.4f", report.Utilization)
	}
	if report.Utilization >= be.hardThreshold {
		t.Errorf("utilization %.4f should be below hard threshold %.2f for this test", report.Utilization, be.hardThreshold)
	}

	warning := be.CheckAndWarn(report)
	if warning == "" {
		t.Error("CheckAndWarn should return warning message for ShouldWarn=true")
	}
}

func TestBudgetEstimator_CheckAndWarnNoWarning(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 100000)

	report := be.Estimate(nil, "hi", nil)
	warning := be.CheckAndWarn(report)

	if warning != "" {
		t.Errorf("CheckAndWarn = %q, want empty string for low utilization", warning)
	}
}

func TestBudgetEstimator_MultipleMessagesAndTools(t *testing.T) {
	est := tokenizer.NewEstimator()
	be := NewBudgetEstimator(est, 8192)

	history := []providers.Message{
		{Role: "user", Content: "What is the weather?"},
		{Role: "assistant", Content: "Let me check the weather for you."},
		{Role: "tool", Content: `{"temp": 72, "condition": "sunny"}`},
		{Role: "assistant", Content: "It's 72 degrees and sunny."},
		{Role: "user", Content: "What about tomorrow?"},
	}
	systemPrompt := "You are a weather assistant with access to weather tools."
	toolDefs := []providers.Tool{
		{Name: "get_weather", Description: "Get current weather for a location"},
		{Name: "get_forecast", Description: "Get weather forecast for upcoming days"},
	}

	report := be.Estimate(history, systemPrompt, toolDefs)

	if report.SystemPromptTokens <= 0 {
		t.Error("expected positive system prompt tokens")
	}
	if report.HistoryTokens <= 0 {
		t.Error("expected positive history tokens")
	}
	if report.ToolDefTokens <= 0 {
		t.Error("expected positive tool def tokens")
	}

	expectedHistory := 0
	for _, msg := range history {
		expectedHistory += est.Estimate(msg.Content) + 4
	}
	if report.HistoryTokens != expectedHistory {
		t.Errorf("HistoryTokens = %d, want %d", report.HistoryTokens, expectedHistory)
	}

	expectedTools := 0
	for _, tool := range toolDefs {
		expectedTools += est.Estimate(tool.Name+tool.Description) + 10
	}
	if report.ToolDefTokens != expectedTools {
		t.Errorf("ToolDefTokens = %d, want %d", report.ToolDefTokens, expectedTools)
	}
}

func TestAgent_BudgetReport(t *testing.T) {
	est := tokenizer.NewEstimator()
	agent := &Agent{
		tokenizer:       est,
		budgetEstimator: NewBudgetEstimator(est, 4096),
		systemPrompt:    "You are a test assistant.",
		maxTokens:       4096,
		history: []providers.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	}

	report := agent.BudgetReport()
	if report == nil {
		t.Fatal("BudgetReport() returned nil")
	}
	if report.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", report.MaxTokens)
	}
	if report.TotalEstimate <= 0 {
		t.Errorf("TotalEstimate = %d, want > 0", report.TotalEstimate)
	}
}

func TestAgent_BudgetReportNilEstimator(t *testing.T) {
	agent := &Agent{
		budgetEstimator: nil,
	}

	report := agent.BudgetReport()
	if report != nil {
		t.Error("BudgetReport() should return nil when budgetEstimator is nil")
	}
}
