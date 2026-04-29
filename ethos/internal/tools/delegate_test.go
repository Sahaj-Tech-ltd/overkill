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
