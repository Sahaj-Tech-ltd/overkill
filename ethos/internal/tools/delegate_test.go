package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestManager() *subagent.Manager {
	return subagent.NewManager(subagent.Config{MaxDepth: 2, MaxChildren: 5})
}

func TestDelegateTool_Name(t *testing.T) {
	d := NewDelegateTool(nil)
	assert.Equal(t, "delegate_task", d.Name())
}

func TestDelegateTool_DisabledWhenNilManager(t *testing.T) {
	d := NewDelegateTool(nil)

	input, _ := json.Marshal(map[string]string{
		"goal": "do something",
	})

	out, err := d.Execute(context.Background(), input)
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal(out, &result))
	assert.Contains(t, result["error"], "not configured")
}

func TestDelegateTool_SingleGoal(t *testing.T) {
	d := NewDelegateTool(newTestManager())

	input, _ := json.Marshal(map[string]string{
		"goal": "fix the auth bug",
	})

	out, err := d.Execute(context.Background(), input)
	require.NoError(t, err)

	var result subagent.Result
	require.NoError(t, json.Unmarshal(out, &result))
	assert.Equal(t, "completed", result.Status)
}

func TestDelegateTool_BatchTasks(t *testing.T) {
	d := NewDelegateTool(newTestManager())

	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]string{
			{"goal": "refactor auth module"},
			{"goal": "add rate limiting"},
		},
	})

	out, err := d.Execute(context.Background(), input)
	require.NoError(t, err)

	var wrapper struct {
		Results []*subagent.Result `json:"results"`
	}
	require.NoError(t, json.Unmarshal(out, &wrapper))
	require.Len(t, wrapper.Results, 2)
	for i, r := range wrapper.Results {
		assert.Equal(t, "completed", r.Status, "task %d status", i)
	}
}

func TestDelegateTool_ValidationNoGoal(t *testing.T) {
	d := NewDelegateTool(newTestManager())

	out, err := d.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal(out, &result))
	assert.Contains(t, result["error"], "goal")
}

func TestDelegateTool_ContractRequiresFactory(t *testing.T) {
	d := NewDelegateTool(newTestManager())
	c := map[string]any{
		"contract": map[string]any{
			"id":   "c1",
			"goal": "do thing",
			"expected_outputs": []map[string]string{
				{"kind": "file", "spec": "out.go"},
			},
			"on_context_full": "compact_and_continue",
		},
	}
	in, _ := json.Marshal(c)
	out, err := d.Execute(context.Background(), in)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Contains(t, got["error"], "driver factory")
}

func TestDelegateTool_ContractWithFactory_SpawnsAndWaits(t *testing.T) {
	mgr := newTestManager()
	mgr.SetDriverFactory(func(c *subagent.Contract) (subagent.StepDriver, error) {
		return &fakeStepDriver{specs: []string{"out.go"}, completeAt: 2}, nil
	})
	d := NewDelegateTool(mgr)
	in, _ := json.Marshal(map[string]any{
		"wait": true,
		"contract": map[string]any{
			"id":   "c-wait",
			"goal": "do thing",
			"expected_outputs": []map[string]string{
				{"kind": "file", "spec": "out.go"},
			},
			"on_context_full": "compact_and_continue",
		},
	})
	out, err := d.Execute(context.Background(), in)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, "c-wait", got["id"])
	assert.NotNil(t, got["final_report"])
}

// fakeStepDriver is the minimal driver for delegate contract tests.
type fakeStepDriver struct {
	steps      int
	completeAt int
	specs      []string
}

func (f *fakeStepDriver) Step(ctx context.Context) subagent.StepResult {
	f.steps++
	return subagent.StepResult{ToolCalls: 1}
}
func (f *fakeStepDriver) Budget() subagent.BudgetSnapshot {
	return subagent.BudgetSnapshot{CurrentTokens: 10, MaxTokens: 100000}
}
func (f *fakeStepDriver) Compact(ctx context.Context) (subagent.BudgetSnapshot, error) {
	return f.Budget(), nil
}
func (f *fakeStepDriver) CheckOutput(ctx context.Context, _ []subagent.Output) []string {
	if f.completeAt > 0 && f.steps >= f.completeAt {
		return f.specs
	}
	return nil
}
