// Package agent — Slash command handling (master plan §7.5).
//
// Overkill supports /safe, /auto, /build, /plan, and /yolo commands
// for mode switching during a session. These work from both the TUI
// (via the command palette) and messaging gateways (Telegram, Discord).
//
// Subcommands:
//   /plan view — opens the rendered plan HTML for the current session.
//
// Mode reference:
//   /safe  — safe mode: every tool call requires human approval
//   /yolo  — yolo mode: only dangerous ops trigger approval
//   /auto  — auto mode: zero human input, self-evaluate loop active
//   /plan  — plan-only mode: spec-driven, plan generation, no code execution
//   /build — build mode: load the current plan, auto-execute with self-eval

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SlashCommand represents a parsed slash command.
type SlashCommand struct {
	Command string // "safe", "auto", "yolo", "plan", "build"
	SubCmd  string // e.g. "view" for "/plan view"
	Args    string // remaining args after subcommand
	Raw     string // original input
}

// knownCommands maps slash commands to their autonomy level.
var knownCommands = map[string]string{
	"safe":    "safe",
	"yolo":    "yolo",
	"auto":    "auto",
	"plan":    "safe",  // plan mode = safe mode + spec driver
	"build":   "auto",  // build mode = auto mode + plan execution
	"think":   "think", // think mode = sequential multi-item processing
	"compact": "safe",  // compact = trigger context compaction
}

// ParseSlashCommand checks if a message is a slash command and returns it.
// Returns nil for normal messages.
func ParseSlashCommand(input string) *SlashCommand {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}

	// Strip leading slash and split.
	rest := strings.TrimPrefix(trimmed, "/")
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return nil
	}

	cmd := strings.ToLower(parts[0])
	var subCmd string
	var args string

	if len(parts) > 1 {
		subCmd = strings.ToLower(parts[1])
		if len(parts) > 2 {
			args = strings.Join(parts[2:], " ")
		}
	}

	if _, ok := knownCommands[cmd]; !ok {
		return nil
	}

	return &SlashCommand{
		Command: cmd,
		SubCmd:  subCmd,
		Args:    args,
		Raw:     trimmed,
	}
}

