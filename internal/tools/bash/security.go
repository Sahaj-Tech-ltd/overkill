// Package bash provides security classification for shell commands.
// Architecture ported from Claude Code's bashPermissions.ts + bashSecurity.ts:
// a validator chain where each validator returns allow | deny | ask | passthrough.
// The first non-passthrough result wins (deny-first semantics).
//
// Validators run cheapest-first: empty check → incomplete detection →
// metacharacter scan → dangerous patterns → shell-specific checks.
package bash

import (
	"strings"
	"unicode"
)

// Behavior is the validator's recommended action.
type Behavior string

const (
	Allow       Behavior = "allow"
	Deny        Behavior = "deny"
	Ask         Behavior = "ask"
	Passthrough Behavior = "passthrough"
)

// Result is a single validator's output.
type Result struct {
	Behavior Behavior
	Message  string
	Reason   string // machine-readable reason for logging/analytics
}

// ValidationCtx carries the parsed/derived forms of a shell command
// that validators examine.
type ValidationCtx struct {
	Command            string // original, as typed
	Trimmed            string // whitespace-trimmed
	BaseCommand        string // first word (the binary/program name)
	Unquoted           string // single-quote content preserved, double-quote content stripped
	FullyUnquoted      string // all quote content removed
	HasTreeSitter      bool   // true if AST-backed validation is available
}

// NewContext parses a raw command string into a ValidationCtx.
func NewContext(command string) ValidationCtx {
	trimmed := strings.TrimSpace(command)
	base := extractBaseCommand(trimmed)
	uq, fuq := extractQuotedContent(command)
	return ValidationCtx{
		Command:       command,
		Trimmed:       trimmed,
		BaseCommand:   base,
		Unquoted:      uq,
		FullyUnquoted: fuq,
	}
}

// Validate runs the full validator chain against the given command.
// Returns the first non-passthrough result (deny-first).
func Validate(command string) Result {
	return ValidateWith(command, AllValidators)
}

// ValidateWith runs a custom validator chain.
func ValidateWith(command string, validators []Validator) Result {
	ctx := NewContext(command)
	for _, v := range validators {
		r := v(ctx)
		if r.Behavior != Passthrough {
			return r
		}
	}
	return Result{Behavior: Allow, Reason: "all_validators_passed"}
}

// Validator is a single security check function.
type Validator func(ctx ValidationCtx) Result

// AllValidators is the default validation chain, ordered cheapest-first.
var AllValidators = []Validator{
	validateEmpty,
	validateIncomplete,
	validateShellMetacharacters,
	validateDangerousPatterns,
	validateZshDangerous, // deny-first before command sub
	validateCommandSubstitution,
	validateNewlines,
	validateObfuscatedFlags,
	validateControlCharacters,
	validateUnicodeWhitespace,
	validateBraceExpansion,
}

// ─── helpers ────────────────────────────────────────────────────────────────

// extractBaseCommand returns the first word of a command string.
func extractBaseCommand(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Skip env var assignments (VAR=value) to find the actual command.
	for {
		parts := strings.SplitN(s, " ", 2)
		first := parts[0]
		if !strings.Contains(first, "=") || !isEnvVarAssign(first) {
			return first
		}
		if len(parts) < 2 {
			return ""
		}
		s = strings.TrimSpace(parts[1])
	}
}

func isEnvVarAssign(s string) bool {
	// Must start with [A-Za-z_][A-Za-z0-9_]*=
	for i, c := range s {
		if c == '=' && i > 0 {
			return true
		}
		if i == 0 && !unicode.IsLetter(c) && c != '_' {
			return false
		}
		if i > 0 && !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' {
			return false
		}
	}
	return false
}

// extractQuotedContent produces two forms:
//   - unquoted: quote characters removed, but content inside all quotes preserved.
//     This is the form used by shell metacharacter detection — $() inside
//     single quotes is literal text, but inside double quotes it's active.
//   - fullyUnquoted: ALL quote content stripped (single + double quote
//     content removed). Used for checks that shouldn't fire on ANY quoted text.
func extractQuotedContent(command string) (unquoted, fullyUnquoted string) {
	var uq, fuq strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for i := 0; i < len(command); i++ {
		c := rune(command[i])

		if escaped {
			escaped = false
			if !inSingle && !inDouble {
				// Only process escape sequences outside single quotes.
				// In bash, backslash inside single quotes is literal.
				uq.WriteRune(c)
				fuq.WriteRune(c)
			}
			continue
		}

		if c == '\\' && !inSingle {
			escaped = true
			uq.WriteRune(c)
			if !inSingle && !inDouble {
				fuq.WriteRune(c)
			}
			continue
		}

		if c == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}

		if c == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}

		// Content inside ANY quote is preserved in uq, but only
		// content outside ALL quotes goes into fuq.
		uq.WriteRune(c)
		if !inSingle && !inDouble {
			fuq.WriteRune(c)
		}
	}

	return uq.String(), fuq.String()
}

// hasUnescaped checks for an unescaped occurrence of a single character.
func hasUnescaped(s string, char byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++ // skip escaped char
			continue
		}
		if s[i] == char {
			return true
		}
	}
	return false
}

// containsAny reports whether s contains any of the given chars.
func containsAny(s string, chars ...byte) bool {
	for i := 0; i < len(s); i++ {
		for _, c := range chars {
			if s[i] == c {
				return true
			}
		}
	}
	return false
}

// EnvVarSet is environment variables considered safe for prefix stripping.
var EnvVarSet = map[string]bool{
	"HOME": true, "USER": true, "PATH": true, "PWD": true,
	"LANG": true, "LC_ALL": true, "TERM": true, "SHELL": true,
	"EDITOR": true, "VISUAL": true, "NODE_ENV": true,
	"GOPATH": true, "GOROOT": true, "PYTHONPATH": true,
	"RUST_LOG": true, "DEBUG": true, "VERBOSE": true,
	"CLAUDE_CODE_": true, "OVERKILL_": true, // prefix match
}
