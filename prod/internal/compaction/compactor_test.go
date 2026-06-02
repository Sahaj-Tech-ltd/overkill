package compaction

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

type mockProvider struct {
	responses []providers.Response
	callCount int
	failAll   bool
}

func (m *mockProvider) Complete(_ context.Context, _ providers.Request) (providers.Response, error) {
	if m.failAll {
		return providers.Response{}, errors.New("provider unavailable")
	}
	if m.callCount >= len(m.responses) {
		return providers.Response{Content: "default summary"}, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return resp, nil
}

func (m *mockProvider) Stream(_ context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
	return nil, nil
}

func (m *mockProvider) Models() []providers.Model { return nil }
func (m *mockProvider) Name() string              { return "mock" }

func makeMessages(n int, content string) []providers.Message {
	msgs := make([]providers.Message, n)
	for i := range n {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = providers.Message{Role: role, Content: content}
	}
	return msgs
}

func defaultOpts() CompactOptions {
	return CompactOptions{
		MaxTokens:       1000,
		PreserveLast:    20,
		Model:           "gpt-4",
		CompactionModel: "gpt-4o-mini",
		SoftThreshold:   0.5,
		HardThreshold:   0.95,
	}
}

func TestShouldCompact_BelowThreshold(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	msgs := makeMessages(5, "hi")
	opts := defaultOpts()
	opts.MaxTokens = 100000

	should, pct := c.ShouldCompact(msgs, opts)
	if should {
		t.Error("should not compact below threshold")
	}
	if pct >= opts.SoftThreshold {
		t.Errorf("usage percent %.2f should be below soft threshold %.2f", pct, opts.SoftThreshold)
	}
}

func TestShouldCompact_AboveSoftThreshold(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	content := strings.Repeat("word ", 200)
	msgs := makeMessages(10, content)
	opts := defaultOpts()
	opts.MaxTokens = 100

	should, pct := c.ShouldCompact(msgs, opts)
	if !should {
		t.Error("should compact above soft threshold")
	}
	if pct < opts.SoftThreshold {
		t.Errorf("usage percent %.2f should be above soft threshold %.2f", pct, opts.SoftThreshold)
	}
}

func TestShouldCompact_AboveHardThreshold(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	content := strings.Repeat("word ", 500)
	msgs := makeMessages(10, content)
	opts := defaultOpts()
	opts.MaxTokens = 50

	should, pct := c.ShouldCompact(msgs, opts)
	if !should {
		t.Error("should compact above hard threshold")
	}
	if pct < opts.HardThreshold {
		t.Errorf("usage percent %.2f should be above hard threshold %.2f", pct, opts.HardThreshold)
	}
}

func TestCompact_Level1Success(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{
		responses: []providers.Response{
			{Content: "This is a concise summary of the conversation."},
		},
	}
	c := NewLCMCompactor(mp, est)

	msgs := makeMessages(30, "some content here for testing")
	opts := defaultOpts()
	opts.MaxTokens = 5000

	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Level != Level1Detailed {
		t.Errorf("expected Level1Detailed, got %v", result.Level)
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if result.MessagesCompacted != 10 {
		t.Errorf("expected 10 messages compacted, got %d", result.MessagesCompacted)
	}
	if result.MessagesPreserved != 20 {
		t.Errorf("expected 20 messages preserved, got %d", result.MessagesPreserved)
	}
}

func TestCompact_Level2Escalation(t *testing.T) {
	est := tokenizer.NewEstimator()
	bigSummary := strings.Repeat("detailed summary text that is very long ", 200)

	mp := &mockProvider{
		responses: []providers.Response{
			{Content: bigSummary},
			{Content: "Concise bullet-point summary."},
		},
	}
	c := NewLCMCompactor(mp, est)

	msgs := makeMessages(30, "some content here")
	opts := defaultOpts()
	opts.MaxTokens = 100

	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Level != Level2Aggressive {
		t.Errorf("expected Level2Aggressive, got %v", result.Level)
	}
	if mp.callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", mp.callCount)
	}
}

func TestCompact_Level3Fallback(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{
		failAll: true,
	}
	c := NewLCMCompactor(mp, est)

	msgs := makeMessages(30, "some content here for the test")
	opts := defaultOpts()
	opts.MaxTokens = 100

	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Level != Level3Truncate {
		t.Errorf("expected Level3Truncate, got %v", result.Level)
	}
	if result.Summary == "" {
		t.Error("level 3 must always produce a summary")
	}
	if !strings.Contains(result.Summary, "[Context summary truncated.") {
		t.Errorf("level 3 summary should contain truncation marker, got: %s", result.Summary)
	}
}

func TestCompact_PreserveLastMessages(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{
		responses: []providers.Response{
			{Content: "Summary."},
		},
	}
	c := NewLCMCompactor(mp, est)

	msgs := makeMessages(25, "content")
	opts := defaultOpts()
	opts.PreserveLast = 20

	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.MessagesCompacted != 5 {
		t.Errorf("expected 5 messages compacted, got %d", result.MessagesCompacted)
	}
	if result.MessagesPreserved != 20 {
		t.Errorf("expected 20 messages preserved, got %d", result.MessagesPreserved)
	}
}

func TestCompact_EmptyMessages(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	result, err := c.Compact(context.Background(), nil, defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for empty messages")
	}
}

func TestCompact_NoMessagesToCompact(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	msgs := makeMessages(10, "content")
	opts := defaultOpts()
	opts.PreserveLast = 20

	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result when all messages are in preserve window")
	}
}

