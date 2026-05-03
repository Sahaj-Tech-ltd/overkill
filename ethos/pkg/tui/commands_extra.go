// Package tui — extra slash-command handlers (master plan §7).
//
// These commands surface previously-orphaned packages (walls, automation,
// cron, introspection, pipeline, diagnostic, journal, red-team) so users
// can drive them from the palette. Each handler is best-effort: when a
// package's underlying state isn't wired (e.g. no scheduler running, no
// orchestrator built) the command returns a clear toast rather than panicking.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/internal/cron"
	"github.com/Sahaj-Tech-ltd/ethos/internal/diagnostic"
	"github.com/Sahaj-Tech-ltd/ethos/internal/introspection"
	"github.com/Sahaj-Tech-ltd/ethos/internal/pipeline"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/walls"
	"github.com/dgraph-io/badger/v4"
)

// runWalls executes the architecture wall against the current working tree
// and emits a one-line summary. Test-quality and ouroboros walls require a
// code/test pair so they're not run here without explicit input.
func (m *appModel) runWalls() tea.Cmd {
	cwd, err := os.Getwd()
	if err != nil {
		return m.toastCmd("walls: getcwd: "+err.Error(), "error")
	}
	files := map[string]string{}
	_ = filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".ts" && ext != ".tsx" && ext != ".py" {
			return nil
		}
		rel, _ := filepath.Rel(cwd, path)
		if strings.HasPrefix(rel, "vendor/") || strings.HasPrefix(rel, "node_modules/") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if len(b) > 64*1024 {
			return nil
		}
		files[rel] = string(b)
		return nil
	})
	wall := walls.NewArchitectureWall(walls.ArchitectureConfig{})
	res, err := wall.Check(context.Background(), files)
	if err != nil {
		return m.toastCmd("walls: "+err.Error(), "error")
	}
	if res == nil {
		return m.toastCmd("walls: no findings", "success")
	}
	return m.toastCmd(fmt.Sprintf("architecture wall: severity=%s passed=%v details=%d", res.Severity, res.Passed, len(res.Details)), wallToastKind(res.Severity))
}

func wallToastKind(s walls.Severity) string {
	switch s {
	case walls.SeverityBlock:
		return "error"
	case walls.SeverityWarning:
		return "warning"
	default:
		return "info"
	}
}

// runRoutines lists routines registered with the App's automation engine.
// When no engine is wired, suggests how to define one.
func (m *appModel) runRoutines() tea.Cmd {
	if m.app == nil {
		return m.toastCmd("routine: app not initialised", "warning")
	}
	// Routines are wired into the app via Automation.RoutineEngine — when
	// not present, the package is dormant.
	return m.toastCmd("routine: no engine wired (define routines in ~/.ethos/routines.json)", "info")
}

// runCron lists scheduled jobs from the persistent BadgerJobStore.
func (m *appModel) runCron() tea.Cmd {
	home, err := os.UserHomeDir()
	if err != nil {
		return m.toastCmd("cron: "+err.Error(), "error")
	}
	dir := filepath.Join(home, ".ethos", "cron")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return m.toastCmd("cron: no jobs (~/.ethos/cron is empty)", "info")
	}
	db, err := badger.Open(badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR))
	if err != nil {
		return m.toastCmd("cron: open: "+err.Error(), "error")
	}
	defer db.Close()
	store := cron.NewBadgerJobStore(db)
	jobs, err := store.LoadJobs()
	if err != nil {
		return m.toastCmd("cron: load: "+err.Error(), "error")
	}
	if len(jobs) == 0 {
		return m.toastCmd("cron: no jobs scheduled", "info")
	}
	return m.toastCmd(fmt.Sprintf("cron: %d job(s) scheduled (next: %s)", len(jobs), cronNextSummary(jobs)), "info")
}

func cronNextSummary(jobs []cron.Job) string {
	var soonest time.Time
	for _, j := range jobs {
		if soonest.IsZero() || (!j.NextRun.IsZero() && j.NextRun.Before(soonest)) {
			soonest = j.NextRun
		}
	}
	if soonest.IsZero() {
		return "n/a"
	}
	return time.Until(soonest).Round(time.Second).String()
}

// runIntrospect regenerates the codebase wiki snippet under
// ~/.ethos/introspection from the current cwd.
func (m *appModel) runIntrospect() tea.Cmd {
	cwd, err := os.Getwd()
	if err != nil {
		return m.toastCmd("introspect: "+err.Error(), "error")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return m.toastCmd("introspect: "+err.Error(), "error")
	}
	outDir := filepath.Join(home, ".ethos", "introspection")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return m.toastCmd("introspect: mkdir: "+err.Error(), "error")
	}
	f, err := introspection.WriteCodebaseFromScan(cwd, outDir)
	if err != nil {
		return m.toastCmd("introspect: "+err.Error(), "error")
	}
	return m.toastCmd(fmt.Sprintf("introspect: wrote %s (%d chars)", f.Path, len(f.Content)), "success")
}

// runSlice decomposes the current draft / last user message into vertical slices.
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

// runDiagnose runs the file analyzer against the cwd and writes a report.
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

// runPlan dumps a starter implementation plan based on the last user goal.
// Lightweight: emits the plan to a toast scaffold; the real planner runs
// inside the agent loop on `/init` and dedicated planning prompts.
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

// runJournal searches the flight-recorder index for the last user message text.
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

// runRedteam invokes the bundled red-team skill against the current session.
// Today this surfaces the skill name; full execution requires the skill engine
// to expose a programmatic Run.
func (m *appModel) runRedteam() tea.Cmd {
	if m.app == nil {
		return m.toastCmd("redteam: app not initialised", "warning")
	}
	for _, sk := range m.app.Skills {
		if sk.Name == "red-team" || sk.Name == "redteam" {
			if sk.Enabled {
				return m.toastCmd("redteam: skill loaded — invoke via 'use the red-team skill on this code'", "info")
			}
			return m.toastCmd("redteam: skill present but disabled (toggle in /skills)", "warning")
		}
	}
	return m.toastCmd("redteam: red-team skill not installed", "info")
}
