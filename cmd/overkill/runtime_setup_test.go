package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func runtimeTool(registry *tools.Registry, name string, input any) (json.RawMessage, error) {
	tool, err := registry.Get(name)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	output, err := tool.Execute(context.Background(), payload)
	if err != nil {
		return nil, err
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(output, &response) == nil && response["error"] != nil {
		return nil, fmt.Errorf("%s: %s", name, response["error"])
	}
	return output, nil
}

func TestSessionRuntimeIsolatesConcurrentWorkspacesAndRestoresPlans(t *testing.T) {
	home := t.TempDir()
	services, err := newRuntimeServices(config.Default(), nil, home)
	require.NoError(t, err)
	workspaces := []string{t.TempDir(), t.TempDir()}
	runtimes := make([]*api.SessionRuntime, 2)
	sessions := make([]*session.Session, 2)
	for i, workspace := range workspaces {
		sessions[i] = session.NewSession(workspace)
		runtimes[i], err = services.BuildSession(context.Background(), sessions[i])
		require.NoError(t, err)
		t.Cleanup(runtimes[i].Cleanup)
	}
	errors := make(chan error, 2)
	for i, runtime := range runtimes {
		go func(i int, runtime *api.SessionRuntime) {
			for _, call := range []struct {
				name  string
				input any
			}{
				{"fs", map[string]any{"action": "write", "path": "same.txt", "content": sessions[i].ID}},
				{"plan_set", map[string]any{"title": sessions[i].ID, "items": []string{"Implement session-specific behavior"}}},
				{"task_open", map[string]any{"intent": sessions[i].ID}},
				{"checkpoint_snapshot", map[string]any{"paths": []string{"same.txt"}, "reason": "before edit"}},
			} {
				if _, err := runtimeTool(runtime.Tools, call.name, call.input); err != nil {
					errors <- err
					return
				}
			}
			errors <- nil
		}(i, runtime)
	}
	for range runtimes {
		require.NoError(t, <-errors)
	}
	taskIDs := []string{}
	checkpointIDs := []string{}
	for i, runtime := range runtimes {
		content, err := os.ReadFile(filepath.Join(workspaces[i], "same.txt"))
		require.NoError(t, err)
		require.Equal(t, sessions[i].ID, string(content))
		output, err := runtimeTool(runtime.Tools, "task_list", nil)
		require.NoError(t, err)
		var listed struct {
			Tasks []struct {
				ID        string `json:"id"`
				SessionID string `json:"session_id"`
			}
		}
		require.NoError(t, json.Unmarshal(output, &listed))
		require.Len(t, listed.Tasks, 1)
		require.Equal(t, sessions[i].ID, listed.Tasks[0].SessionID)
		taskIDs = append(taskIDs, listed.Tasks[0].ID)
		output, err = runtimeTool(runtime.Tools, "checkpoint_list", nil)
		require.NoError(t, err)
		var checkpoints struct {
			Checkpoints []struct {
				ID        string `json:"id"`
				SessionID string `json:"session_id"`
			}
		}
		require.NoError(t, json.Unmarshal(output, &checkpoints))
		require.Len(t, checkpoints.Checkpoints, 1)
		require.Equal(t, sessions[i].ID, checkpoints.Checkpoints[0].SessionID)
		checkpointIDs = append(checkpointIDs, checkpoints.Checkpoints[0].ID)
	}
	_, err = runtimeTool(runtimes[0].Tools, "task_close", map[string]string{"id": taskIDs[1]})
	require.Error(t, err)
	_, err = runtimeTool(runtimes[0].Tools, "checkpoint_restore", map[string]string{"id": checkpointIDs[1]})
	require.Error(t, err)
	// Reconstruct services and tools, as a desktop/API restart would do.
	fresh, err := newRuntimeServices(config.Default(), nil, home)
	require.NoError(t, err)
	restored, err := fresh.BuildSession(context.Background(), sessions[0])
	require.NoError(t, err)
	defer restored.Cleanup()
	output, err := runtimeTool(restored.Tools, "plan_status", nil)
	require.NoError(t, err)
	require.Contains(t, string(output), sessions[0].ID)
	require.NotContains(t, string(output), sessions[1].ID)
	_, err = os.Stat(filepath.Join(home, "plans", ".json"))
	require.True(t, os.IsNotExist(err), "runtime must never write the empty-session plan")
}

func TestSessionRuntimeGoalsUseSameStoreAsAgent(t *testing.T) {
	database, _ := testpg.Open(t)
	services, err := newRuntimeServices(config.Default(), database, t.TempDir())
	require.NoError(t, err)
	for _, workspace := range []string{t.TempDir(), t.TempDir()} {
		metadata := session.NewSession(workspace)
		runtime, err := services.BuildSession(context.Background(), metadata)
		require.NoError(t, err)
		defer runtime.Cleanup()
		require.Same(t, services.Goals, runtime.GoalStore)
		_, err = runtimeTool(runtime.Tools, "create_goal", map[string]string{"objective": metadata.ID})
		require.NoError(t, err)
		goal, err := runtime.GoalStore.Get(context.Background(), metadata.ID)
		require.NoError(t, err)
		require.Equal(t, metadata.ID, goal.Text)
		restored, err := services.BuildSession(context.Background(), metadata)
		require.NoError(t, err)
		output, err := runtimeTool(restored.Tools, "get_goal", nil)
		restored.Cleanup()
		require.NoError(t, err)
		require.Contains(t, string(output), metadata.ID)
	}
	empty, err := services.Goals.Get(context.Background(), "")
	require.NoError(t, err)
	require.Nil(t, empty)
}

func TestSessionRuntimeAlarmCallbacksUseImmutableSession(t *testing.T) {
	services, err := newRuntimeServices(config.Default(), nil, t.TempDir())
	require.NoError(t, err)
	clock := automation.NewAlarmClockWithStore(func(*automation.Alarm) error { return nil }, automation.NewMemoryAlarmStore())
	services.Alarm = clock
	runtimes := []*api.SessionRuntime{}
	ids := []string{}
	for range 2 {
		metadata := session.NewSession(t.TempDir())
		ids = append(ids, metadata.ID)
		runtime, err := services.BuildSession(context.Background(), metadata)
		require.NoError(t, err)
		defer runtime.Cleanup()
		runtimes = append(runtimes, runtime)
		_, err = runtimeTool(runtime.Tools, "alarm_set", map[string]string{"name": "session-alarm", "when": "in 1h", "prompt": "Continue work"})
		require.NoError(t, err)
	}
	alarms := clock.List()
	require.Len(t, alarms, 2)
	found := map[string]bool{}
	for _, alarm := range alarms {
		require.NotEmpty(t, alarm.SessionID)
		found[alarm.SessionID] = true
	}
	require.True(t, found[ids[0]])
	require.True(t, found[ids[1]])
}