func TestCompact_ContextCancelled(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	msgs := makeMessages(30, "content")
	_, err := c.Compact(ctx, msgs, defaultOpts())
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !strings.Contains(err.Error(), "compaction:") {
		t.Errorf("error should be wrapped with compaction prefix: %v", err)
	}
}

func TestEstimateUsage(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	msgs := makeMessages(5, "hello world this is a test")
	count := c.EstimateUsage(msgs, "gpt-4")
	if count <= 0 {
		t.Errorf("expected positive token count, got %d", count)
	}
}

func TestCompact_SingleMessage(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{
		responses: []providers.Response{
			{Content: "Summary of single message."},
		},
	}
	c := NewLCMCompactor(mp, est)

	msgs := []providers.Message{{Role: "user", Content: "Hello world"}}
	opts := defaultOpts()
	opts.PreserveLast = 0

	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.MessagesCompacted != 1 {
		t.Errorf("expected 1 message compacted, got %d", result.MessagesCompacted)
	}
}

func TestCompact_ToolResults(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{
		responses: []providers.Response{
			{Content: "Summary with tool results."},
		},
	}
	c := NewLCMCompactor(mp, est)

	msgs := []providers.Message{
		{Role: "user", Content: "list files"},
		{Role: "assistant", Content: "", ToolCalls: []providers.ToolCall{
			{ID: "tc1", Name: "shell", Arguments: `{"command":"ls"}`},
		}},
		{Role: "tool", Content: "file1.go\nfile2.go", ToolCallID: "tc1"},
		{Role: "assistant", Content: "Here are the files."},
	}
	opts := defaultOpts()
	opts.PreserveLast = 0
	opts.MaxTokens = 5000

	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Level != Level1Detailed {
		t.Errorf("expected Level1Detailed, got %v", result.Level)
	}
}

func TestCompact_Ratio(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{
		responses: []providers.Response{
			{Content: "Short summary."},
		},
	}
	c := NewLCMCompactor(mp, est)

	content := strings.Repeat("this is a longer message with more tokens ", 50)
	msgs := makeMessages(30, content)
	opts := defaultOpts()
	opts.MaxTokens = 50000

	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Ratio < 0 || result.Ratio > 1 {
		t.Errorf("ratio %.3f should be between 0 and 1", result.Ratio)
	}
	if result.OriginalTokens <= result.CompactedTokens {
		t.Errorf("compacted tokens (%d) should be less than original (%d)", result.CompactedTokens, result.OriginalTokens)
	}
}

func TestBuildSummaryPrompt_Detailed(t *testing.T) {
	msgs := []providers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "tool", Content: "result data", ToolCallID: "tc1"},
	}
	prompt := buildDetailedSummaryPrompt(msgs, 500)

	if !strings.Contains(prompt, "User: Hello") {
		t.Error("detailed prompt should contain user message")
	}
	if !strings.Contains(prompt, "Assistant: Hi there") {
		t.Error("detailed prompt should contain assistant message")
	}
	if !strings.Contains(prompt, "Tool (tc1): result data") {
		t.Error("detailed prompt should contain tool result")
	}
	if !strings.Contains(prompt, "500") {
		t.Error("detailed prompt should mention target token count")
	}
}

func TestBuildSummaryPrompt_Aggressive(t *testing.T) {
	msgs := []providers.Message{
		{Role: "user", Content: "Build a feature"},
		{Role: "assistant", Content: "Done", ToolCalls: []providers.ToolCall{
			{ID: "tc1", Name: "shell", Arguments: `{"cmd":"go build"}`},
		}},
	}
	prompt := buildAggressiveSummaryPrompt(msgs, 1000)

	if !strings.Contains(prompt, "User: Build a feature") {
		t.Error("aggressive prompt should contain user message")
	}
	if !strings.Contains(prompt, "Called tool: shell") {
		t.Error("aggressive prompt should reference tool call")
	}
	if !strings.Contains(prompt, "bullet") {
		t.Error("aggressive prompt should mention bullet points")
	}
}

func TestTruncateMessage(t *testing.T) {
	short := "hello"
	if got := truncateMessage(short, 10); got != short {
		t.Errorf("short message should not be truncated")
	}

	long := strings.Repeat("a", 3000)
	got := truncateMessage(long, maxMessageChars)
	if !strings.HasSuffix(got, "\n[...truncated...]") {
		t.Error("long message should be truncated with marker")
	}
	if len(got) > maxMessageChars+50 {
		t.Errorf("truncated message too long: %d", len(got))
	}
}

// TestCompact_EmptyLLMResponse proves bug #46:
// When the LLM returns an empty response (no content), the compaction
// silently treats this as success and stores the empty summary,
// causing silent context loss. It should fall through to truncation.
func TestCompact_EmptyLLMResponse(t *testing.T) {
	est := tokenizer.NewEstimator()
	// Mock returns empty content — simulating LLM returning nothing.
	mp := &mockProvider{
		responses: []providers.Response{
			{Content: ""}, // Level 1: empty response
			{Content: ""}, // Level 2: empty response (fallback also empty)
		},
	}
	c := NewLCMCompactor(mp, est)

	msgs := makeMessages(30, "important context that should not be lost")
	opts := defaultOpts()
	opts.MaxTokens = 100

	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// BUG #46: Empty LLM response should NOT be treated as success.
	// The compaction should fall through to Level 3 (truncation) and
	// produce a non-empty summary. If Level is Level1Detailed or
	// Level2Aggressive with an empty Summary, context was silently lost.
	if result.Summary == "" {
		t.Errorf("BUG #46: empty summary — context was silently lost")
	}
	if result.Level != Level3Truncate {
		t.Errorf("BUG #46: empty LLM response treated as successful at level %v, want Level3Truncate fallback", result.Level)
	}
}
