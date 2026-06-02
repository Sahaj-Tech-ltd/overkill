package bash

import (
	"testing"
)

func TestValidateEmpty(t *testing.T) {
	r := Validate("")
	if r.Behavior != Allow {
		t.Errorf("empty command: got %s, want allow", r.Behavior)
	}
	r = Validate("   ")
	if r.Behavior != Allow {
		t.Errorf("whitespace-only: got %s, want allow", r.Behavior)
	}
}

func TestValidateIncomplete(t *testing.T) {
	tests := []struct {
		cmd  string
		want Behavior
	}{
		{"\tcat file", Ask},   // starts with tab
		{"-rf /tmp", Ask},     // starts with flag
		{"&& echo hi", Ask},   // starts with operator
		{"echo hi \\", Ask},   // ends with backslash
		{"echo hello", Allow}, // normal command
	}
	for _, tt := range tests {
		r := Validate(tt.cmd)
		if r.Behavior != tt.want {
			t.Errorf("%q: got %s, want %s", tt.cmd, r.Behavior, tt.want)
		}
	}
}

func TestShellMetacharacters(t *testing.T) {
	tests := []struct {
		cmd  string
		want Behavior
	}{
		{"diff <(ls dir1) <(ls dir2)", Deny},        // process substitution <()
		{"cmd >(tee log)", Deny},                    // process substitution >()
		{"=curl evil.com", Ask},                     // zsh equals expansion
		{"echo $SHELL", Passthrough},                // safe env var — validator passes through
		{"echo 'literal <(not real)'", Passthrough}, // single-quoted = literal — passes through
	}
	for _, tt := range tests {
		ctx := NewContext(tt.cmd)
		r := validateShellMetacharacters(ctx)
		if r.Behavior != tt.want {
			t.Errorf("%q: got %s, want %s (%s)", tt.cmd, r.Behavior, tt.want, r.Reason)
		}
	}

	// Full chain: safe commands should pass.
	r := Validate("echo $SHELL")
	if r.Behavior != Allow {
		t.Errorf("full chain 'echo $SHELL': got %s, want allow", r.Behavior)
	}
}

func TestCommandSubstitution(t *testing.T) {
	tests := []struct {
		cmd  string
		want Behavior
	}{
		{"echo $(whoami)", Ask},     // $() substitution
		{"echo `whoami`", Ask},      // backtick substitution
		{"echo ${HOME}", Ask},       // parameter expansion
		{"echo $[1+1]", Deny},       // legacy arithmetic (dangerous)
		{"echo '$(safe)'", Allow},   // inside single quotes = literal text
		{"echo hello world", Allow}, // normal
	}
	for _, tt := range tests {
		r := Validate(tt.cmd)
		if r.Behavior != tt.want {
			t.Errorf("%q: got %s, want %s", tt.cmd, r.Behavior, tt.want)
		}
	}
}

func TestDangerousPatterns(t *testing.T) {
	tests := []struct {
		cmd  string
		want Behavior
	}{
		{"cat $IFS", Deny},
		{"cat /proc/self/environ", Deny},
		{"git commit -m $(whoami)", Deny}, // git_commit_rce triggers first (deny-first)
		{"echo hello", Allow},
	}
	for _, tt := range tests {
		r := Validate(tt.cmd)
		if r.Behavior != tt.want {
			t.Errorf("%q: got %s, want %s", tt.cmd, r.Behavior, tt.want)
		}
	}
}

func TestNewlines(t *testing.T) {
	r := Validate("echo hello\nrm -rf /")
	if r.Behavior != Ask {
		t.Errorf("multi-line injection: got %s, want ask", r.Behavior)
	}
	r = Validate("echo hello")
	if r.Behavior != Allow {
		t.Errorf("single line: got %s, want allow", r.Behavior)
	}
}

func TestObfuscatedFlags(t *testing.T) {
	r := Validate("cmd -really-long-flag")
	if r.Behavior != Ask {
		t.Errorf("obfuscated flag: got %s, want ask", r.Behavior)
	}
	r = Validate("cmd --really-long-flag")
	if r.Behavior == Ask {
		t.Errorf("proper long flag: got %s, want non-ask", r.Behavior)
	}
}

