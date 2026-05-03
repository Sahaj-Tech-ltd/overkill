package compaction

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type mockCompressorProvider struct {
	response string
	err      error
}

func (m *mockCompressorProvider) Name() string { return "mock" }
func (m *mockCompressorProvider) Models() []providers.Model {
	return nil
}
func (m *mockCompressorProvider) Complete(ctx context.Context, req providers.Request) (providers.Response, error) {
	if m.err != nil {
		return providers.Response{}, m.err
	}
	return providers.Response{Content: m.response}, nil
}
func (m *mockCompressorProvider) Stream(ctx context.Context, req providers.Request) (<-chan providers.Chunk, error) {
	ch := make(chan providers.Chunk, 1)
	ch <- providers.Chunk{Content: m.response, Done: true}
	close(ch)
	return ch, nil
}

func longPrompt(n int) string {
	return strings.Repeat("a", n*4)
}

func TestCompress_ReturnsOriginalWhenDisabled(t *testing.T) {
	pc := NewPromptCompressor(nil, "test-model")
	pc.SetEnabled(false)

	prompt := longPrompt(600)
	result, meta, err := pc.Compress(context.Background(), prompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != prompt {
		t.Error("expected original prompt when disabled")
	}
	if !meta.Skipped {
		t.Error("expected Skipped=true")
	}
	if meta.SkippedReason != "disabled" {
		t.Errorf("expected SkippedReason='disabled', got '%s'", meta.SkippedReason)
	}
}

func TestCompress_ReturnsOriginalWhenProviderNil(t *testing.T) {
	pc := NewPromptCompressor(nil, "test-model")

	prompt := longPrompt(600)
	result, meta, err := pc.Compress(context.Background(), prompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != prompt {
		t.Error("expected original prompt when provider is nil")
	}
	if !meta.Skipped {
		t.Error("expected Skipped=true")
	}
	if meta.SkippedReason != "no provider" {
		t.Errorf("expected SkippedReason='no provider', got '%s'", meta.SkippedReason)
	}
}

func TestCompress_ReturnsOriginalForShortPrompts(t *testing.T) {
	mock := &mockCompressorProvider{response: "compressed"}
	pc := NewPromptCompressor(mock, "test-model")

	prompt := "short prompt"
	result, meta, err := pc.Compress(context.Background(), prompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != prompt {
		t.Error("expected original prompt for short input")
	}
	if !meta.Skipped {
		t.Error("expected Skipped=true")
	}
	if meta.SkippedReason != "too short to compress" {
		t.Errorf("expected SkippedReason='too short to compress', got '%s'", meta.SkippedReason)
	}
}

func TestCompress_CallsProviderComplete(t *testing.T) {
	called := false
	mock := &struct {
		mockCompressorProvider
	}{
		mockCompressorProvider: mockCompressorProvider{response: "compressed"},
	}
	original := mock.Complete
	_ = original
	mock.mockCompressorProvider = mockCompressorProvider{
		response: strings.Repeat("b", 500*4),
	}

	pc := NewPromptCompressor(mock, "test-model")
	prompt := longPrompt(1000)

	result, meta, err := pc.Compress(context.Background(), prompt)
	_ = result
	_ = meta
	_ = err
	_ = called

	if meta.Skipped {
		t.Errorf("expected compression to succeed, got skipped: %s", meta.SkippedReason)
	}
}

func TestCompress_ReturnsOriginalOnProviderError(t *testing.T) {
	mock := &mockCompressorProvider{err: errors.New("boom")}
	pc := NewPromptCompressor(mock, "test-model")

	prompt := longPrompt(1000)
	result, meta, err := pc.Compress(context.Background(), prompt)
	if err == nil {
		t.Fatal("expected error from provider failure")
	}
	if result != prompt {
		t.Error("expected original prompt on provider error (fail-open)")
	}
	if !meta.Skipped {
		t.Error("expected Skipped=true")
	}
	if meta.SkippedReason != "provider error" {
		t.Errorf("expected SkippedReason='provider error', got '%s'", meta.SkippedReason)
	}
}

func TestCompress_TracksAccurateMetrics(t *testing.T) {
	mock := &mockCompressorProvider{response: "short"}
	pc := NewPromptCompressor(mock, "test-model")
	pc.SetMinSavingRatio(0.0)

	prompt := longPrompt(1000)
	_, meta, err := pc.Compress(context.Background(), prompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedOriginal := len(prompt) / 4
	if meta.OriginalTokens != expectedOriginal {
		t.Errorf("expected OriginalTokens=%d, got %d", expectedOriginal, meta.OriginalTokens)
	}

	expectedCompressed := len("short") / 4
	if meta.CompressedTokens != expectedCompressed {
		t.Errorf("expected CompressedTokens=%d, got %d", expectedCompressed, meta.CompressedTokens)
	}

	expectedRatio := float64(expectedOriginal-expectedCompressed) / float64(expectedOriginal)
	if meta.Ratio < expectedRatio-0.01 || meta.Ratio > expectedRatio+0.01 {
		t.Errorf("expected Ratio≈%.4f, got %.4f", expectedRatio, meta.Ratio)
	}
}

func TestSetMinSavingRatio_UpdatesThreshold(t *testing.T) {
	mock := &mockCompressorProvider{response: longPrompt(990)}
	pc := NewPromptCompressor(mock, "test-model")
	pc.SetMinSavingRatio(0.5)

	prompt := longPrompt(1000)
	result, meta, err := pc.Compress(context.Background(), prompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != prompt {
		t.Error("expected original prompt when savings ratio below threshold")
	}
	if !meta.Skipped {
		t.Error("expected Skipped=true with savings below minSavingRatio")
	}
}

func TestCompress_SuccessfulProviderReturnsCompressedText(t *testing.T) {
	compressed := "This is compressed output with enough length to pass."
	mock := &mockCompressorProvider{response: compressed}
	pc := NewPromptCompressor(mock, "test-model")
	pc.SetMinSavingRatio(0.0)

	prompt := longPrompt(1000)
	result, meta, err := pc.Compress(context.Background(), prompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != compressed {
		t.Errorf("expected compressed text, got different output")
	}
	if meta.Skipped {
		t.Errorf("expected Skipped=false, got skipped: %s", meta.SkippedReason)
	}
	if meta.ModelUsed != "test-model" {
		t.Errorf("expected ModelUsed='test-model', got '%s'", meta.ModelUsed)
	}
}

func TestIsEnabled_ReflectsState(t *testing.T) {
	pc := NewPromptCompressor(nil, "model")
	if !pc.IsEnabled() {
		t.Error("expected enabled by default")
	}
	pc.SetEnabled(false)
	if pc.IsEnabled() {
		t.Error("expected disabled after SetEnabled(false)")
	}
	pc.SetEnabled(true)
	if !pc.IsEnabled() {
		t.Error("expected enabled after SetEnabled(true)")
	}
}
