// Package input classifies user-typed text into intent categories so the
// TUI can route or hint accordingly. Phase 1.5 #2 (master plan).
//
// The classifier is deliberately heuristic — fast, deterministic, no LLM
// call. False positives are corrected by the user (they edit and resend);
// false negatives just mean a missed hint, not a broken interaction.
//
// Three labels:
//   - KindShell:    a literal shell command. Routes to direct exec ($hell).
//   - KindNL:       natural language for the agent. Default.
//   - KindAmbiguous: looks like it could be either. UI can show a hint
//                   ("press $ to run as shell, Enter to ask agent").
//
// We DON'T attempt to be exhaustive. The high-confidence shell signal is
// strong (leading $, common commands, metacharacters). Everything else
// stays NL. The Ambiguous label is reserved for short single-word inputs
// that are ALSO a real command name (`ls`, `git`, `npm`) — those are the
// only place the user might be unsure.
package input

import (
	"strings"
	"unicode"
)

// Kind is the classification result.
type Kind int

const (
	// KindNL is natural language (the agent path). Default.
	KindNL Kind = iota
	// KindShell is a literal shell command (the $hell path).
	KindShell
	// KindAmbiguous looks like both. Surface a hint, let user pick.
	KindAmbiguous
)

func (k Kind) String() string {
	switch k {
	case KindShell:
		return "shell"
	case KindAmbiguous:
		return "ambiguous"
	default:
		return "nl"
	}
}

// commonShellCommands is the tight set of recognised single-token
// commands. Membership signals "could be shell" — combined with other
// signals it tips toward KindShell, alone it triggers KindAmbiguous.
//
// Curated, not exhaustive: catches the 95% case without false-positiving
// on every English verb. `make`, `test`, `build` etc. are deliberately
// excluded because they're common verbs in NL chat.
var commonShellCommands = map[string]bool{
	"ls": true, "pwd": true, "cd": true, "cat": true, "head": true,
	"tail": true, "grep": true, "find": true, "rg": true, "ag": true,
	"git": true, "gh": true, "npm": true, "pnpm": true, "yarn": true,
	"bun": true, "cargo": true, "go": true, "python": true, "python3": true,
	"node": true, "deno": true, "ruby": true, "rake": true,
	"docker": true, "kubectl": true, "helm": true, "terraform": true,
	"curl": true, "wget": true, "ssh": true, "scp": true, "rsync": true,
	"chmod": true, "chown": true, "rm": true, "mv": true, "cp": true,
	"ln": true, "mkdir": true, "rmdir": true, "touch": true,
	"echo": true, "printf": true, "true": true, "false": true,
	"sed": true, "awk": true, "cut": true, "sort": true, "uniq": true,
	"wc": true, "tr": true, "tee": true, "xargs": true, "ps": true,
	"top": true, "df": true, "du": true, "kill": true, "killall": true,
	"jobs": true, "fg": true, "bg": true, "nohup": true,
	"export": true, "env": true, "unset": true, "alias": true,
	"which": true, "whereis": true, "type": true, "command": true,
	"date": true, "uptime": true, "uname": true, "hostname": true,
	"tar": true, "zip": true, "unzip": true, "gzip": true,
}

// shellMetaChars are characters that strongly suggest a shell command
// when present. Pipes, redirects, glob, command substitution.
const shellMetaChars = "|><&`$*?["

// Classify returns the kind for a single input line. Empty / whitespace
// input returns KindNL (no signal to act on).
func Classify(input string) Kind {
	s := strings.TrimSpace(input)
	if s == "" {
		return KindNL
	}

	// 1. Leading `$` is a deterministic shell signal — same path as the
	// $hell shortcut. Highest-confidence shell label.
	if strings.HasPrefix(s, "$") {
		return KindShell
	}

	// 2. Shell metacharacters anywhere. Heuristic but strong: it's hard
	// to construct NL chat containing |, >, &&, etc. without meaning a
	// shell command. The `$` case is already handled above.
	if containsAny(s, shellMetaChars) {
		// Refinement: words ending in "?" are likely questions, not
		// shell glob. Bail if the only meta char is a trailing `?`.
		if onlyTrailingQuestion(s) {
			return KindNL
		}
		return KindShell
	}

	// 3. First token is a recognised shell command name.
	first := firstToken(s)
	if first == "" {
		return KindNL
	}
	isCmd := commonShellCommands[strings.ToLower(first)]
	if !isCmd {
		return KindNL
	}

	// First token is a known command. Look at the rest:
	//   - If it has a flag-like token (-x, --foo) or a path-like token
	//     (./, /, ~/) → confidently shell.
	//   - Otherwise it's ambiguous: `ls` alone could be the command or
	//     a person saying "ls" as shorthand for "list".
	rest := strings.TrimSpace(strings.TrimPrefix(s, first))
	if rest == "" {
		return KindAmbiguous
	}
	if hasFlagOrPath(rest) {
		return KindShell
	}
	// `git status`, `npm install` — known command + plain arg. The
	// joint vocabulary makes it more shell than NL.
	if commonShellCommands[strings.ToLower(firstToken(rest))] || isLikelySubcommand(first, firstToken(rest)) {
		return KindShell
	}
	return KindAmbiguous
}

// containsAny reports whether s contains any char from chars.
func containsAny(s, chars string) bool {
	for i := 0; i < len(s); i++ {
		if strings.ContainsRune(chars, rune(s[i])) {
			return true
		}
	}
	return false
}

// onlyTrailingQuestion checks if the only meta character is a `?` and
// it sits at the very end. NL questions like "how do globs work?" should
// not be classified as shell just because they contain `?`.
func onlyTrailingQuestion(s string) bool {
	if !strings.HasSuffix(s, "?") {
		return false
	}
	body := strings.TrimRight(s, "?")
	return !containsAny(body, shellMetaChars)
}

// firstToken returns the first whitespace-separated token.
func firstToken(s string) string {
	for i, r := range s {
		if unicode.IsSpace(r) {
			return s[:i]
		}
	}
	return s
}

// hasFlagOrPath returns true if any token starts with `-`, `./`, `../`,
// `/`, or `~/`. These are clear shell-argument shapes.
func hasFlagOrPath(s string) bool {
	for _, tok := range strings.Fields(s) {
		switch {
		case strings.HasPrefix(tok, "-"):
			return true
		case strings.HasPrefix(tok, "./"):
			return true
		case strings.HasPrefix(tok, "../"):
			return true
		case strings.HasPrefix(tok, "/"):
			return true
		case strings.HasPrefix(tok, "~/"):
			return true
		}
	}
	return false
}

// isLikelySubcommand handles the common `<tool> <subcommand>` pattern
// (`git status`, `npm install`, `docker ps`). We don't try to enumerate
// every subcommand — instead we treat a lowercase alphanumeric second
// token as a likely subcommand when paired with a known tool.
func isLikelySubcommand(tool, second string) bool {
	if second == "" {
		return false
	}
	// Only the tools whose subcommand surface is large and shell-shaped
	// — `git status`, `docker ps`, etc. — benefit from this. For other
	// commands we already returned shell above on flag/path detection.
	switch strings.ToLower(tool) {
	case "git", "gh", "docker", "kubectl", "helm", "npm", "pnpm", "yarn",
		"bun", "cargo", "go", "make", "terraform":
	default:
		return false
	}
	for _, r := range second {
		if !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '-' && r != ':' {
			return false
		}
	}
	return true
}
