// Package tools — automation_list and cron_list surface SOP runs and
// scheduled jobs to the interactive agent (master plan §7.1, §7.2).
//
// Background: SOPs and cron jobs are owned by the daemon process; the
// interactive TUI never participated. The agent had zero visibility into
// scheduled work — it couldn't warn "your build cron fires in 4 min" or
// "the deploy SOP is paused waiting on your approval."
//
// These read-only tools query the same Postgres tables the daemon
// writes to. No live scheduler dependency. The daemon (or `overkill cron`
// / `overkill sop`) still owns mutation; the agent just reads.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
)

// AutomationLister is the minimal surface for listing SOPs without
// pulling in the full engine. *automation.PostgresSOPStore satisfies it.
type AutomationLister interface {
	LoadSOPs() ([]automation.SOP, error)
}

// AutomationListTool returns every known SOP with its status. Read-only.
type AutomationListTool struct {
	store AutomationLister
}

func NewAutomationListTool(s AutomationLister) *AutomationListTool {
	return &AutomationListTool{store: s}
}

func (t *AutomationListTool) Name() string { return "automation_list" }

type automationListInput struct {
	// Status optionally filters to one of "pending", "running", "completed",
	// "failed", "paused", "cancelled". Empty = no filter.
	Status string `json:"status,omitempty"`
}

func (t *AutomationListTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("automation store not configured (daemon-only setup)"), nil
	}
	var req automationListInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("automation_list: %w", err)
	}
	sops, err := t.store.LoadSOPs()
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out := make([]map[string]any, 0, len(sops))
	for _, s := range sops {
		if req.Status != "" && string(s.Status) != req.Status {
			continue
		}
		out = append(out, map[string]any{
			"id":         s.ID,
			"name":       s.Name,
			"status":     s.Status,
			"step_count": len(s.Steps),
			"created_at": s.CreatedAt,
			"updated_at": s.UpdatedAt,
		})
	}
	body, _ := json.Marshal(map[string]any{
		"sops":  out,
		"count": len(out),
	})
	return body, nil
}

// CronLister is the minimal surface for listing cron jobs without a live
// Scheduler. *cron.PostgresJobStore satisfies it.
type CronLister interface {
	LoadJobs() ([]cron.Job, error)
}

// CronListTool returns every known cron job with its next run, sorted by
// next run (closest first). Read-only — the daemon owns dispatch.
type CronListTool struct {
	store CronLister
}

func NewCronListTool(s CronLister) *CronListTool {
	return &CronListTool{store: s}
}

func (t *CronListTool) Name() string { return "cron_list" }

type cronListInput struct {
	// UpcomingOnly drops jobs whose NextRun is in the past or zero (i.e.
	// paused / never-scheduled). Default false = show everything.
	UpcomingOnly bool `json:"upcoming_only,omitempty"`
	// Within filters to jobs scheduled to fire within this many minutes
	// from now. 0 = no time filter. Implies upcoming_only.
	WithinMinutes int `json:"within_minutes,omitempty"`
}

func (t *CronListTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("cron store not configured (daemon-only setup)"), nil
	}
	var req cronListInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("cron_list: %w", err)
	}
	jobs, err := t.store.LoadJobs()
	if err != nil {
		return errorJSON(err.Error()), nil
	}

	now := time.Now()
	var cutoff time.Time
	if req.WithinMinutes > 0 {
		cutoff = now.Add(time.Duration(req.WithinMinutes) * time.Minute)
		req.UpcomingOnly = true
	}

	filtered := make([]cron.Job, 0, len(jobs))
	for _, j := range jobs {
		if req.UpcomingOnly && (j.NextRun.IsZero() || j.NextRun.Before(now)) {
			continue
		}
		if !cutoff.IsZero() && j.NextRun.After(cutoff) {
			continue
		}
		filtered = append(filtered, j)
	}
	// Closest-first ordering — the model usually cares about "what fires
	// next?" not the historical first-added.
	sort.Slice(filtered, func(i, j int) bool {
		a, b := filtered[i].NextRun, filtered[j].NextRun
		if a.IsZero() {
			return false
		}
		if b.IsZero() {
			return true
		}
		return a.Before(b)
	})

	out := make([]map[string]any, 0, len(filtered))
	for _, j := range filtered {
		entry := map[string]any{
			"id":            j.ID,
			"name":          j.Name,
			"schedule":      j.Schedule,
			"timezone":      j.Timezone,
			"status":        j.Status,
			"next_run":      j.NextRun,
			"last_run":      j.LastRun,
			"run_count":     j.RunCount,
			"failure_count": j.FailureCount,
		}
		if !j.NextRun.IsZero() {
			entry["minutes_until"] = fmt.Sprintf("%.1f", time.Until(j.NextRun).Minutes())
		}
		out = append(out, entry)
	}
	body, _ := json.Marshal(map[string]any{
		"jobs":  out,
		"count": len(out),
	})
	return body, nil
}
