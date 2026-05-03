package agent

import (
	"context"
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tokenizer"
)

func TestAgent_Compact_ReplacesHistory(t *testing.T) {
	prov := &mockProvider{
		responses: []providers.Response{
			{Content: "summary of the conversation"},
		},
	}
	a := New(Config{
		Provider:  prov,
		Tokenizer: tokenizer.NewEstimator(),
		Model:     "test",
		MaxTokens: 1000,
	})
	a.SetHistory([]providers.Message{
		{Role: "user", Content: "do thing"},
		{Role: "assistant", Content: "did thing"},
		{Role: "user", Content: "do another"},
		{Role: "assistant", Content: "did another"},
	})

	res, err := a.Compact(context.Background())
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if res.Summary != "summary of the conversation" {
		t.Fatalf("unexpected summary: %q", res.Summary)
	}
	if res.MessagesBefore != 4 {
		t.Fatalf("expected 4 messages before, got %d", res.MessagesBefore)
	}
	if res.MessagesAfter != 1 {
		t.Fatalf("expected 1 message after, got %d", res.MessagesAfter)
	}
	hist := a.History()
	if len(hist) != 1 {
		t.Fatalf("expected 1 history msg, got %d", len(hist))
	}
}

func TestAgent_Compact_EmptyHistory(t *testing.T) {
	prov := &mockProvider{}
	a := New(Config{Provider: prov, Tokenizer: tokenizer.NewEstimator()})
	res, err := a.Compact(context.Background())
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if res.Summary != "" {
		t.Fatalf("expected empty summary, got %q", res.Summary)
	}
}

func TestAgent_ApprovalGate_AllowedTools(t *testing.T) {
	a := New(Config{Provider: &mockProvider{}})
	called := 0
	a.SetApprovalFunc(func(name, args, risk string) Approval {
		called++
		return Approval{Allow: true, Persist: true}
	})
	if !a.checkToolApproval("shell", "ls") {
		t.Fatal("expected first call to be allowed")
	}
	if !a.checkToolApproval("shell", "ls") {
		t.Fatal("expected persistent allowance to bypass callback")
	}
	if called != 1 {
		t.Fatalf("expected 1 callback invocation (persist cached), got %d", called)
	}
}
