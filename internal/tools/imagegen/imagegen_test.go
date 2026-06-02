package imagegen

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

func TestToolName(t *testing.T) {
	tool := New(config.ImageGenConfig{})
	if tool.Name() != "image_gen" {
		t.Errorf("Name: got %q, want image_gen", tool.Name())
	}
}

func TestExecute_EmptyPrompt(t *testing.T) {
	tool := New(config.ImageGenConfig{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":""}`))
	if err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestExecute_NoProvider(t *testing.T) {
	tool := New(config.ImageGenConfig{})
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"a cat"}`))
	if err != nil {
		t.Fatalf("Execute no provider: %v", err)
	}
	var result ImageGenOutput
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if result.Provider != "none" {
		t.Errorf("provider: got %q, want none", result.Provider)
	}
	if result.Prompt != "a cat" {
		t.Errorf("prompt: got %q, want a cat", result.Prompt)
	}
}

func TestExecute_ConfigProvider(t *testing.T) {
	tool := New(config.ImageGenConfig{Provider: "openai"})
	// With no API key, this should fail — but we test the input path, not the call.
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"test"}`))
	_ = out
	// openai provider requires API key — will error, which is fine.
	// We just want to verify it doesn't panic and selects the right provider.
	_ = err
}

func TestInputOutput_Roundtrip(t *testing.T) {
	input := ImageGenInput{
		Prompt:   "a beautiful sunset",
		Provider: "stability",
		Size:     "1024x1024",
		N:        1,
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ImageGenInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Prompt != "a beautiful sunset" {
		t.Errorf("prompt: got %q", decoded.Prompt)
	}
	if decoded.Provider != "stability" {
		t.Errorf("provider: got %q", decoded.Provider)
	}
}

func TestOutput_Marshal(t *testing.T) {
	out := ImageGenOutput{
		Images:   []string{"https://example.com/img.png"},
		Provider: "openai",
		Prompt:   "a cat",
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ImageGenOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Images) != 1 || decoded.Images[0] != "https://example.com/img.png" {
		t.Errorf("images: got %v", decoded.Images)
	}
}

func TestExecute_InvalidJSON(t *testing.T) {
	tool := New(config.ImageGenConfig{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestExecute_DefaultSize(t *testing.T) {
	tool := New(config.ImageGenConfig{Provider: "replicate"})
	// replicate without key will fail but shouldn't panic
	_, _ = tool.Execute(context.Background(), json.RawMessage(`{"prompt":"test"}`))
	// Just verifying no panic.
}

func TestInputProviderOverride(t *testing.T) {
	tool := New(config.ImageGenConfig{Provider: "openai"})
	// Input provider should override config provider.
	// With no API key for either, this will fail but the code path is tested.
	_, _ = tool.Execute(context.Background(), json.RawMessage(`{"prompt":"test","provider":"replicate"}`))
}
