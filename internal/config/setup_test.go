package config

import (
	"strings"
	"testing"
)

func TestNewSetupWizard_CreatesWizard(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	if sw == nil {
		t.Fatal("expected non-nil wizard")
	}
	steps := sw.Steps()
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(steps))
	}
	if steps[0].ID != "provider" {
		t.Errorf("expected first step ID 'provider', got %s", steps[0].ID)
	}
}

func TestAvailableProviders_ReturnsAll(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	providers := sw.AvailableProviders()
	if len(providers) != 18 {
		t.Fatalf("expected 18 providers, got %d", len(providers))
	}
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = p.Name
	}
	t.Logf("providers: %v", names)
}

func TestProviderSteps_OpenAI(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	steps := sw.ProviderSteps("openai")
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(steps))
	}

	baseURLStep := steps[2]
	if baseURLStep.Default != "https://api.openai.com/v1" {
		t.Errorf("expected default base URL 'https://api.openai.com/v1', got %s", baseURLStep.Default)
	}

	modelStep := steps[3]
	if len(modelStep.Options) != 4 {
		t.Errorf("expected 4 model options, got %d", len(modelStep.Options))
	}
	if modelStep.Default != "gpt-4o" {
		t.Errorf("expected default model 'gpt-4o', got %s", modelStep.Default)
	}
}

func TestProviderSteps_Ollama(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	steps := sw.ProviderSteps("ollama")
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(steps))
	}

	apiKeyStep := steps[1]
	if err := apiKeyStep.Validate(""); err != nil {
		t.Errorf("ollama should allow empty API key, got error: %v", err)
	}
}

func TestProviderSteps_Unknown(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	steps := sw.ProviderSteps("unknown")
	if steps != nil {
		t.Error("expected nil for unknown provider")
	}
}

func TestValidateStep_ValidProvider(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	if err := sw.ValidateStep("provider", "openai"); err != nil {
		t.Errorf("expected openai to be valid, got: %v", err)
	}
	if err := sw.ValidateStep("provider", "ollama"); err != nil {
		t.Errorf("expected ollama to be valid, got: %v", err)
	}
}

func TestValidateStep_InvalidProvider(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	err := sw.ValidateStep("provider", "unknown")
	if err == nil {
		t.Error("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("expected 'unknown provider' in error, got: %v", err)
	}
}

func TestValidateStep_EmptyAPIKey(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	sw.selected = "openai"
	err := sw.ValidateStep("api_key", "")
	if err == nil {
		t.Error("expected error for empty API key with non-ollama provider")
	}
}

func TestValidateStep_EmptyAPIKey_Ollama(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	sw.selected = "ollama"
	err := sw.ValidateStep("api_key", "")
	if err != nil {
		t.Errorf("ollama should allow empty API key, got: %v", err)
	}
}

func TestValidateStep_InvalidURL(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	err := sw.ValidateStep("base_url", "not-a-url")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
	if !strings.Contains(err.Error(), "http://") && !strings.Contains(err.Error(), "https://") {
		t.Errorf("expected url prefix hint in error, got: %v", err)
	}
}

func TestValidateStep_ValidURL(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	if err := sw.ValidateStep("base_url", "https://api.example.com"); err != nil {
		t.Errorf("expected valid URL, got: %v", err)
	}
	if err := sw.ValidateStep("base_url", "http://localhost:8080"); err != nil {
		t.Errorf("expected valid URL, got: %v", err)
	}
}

func TestValidateStep_EmptyModel(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	err := sw.ValidateStep("model", "")
	if err == nil {
		t.Error("expected error for empty model")
	}
}

func TestValidateStep_ValidModel(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	if err := sw.ValidateStep("model", "gpt-4o"); err != nil {
		t.Errorf("expected valid model, got: %v", err)
	}
}

func TestValidateStep_UnknownStep(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	err := sw.ValidateStep("nonexistent", "value")
	if err == nil {
		t.Error("expected error for unknown step")
	}
}

func TestApplyStep_SetsProvider(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	sw.ApplyStep("provider", "openai")

	if cfg.Agent.DefaultProvider != "openai" {
		t.Errorf("expected default provider 'openai', got %s", cfg.Agent.DefaultProvider)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "openai" {
		t.Errorf("expected provider name 'openai', got %s", cfg.Providers[0].Name)
	}
	if cfg.Providers[0].BaseURL != "https://api.openai.com/v1" {
		t.Errorf("expected default base URL, got %s", cfg.Providers[0].BaseURL)
	}
}

func TestApplyStep_SetsProvider_NoDuplicate(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	sw.ApplyStep("provider", "openai")
	sw.ApplyStep("provider", "openai")
	if len(cfg.Providers) != 1 {
		t.Errorf("expected 1 provider after duplicate apply, got %d", len(cfg.Providers))
	}
}

func TestApplyStep_SetsAPIKey(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	sw.ApplyStep("provider", "openai")
	sw.ApplyStep("api_key", "sk-test-key-123")

	if cfg.Providers[0].APIKey != "sk-test-key-123" {
		t.Errorf("expected API key 'sk-test-key-123', got %s", cfg.Providers[0].APIKey)
	}
}

func TestApplyStep_SetsBaseURL(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	sw.ApplyStep("provider", "openai")
	sw.ApplyStep("base_url", "https://custom.api.com/v1")

	if cfg.Providers[0].BaseURL != "https://custom.api.com/v1" {
		t.Errorf("expected base URL 'https://custom.api.com/v1', got %s", cfg.Providers[0].BaseURL)
	}
}

func TestApplyStep_SetsModel(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)
	sw.ApplyStep("provider", "openai")
	sw.ApplyStep("model", "gpt-4o")

	if cfg.Agent.DefaultModel != "gpt-4o" {
		t.Errorf("expected default model 'gpt-4o', got %s", cfg.Agent.DefaultModel)
	}
}

func TestApplyStep_FullFlow_Anthropic(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)

	sw.ApplyStep("provider", "anthropic")
	sw.ApplyStep("api_key", "sk-ant-test")
	sw.ApplyStep("base_url", "https://api.anthropic.com")
	sw.ApplyStep("model", "claude-sonnet-4-20250514")

	if cfg.Agent.DefaultProvider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %s", cfg.Agent.DefaultProvider)
	}
	if cfg.Agent.DefaultModel != "claude-sonnet-4-20250514" {
		t.Errorf("expected model 'claude-sonnet-4-20250514', got %s", cfg.Agent.DefaultModel)
	}
	if cfg.Providers[0].APIKey != "sk-ant-test" {
		t.Errorf("expected API key set, got %s", cfg.Providers[0].APIKey)
	}
	if len(cfg.Providers[0].Models) != 3 {
		t.Errorf("expected 3 model configs, got %d", len(cfg.Providers[0].Models))
	}
}

func TestApplyStep_FullFlow_Ollama(t *testing.T) {
	cfg := &Config{}
	sw := NewSetupWizard(cfg)

	steps := sw.ProviderSteps("ollama")

	sw.ApplyStep("provider", "ollama")
	sw.ApplyStep("api_key", "")
	sw.ApplyStep("base_url", steps[2].Default)
	sw.ApplyStep("model", "llama3.1:8b")

	if cfg.Providers[0].BaseURL != "http://localhost:11434" {
		t.Errorf("expected ollama base URL, got %s", cfg.Providers[0].BaseURL)
	}
	if cfg.Agent.DefaultModel != "llama3.1:8b" {
		t.Errorf("expected model 'llama3.1:8b', got %s", cfg.Agent.DefaultModel)
	}
}