// handleSlashCommand processes a slash command at the top of the agent
// loop. Returns a response string and true if the input was consumed
// as a command. Returns ("", false) for non-command inputs.
//
// Side effects: updates the agent's autonomy level, spec driver, plan
// state, and self-evaluate loop.
func (a *Agent) handleSlashCommand(cmd *SlashCommand) (string, bool) {
	if cmd == nil {
		return "", false
	}

	switch cmd.Command {
	case "safe":
		a.SetAutoMode("safe")
		// Disable self-eval in safe mode — human is in control.
		if am := a.AutoMode(); am != nil {
			am.SetSelfEval(nil)
		}
		return "🔒 **Safe mode** — I'll ask before every tool call.", true

	case "yolo":
		a.SetAutoMode("yolo")
		// Disable self-eval in yolo mode — approval still gates dangerous ops.
		if am := a.AutoMode(); am != nil {
			am.SetSelfEval(nil)
		}
		return "⚡ **YOLO mode** — only dangerous operations need approval.", true

	case "auto":
		a.SetAutoMode("auto")
		// Enable self-eval loop for full autonomy.
		if am := a.AutoMode(); am != nil {
			sel := NewSelfEvaluateLoop(nil, nil, nil)
			a.mu.RLock()
			sel.recovery = a.recovery
			a.mu.RUnlock()
			am.SetSelfEval(sel)
		}
		return "🤖 **Auto mode** — full autonomy with self-evaluation. I'll iterate until confident.", true

	case "plan":
		// /plan view — show rendered plan HTML
		if cmd.SubCmd == "view" {
			return handlePlanView(), true
		}

		// Plan mode: safe autonomy + spec driver enabled.
		a.SetAutoMode("safe")
		if am := a.AutoMode(); am != nil {
			am.SetSelfEval(nil)
		}
		// Enable spec-driven mode.
		if a.specDriver != nil {
			a.specDriver.SetEnabled(true)
		}
		return "📋 **Plan mode** — I'll write a structured plan before any code. Describe what you want to build.", true

	case "build":
		// Build mode: auto mode + load current plan + start executing.
		a.SetAutoMode("auto")
		// Try to find and load a plan.
		planFiles := FindPlanFiles()
		msg := "🔨 **Build mode** — full autonomy with self-evaluation."

		if len(planFiles) > 0 {
			plan, err := LoadPlan(planFiles[0])
			if err != nil {
				msg += "\n⚠️ Found plan but couldn't load: " + err.Error()
			} else {
				if am := a.AutoMode(); am != nil {
					am.Plan = plan
					sel := NewSelfEvaluateLoop(nil, nil, nil)
					a.mu.RLock()
					sel.recovery = a.recovery
					a.mu.RUnlock()
					am.SetSelfEval(sel)
				}
				msg += "\n📄 Loaded plan: **" + plan.Title + "** (" +
					strings.ReplaceAll(formatPlanProgress(plan), "\n", " ") + ")"
				msg += "\nStarting execution automatically..."
			}
		} else {
			msg += "\n⚠️ No plan found. Use /plan first to create one."
		}

		return msg, true

	case "think":
		// Think mode toggle: enables/disables preamble streaming.
		// When enabled, the agent emits short natural-language preambles
		// before tool calls so the user sees what's happening.
		a.mu.Lock()
		current := a.thinkConfig.Enabled
		a.thinkConfig.Enabled = !current
		a.seqEnabled = a.thinkConfig.Enabled // keep seq in sync for compat
		a.mu.Unlock()
		if !current {
			return "🧠 **Thinking on** — I'll stream brief preambles before tool calls so you can see what I'm doing.", true
		}
		return "🧠 **Thinking off** — preamble streaming disabled.", true

	case "compact":
		// Trigger context compaction immediately.
		result, err := a.Compact(context.Background())
		if err != nil {
			return "⚠️ Compaction failed: " + err.Error(), true
		}
		return fmt.Sprintf("📦 **Compacted** — %d → %d messages, %d → %d tokens. Saved %d%%.",
			result.MessagesBefore, result.MessagesAfter,
			result.TokensBefore, result.TokensAfter,
			100-int(float64(result.TokensAfter)/float64(result.TokensBefore)*100)), true
	}

	return "", false
}

// handlePlanView returns info about the rendered plan HTML files.
func handlePlanView() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "⚠️ Cannot find home directory: " + err.Error()
	}
	plansDir := filepath.Join(home, ".overkill", "plans")
	entries, err := os.ReadDir(plansDir)
	if err != nil {
		return "📂 No rendered plans yet. Plans appear here after running the pipeline with `/plan` or `/goal set`."
	}

	var htmlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".html") {
			htmlFiles = append(htmlFiles, e.Name())
		}
	}

	if len(htmlFiles) == 0 {
		return "📂 No plan HTML files found in `" + plansDir + "`. Run the pipeline to generate one."
	}

	msg := "📋 **Rendered Plans** (" + itoa(len(htmlFiles)) + "):\n"
	for _, f := range htmlFiles {
		fullPath := filepath.Join(plansDir, f)
		msg += "• `" + fullPath + "`\n"
	}
	msg += "\nOpen any file in your browser to view the full DeepWiki-style plan."
	return msg
}

func formatPlanProgress(plan *Plan) string {
	total := len(plan.Phases)
	done := 0
	for _, p := range plan.Phases {
		if p.Status == PhaseDone {
			done++
		}
	}
	if total == 0 {
		return "0 phases"
	}
	return strings.Repeat("✅", done) + strings.Repeat("⬜", total-done) +
		" " + itoa(done) + "/" + itoa(total) + " phases"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
