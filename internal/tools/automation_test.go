package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
)

type fakeAutoStore struct {
	sops []automation.SOP
	err  error
}

func (f fakeAutoStore) LoadSOPs() ([]automation.SOP, error) { return f.sops, f.err }

type fakeCronStore struct {
	jobs []cron.Job
	err  error
}

func (f fakeCronStore) LoadJobs(ctx context.Context) ([]cron.Job, error) { return f.jobs, f.err }

func TestAutomationListTool_NoStore(t *testing.T) {
	tool := NewAutomationListTool(nil)
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(got), "not configured") {
		t.Errorf("expected not-configured, got %s", got)
	}
}

func TestAutomationListTool_FilterByStatus(t *testing.T) {
	store := fakeAutoStore{sops: []automation.SOP{
		{ID: "1", Name: "deploy", Status: "running"},
		{ID: "2", Name: "backup", Status: "paused"},
		{ID: "3", Name: "test", Status: "running"},
	}}
	tool := NewAutomationListTool(store)
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{"status":"running"}`))
	var out struct {
		Count int              `json:"count"`
		SOPs  []map[string]any `json:"sops"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 2 {
		t.Errorf("expected 2 running, got %d", out.Count)
	}
}

func TestCronListTool_SortByNextRun(t *testing.T) {
	now := time.Now()
	store := fakeCronStore{jobs: []cron.Job{
		{ID: "later", Name: "later", NextRun: now.Add(10 * time.Minute)},
		{ID: "sooner", Name: "sooner", NextRun: now.Add(1 * time.Minute)},
		{ID: "soonest", Name: "soonest", NextRun: now.Add(30 * time.Second)},
	}}
	tool := NewCronListTool(store)
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	var out struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(out.Jobs))
	}
	if out.Jobs[0]["name"] != "soonest" {
		t.Errorf("expected soonest first, got %v", out.Jobs[0]["name"])
	}
}

func TestCronListTool_WithinFilter(t *testing.T) {
	now := time.Now()
	store := fakeCronStore{jobs: []cron.Job{
		{ID: "near", NextRun: now.Add(2 * time.Minute)},
		{ID: "far", NextRun: now.Add(60 * time.Minute)},
		{ID: "past", NextRun: now.Add(-1 * time.Minute)},
	}}
	tool := NewCronListTool(store)
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{"within_minutes":10}`))
	var out struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(got, &out)
	if out.Count != 1 {
		t.Errorf("expected only the near job, got %d", out.Count)
	}
}
