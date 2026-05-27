// Package tui — code-analysis slash commands.
//
// /introspect, /slice, /diagnose, /plan, /journal all walk the codebase
// or the session history and emit a report. They're grouped together
// because they share the "read the world, produce a summary" shape —
// none of them mutate files or change agent state.
package tui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/diagnostic"
	"github.com/Sahaj-Tech-ltd/overkill/internal/introspection"
	"github.com/Sahaj-Tech-ltd/overkill/internal/pipeline"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// runIntrospect regenerates the codebase wiki snippet under
// ~/.overkill/introspection from the current cwd.
func (m *appModel) runIntrospect() tea.Cmd {
	cwd, err := os.Getwd()
	if err != nil {
		return m.toastCmd("introspect: "+err.Error(), "error")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return m.toastCmd("introspect: "+err.Error(), "error")
	}
	outDir := filepath.Join(home, ".overkill", "introspection")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return m.toastCmd("introspect: mkdir: "+err.Error(), "error")
	}
	f, err := introspection.WriteCodebaseFromScan(cwd, outDir)
	if err != nil {
		return m.toastCmd("introspect: "+err.Error(), "error")
	}
	return m.toastCmd(fmt.Sprintf("introspect: wrote %s (%d chars)", f.Path, len(f.Content)), "success")
}

// runSlice decomposes the current draft / last user message into
// vertical slices.
func (m *appModel) runSlice() tea.Cmd {
	if m.app == nil || m.app.Agent == nil {
		return m.toastCmd("slice: agent not running", "warning")
	}
	hist := m.app.Agent.History()
	if len(hist) == 0 {
		return m.toastCmd("slice: no input — type a goal first, then run /slice", "info")
	}
	// Use the most recent user message as the spec.
	var spec string
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == "user" {
			spec = hist[i].Content
			break
		}
	}
	if spec == "" {
		return m.toastCmd("slice: no user message in history", "info")
	}
	slices, err := pipeline.DecomposeIntoSlices(spec)
	if err != nil {
		return m.toastCmd("slice: "+err.Error(), "error")
	}
	if len(slices) == 0 {
		return m.toastCmd("slice: nothing decomposable in last message", "info")
	}
	return m.toastCmd(fmt.Sprintf("slice: %d slices identified", len(slices)), "success")
}

// runDiagnose runs the file analyzer against the cwd and writes a
// report.
func (m *appModel) runDiagnose() tea.Cmd {
	cwd, err := os.Getwd()
	if err != nil {
		return m.toastCmd("diagnose: "+err.Error(), "error")
	}
	count := 0
	_ = filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".ts" && ext != ".tsx" && ext != ".py" {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil || len(b) > 64*1024 {
			return nil
		}
		_ = diagnostic.AnalyzeFile(path, string(b))
		count++
		return nil
	})
	return m.toastCmd(fmt.Sprintf("diagnose: analysed %d files", count), "success")
}

// runPlan dumps a starter implementation plan based on the last user
// goal. Lightweight: emits the plan to a toast scaffold; the real
// planner runs inside the agent loop on `/init` and dedicated planning
// prompts.
func (m *appModel) runPlan() tea.Cmd {
	if m.app == nil || m.app.Agent == nil {
		return m.toastCmd("plan: agent not running", "warning")
	}
	hist := m.app.Agent.History()
	var goal string
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == "user" {
			goal = hist[i].Content
			break
		}
	}
	if goal == "" {
		return m.toastCmd("plan: no goal — type one and re-run /plan", "info")
	}
	// Inject a plan-mode prompt; the agent picks it up on next turn.
	m.app.Agent.Inject(providers.Message{Role: "user", Content: planPrompt(goal)})
	return m.toastCmd("plan: drafting implementation plan from your last goal — see chat", "info")
}

func planPrompt(goal string) string {
	return "Switch to plan mode for the following goal. Produce: (1) a numbered task breakdown, (2) the files to touch with brief reasons, (3) risks/unknowns, (4) acceptance criteria. Do NOT modify any files.\n\nGoal:\n" + goal
}

// runJournal searches the flight-recorder index for the last user
// message text.
func (m *appModel) runJournal() tea.Cmd {
	if m.app == nil || m.app.Journal == nil {
		return m.toastCmd("journal: flight recorder not running", "warning")
	}
	// Lightweight summary: count entries for the active session.
	sid := ""
	if m.app.Agent != nil {
		sid = m.app.Agent.SessionID()
	}
	if sid == "" {
		return m.toastCmd("journal: no active session", "info")
	}
	entries, err := m.app.Journal.ReadSession(sid)
	if err != nil {
		return m.toastCmd("journal: "+err.Error(), "error")
	}
	return m.toastCmd(fmt.Sprintf("journal: %d entries in session %s", len(entries), sid), "info")
}