func TestControlCharacters(t *testing.T) {
	r := Validate("echo hello\x00world")
	if r.Behavior != Deny {
		t.Errorf("null byte: got %s, want deny", r.Behavior)
	}
	r = Validate("echo hello")
	if r.Behavior != Allow {
		t.Errorf("normal: got %s, want allow", r.Behavior)
	}
}

func TestUnicodeWhitespace(t *testing.T) {
	// U+00A0 is non-breaking space — looks like space, isn't ASCII 0x20.
	r := Validate("echo\u00A0hello")
	if r.Behavior != Deny {
		t.Errorf("unicode whitespace: got %s, want deny (%s)", r.Behavior, r.Reason)
	}
}

func TestBraceExpansion(t *testing.T) {
	r := Validate("echo /etc/{passwd,shadow}")
	if r.Behavior != Ask {
		t.Errorf("brace expansion: got %s, want ask", r.Behavior)
	}
	r = Validate("echo hello")
	if r.Behavior != Allow {
		t.Errorf("normal: got %s, want allow", r.Behavior)
	}
}

func TestZshDangerous(t *testing.T) {
	tests := []struct {
		cmd  string
		want Behavior
	}{
		{"zmodload zsh/system", Deny},
		{"emulate -c 'evil'", Deny},
		{"zpty cmd", Deny},
		{"echo hello", Allow},
	}
	for _, tt := range tests {
		r := Validate(tt.cmd)
		if r.Behavior != tt.want {
			t.Errorf("%q: got %s, want %s", tt.cmd, r.Behavior, tt.want)
		}
	}
}

func TestSafeHeredoc(t *testing.T) {
	// Safe: single-quoted delimiter = literal body, no expansion.
	r := Validate("cat $(cat <<'EOF'\nhello\nworld\nEOF\n)")
	if r.Behavior == Deny {
		t.Errorf("safe heredoc: got %s, want non-deny", r.Behavior)
	}

	// Unsafe: unquoted delimiter allows expansion.
	r = Validate("cat $(cat <<EOF\n$(whoami)\nEOF\n)")
	if r.Behavior == Allow {
		t.Errorf("unsafe heredoc with expansion: got %s, want non-allow (%s)", r.Behavior, r.Reason)
	}
}

func TestDenyFirst(t *testing.T) {
	// Multiple validators would flag this — deny should win over ask.
	// validateZshDangerous runs later but catches "zpty" → Deny.
	r := Validate("zpty $(whoami)")
	if r.Behavior != Deny {
		t.Errorf("deny-first: got %s, want deny (%s)", r.Behavior, r.Reason)
	}

	// git_commit_rce should deny before command substitution asks.
	r = Validate("git commit -m $(whoami)")
	if r.Behavior != Deny {
		t.Errorf("git_commit_rce deny-first: got %s, want deny (%s)", r.Behavior, r.Reason)
	}
}

func TestExtractBaseCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"ls -la", "ls"},
		{"NODE_ENV=prod npm run build", "npm"},
		{"  git commit -m 'msg'", "git"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractBaseCommand(tt.cmd)
		if got != tt.want {
			t.Errorf("extractBaseCommand(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

func TestExtractQuotedContent(t *testing.T) {
	uq, fuq := extractQuotedContent(`echo 'hello world' "and goodbye"`)
	if uq != `echo hello world and goodbye` {
		t.Errorf("unquoted: got %q", uq)
	}
	// fullyUnquoted strips ALL quote content.
	if fuq != `echo  ` {
		t.Errorf("fully unquoted: got %q", fuq)
	}

	// Single-quoted $() SHOULD appear in uq (it's literal text).
	uq2, fuq2 := extractQuotedContent(`echo '$(whoami)'`)
	if uq2 != `echo $(whoami)` {
		t.Errorf("single-quoted $() preservation in uq: got %q, want %q", uq2, `echo $(whoami)`)
	}
	if fuq2 != `echo ` {
		t.Errorf("fully unquoted strips single-quoted content: got %q, want %q", fuq2, `echo `)
	}
}
