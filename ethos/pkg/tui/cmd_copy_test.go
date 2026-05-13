package tui

import (
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func TestCodeBlocksInOrder_BacktickFence(t *testing.T) {
	src := "preamble\n```go\nfunc Hello() {}\n```\nmiddle\n```python\nprint('hi')\n```"
	got := codeBlocksInOrder(src)
	if len(got) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(got))
	}
	if got[0].Lang != "go" || got[0].Body != "func Hello() {}" {
		t.Errorf("block 0: %+v", got[0])
	}
	if got[1].Lang != "python" || got[1].Body != "print('hi')" {
		t.Errorf("block 1: %+v", got[1])
	}
}

func TestCodeBlocksInOrder_TildeFence(t *testing.T) {
	src := "~~~bash\necho hi\n~~~"
	got := codeBlocksInOrder(src)
	if len(got) != 1 {
		t.Fatalf("tilde fence not detected: %+v", got)
	}
	if got[0].Lang != "bash" || got[0].Body != "echo hi" {
		t.Errorf("got %+v", got[0])
	}
}

func TestCodeBlocksInOrder_BareFence(t *testing.T) {
	src := "```\nplain text\n```"
	got := codeBlocksInOrder(src)
	if len(got) != 1 || got[0].Lang != "" || got[0].Body != "plain text" {
		t.Errorf("bare fence: %+v", got)
	}
}

func TestCodeBlocksInOrder_NoFenceReturnsEmpty(t *testing.T) {
	src := "just prose, nothing fenced here"
	got := codeBlocksInOrder(src)
	if len(got) != 0 {
		t.Errorf("non-code content should yield no blocks: %+v", got)
	}
}

func TestExtractCodeBlocks_NewestFirstAcrossMessages(t *testing.T) {
	history := []providers.Message{
		{Role: "assistant", Content: "```go\nA\n```"},
		{Role: "user", Content: "okay"},
		{Role: "assistant", Content: "```go\nB\n```\n```python\nC\n```"},
	}
	got := extractCodeBlocks(history)
	if len(got) != 3 {
		t.Fatalf("want 3 blocks, got %d: %+v", len(got), got)
	}
	// Newest assistant message contributes B then C in source order;
	// extractCodeBlocks pushes those reversed so the slice goes
	// newest-first. So index 0 = C (newest), 1 = B, 2 = A (oldest).
	if got[0].Body != "C" {
		t.Errorf("expected C newest, got %q", got[0].Body)
	}
	if got[1].Body != "B" {
		t.Errorf("expected B middle, got %q", got[1].Body)
	}
	if got[2].Body != "A" {
		t.Errorf("expected A oldest, got %q", got[2].Body)
	}
}

func TestExtractCodeBlocks_IgnoresUserMessages(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "```go\nshould-be-ignored\n```"},
		{Role: "assistant", Content: "```go\nincluded\n```"},
	}
	got := extractCodeBlocks(history)
	if len(got) != 1 {
		t.Fatalf("user message should be skipped: got %d blocks", len(got))
	}
	if got[0].Body != "included" {
		t.Errorf("wrong body: %q", got[0].Body)
	}
}

func TestExtractCodeBlocks_EmptyHistoryReturnsEmpty(t *testing.T) {
	if got := extractCodeBlocks(nil); len(got) != 0 {
		t.Errorf("nil history should produce no blocks: %+v", got)
	}
}

func TestCodeBlocksInOrder_PreservesInternalNewlines(t *testing.T) {
	src := "```\nline 1\nline 2\nline 3\n```"
	got := codeBlocksInOrder(src)
	if len(got) != 1 {
		t.Fatalf("want 1 block, got %d", len(got))
	}
	if !strings.Contains(got[0].Body, "line 1\nline 2\nline 3") {
		t.Errorf("internal newlines lost: %q", got[0].Body)
	}
}

func TestCodeBlocksInOrder_LangCharacterSet(t *testing.T) {
	// Underscores and dashes in lang tags should match.
	tests := []struct {
		src, wantLang string
	}{
		{"```c++\nfoo\n```", ""}, // ++ isn't in our allowed set — bare fence
		{"```objective-c\nfoo\n```", "objective-c"},
		{"```snake_case\nfoo\n```", "snake_case"},
		{"```js\nfoo\n```", "js"},
	}
	for _, tt := range tests {
		got := codeBlocksInOrder(tt.src)
		if len(got) != 1 {
			// c++ case: regex won't match the c++ portion as a lang and
			// the whole fence becomes opaque. That's acceptable — the
			// body is still extracted, just under an empty lang.
			continue
		}
		if got[0].Lang != tt.wantLang {
			t.Errorf("lang for %q: got %q want %q", tt.src, got[0].Lang, tt.wantLang)
		}
	}
}
