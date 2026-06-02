package slack

import "testing"

func TestMarkdownToMrkdwn(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"bold", "this is **bold** text", "this is *bold* text"},
		{"italic_star", "an *italic* word", "an _italic_ word"},
		{"inline_code", "use `git status`", "use `git status`"},
		{"fenced_code", "```\nrun\n```", "```\nrun\n```"},
		{"heading_h1", "# Title\nbody", "*Title*\nbody"},
		{"heading_h2", "## Sub\nbody", "*Sub*\nbody"},
		{"link", "see [docs](https://example.com)", "see <https://example.com|docs>"},
		{"unterminated_bold", "x **y", "x **y"},
		{"bullet_list_unchanged", "* item\n* item", "* item\n* item"},
		{"mixed", "**bold** and `code` and *italic*", "*bold* and `code` and _italic_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MarkdownToMrkdwn(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestEscapeMrkdwn(t *testing.T) {
	got := EscapeMrkdwn("a&b<c>d")
	want := "a&amp;b&lt;c&gt;d"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatToolCall(t *testing.T) {
	out := FormatToolCall("shell", `{"cmd":"ls"}`)
	if out == "" || out[:9] != ":wrench: " {
		t.Fatalf("unexpected: %q", out)
	}
	// Truncation
	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'x'
	}
	out2 := FormatToolCall("noisy", string(long))
	if len(out2) >= len(long) {
		t.Fatalf("long args were not truncated: %d", len(out2))
	}
}
