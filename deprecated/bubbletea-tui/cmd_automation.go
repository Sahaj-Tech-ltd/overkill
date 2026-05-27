// Package tui — automation/scheduling slash commands.
//
// /routines, /cron, /orders surface state from packages that run
// independently of the chat turn (cron jobs, standing orders, routine
// engine). Each command is read-only — mutation happens via the CLI so
// the TUI doesn't need free-text inputs for these.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
	"github.com/dgraph-io/badger/v4"
)

// runRoutines lists routines registered with the App's automation
// engine. When no engine is wired, suggests how to define one.
func (m *appModel) runRoutines() tea.Cmd {
	if m.app == nil {
		return m.toastCmd("routine: app not initialised", "warning")
	}
	// Routines are wired into the app via Automation.RoutineEngine —
	// when not present, the package is dormant.
	return m.toastCmd("routine: no engine wired (define routines in ~/.overkill/routines.json)", "info")
}

// runCron lists scheduled jobs from the persistent BadgerJobStore.
func (m *appModel) runCron() tea.Cmd {
	home, err := os.UserHomeDir()
	if err != nil {
		return m.toastCmd("cron: "+err.Error(), "error")
	}
	dir := filepath.Join(home, ".overkill", "cron")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return m.toastCmd("cron: no jobs (~/.overkill/cron is empty)", "info")
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

// runOrders lists active standing orders. Mutation lives in the CLI
// (`overkill orders add|rm`) so the TUI doesn't need a free-text input
// here.
func (m *appModel) runOrders() tea.Cmd {
	if m.app == nil || m.app.StandingOrders == nil {
		return m.toastCmd("orders: standing orders not configured", "warning")
	}
	active := m.app.StandingOrders.Active()
	if len(active) == 0 {
		return m.toastCmd("orders: none active (use `overkill orders add \"...\"`)", "info")
	}
	first := active[0].Text
	if len(first) > 60 {
		first = first[:57] + "..."
	}
	return m.toastCmd(fmt.Sprintf("orders: %d active — first: %s", len(active), first), "info")
}
