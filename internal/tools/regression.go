// Package tools — regression_record / regression_list / regression_verify
// surface walls.RegressionBank to the agent (master plan §6.5 Wall 3).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
)

// RegressionRecordTool persists a new regression after a bug-fix.
type RegressionRecordTool struct {
	bank *walls.RegressionBank
}

func NewRegressionRecordTool(b *walls.RegressionBank) *RegressionRecordTool {
	return &RegressionRecordTool{bank: b}
}

func (t *RegressionRecordTool) Name() string { return "regression_record" }

type regressionRecordInput struct {
	Title     string `json:"title"`
	Symptom   string `json:"symptom"`
	RootCause string `json:"root_cause,omitempty"`
	TestCmd   string `json:"test_cmd"`
	CommitSHA string `json:"commit_sha,omitempty"`
}

func (t *RegressionRecordTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.bank == nil {
		return errorJSON("regression bank not configured"), nil
	}
	var req regressionRecordInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("regression_record: %w", err)
	}
	rec, err := t.bank.Record(&walls.Regression{
		Title:     req.Title,
		Symptom:   req.Symptom,
		RootCause: req.RootCause,
		TestCmd:   req.TestCmd,
		CommitSHA: req.CommitSHA,
	})
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"id": rec.ID, "created_at": rec.CreatedAt})
	return out, nil
}

// RegressionListTool returns all known regressions newest-first.
type RegressionListTool struct {
	bank *walls.RegressionBank
}

func NewRegressionListTool(b *walls.RegressionBank) *RegressionListTool {
	return &RegressionListTool{bank: b}
}

func (t *RegressionListTool) Name() string { return "regression_list" }

func (t *RegressionListTool) Execute(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if t.bank == nil {
		return errorJSON("regression bank not configured"), nil
	}
	list, err := t.bank.List()
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"regressions": list, "count": len(list)})
	return out, nil
}

// RegressionVerifyTool re-runs every recorded TestCmd and reports pass/fail.
type RegressionVerifyTool struct {
	bank *walls.RegressionBank
}

func NewRegressionVerifyTool(b *walls.RegressionBank) *RegressionVerifyTool {
	return &RegressionVerifyTool{bank: b}
}

func (t *RegressionVerifyTool) Name() string { return "regression_verify" }

type regressionVerifyInput struct {
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

func (t *RegressionVerifyTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.bank == nil {
		return errorJSON("regression bank not configured"), nil
	}
	var req regressionVerifyInput
	_ = json.Unmarshal(in, &req)
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	results, err := t.bank.Verify(ctx, timeout)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	failed := 0
	for _, r := range results {
		if !r.Passed {
			failed++
		}
	}
	out, _ := json.Marshal(map[string]any{
		"results":      results,
		"total":        len(results),
		"failed":       failed,
		"all_passing":  failed == 0,
	})
	return out, nil
}
