package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type sequentialCountFS struct {
	tools.Tool
	writes atomic.Int32
}

func (t *sequentialCountFS) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Action string `json:"action"`
	}
	json.Unmarshal(input, &in)
	if in.Action == "write" {
		t.writes.Add(1)
	}
	return t.Tool.Execute(ctx, input)
}
func TestSequentialDurableResumeContinuesActiveItemWithoutReplay(t *testing.T) {
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	root := sequentialFiles(t)
	root, err = filepath.EvalSymlinks(root)
	require.NoError(t, err)
	metadata := session.NewSession(root)
	metadata.Model = "fixture"
	metadata.Provider = "fixture"
	require.NoError(t, sessions.Create(t.Context(), metadata))
	queue, err := NewFlowQueue(db, time.Millisecond)
	require.NoError(t, err)
	bound := queue.Bind(metadata.ID, root, "fixture", "fixture")
	var mu sync.Mutex
	requests := map[string]int{}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Role, Content string
				ToolCalls     json.RawMessage `json:"tool_calls"`
				ToolCallID    string          `json:"tool_call_id"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&wire))
		encoded, _ := json.Marshal(wire.Messages)
		view := string(encoded)
		item := ""
		for _, name := range []string{"auth", "billing", "reports"} {
			if strings.Contains(view, "Inspect "+name+".ts") {
				item = name
				break
			}
		}
		mu.Lock()
		requests[item]++
		number := requests[item]
		mu.Unlock()
		if item == "billing" && number == 1 {
			sendSequentialFixture(w, wire.Stream, "", map[string]any{"id": "only-write", "type": "function", "function": map[string]any{"name": "fs", "arguments": `{"action":"write","path":"resume-proof.txt","content":"performed exactly once"}`}})
			return
		}
		if item == "billing" {
			require.True(t, wire.Stream)
			require.NotContains(t, view, "AUTH_ONLY_BYTES")
			require.NotContains(t, view, "AUTH_COMPLETED_ONCE")
			require.NotContains(t, view, "REPORTS_ONLY_BYTES")
			require.Contains(t, view, "only-write")
			require.Contains(t, view, "wrote resume-proof.txt")
		}
		sendSequentialFixture(w, wire.Stream, strings.ToUpper(item)+"_COMPLETED_ONCE", nil)
	}))
	defer fixture.Close()
	provider := providers.NewOpenAIProvider("fixture-only", nil)
	provider.BaseProvider = providers.NewBaseProvider("openai", fixture.URL, "fixture-only", []providers.Model{{ID: "fixture", ContextWindow: 32768}})
	counter := &sequentialCountFS{Tool: tools.NewFSTool(root)}
	registry := tools.NewRegistry()
	require.NoError(t, registry.Register(counter))
	makeAgent := func(steps int) *Agent {
		a := New(Config{Workspace: root, SessionID: metadata.ID, Provider: provider, Model: "fixture", Tools: registry, MaxSteps: steps, Tokenizer: tokenizer.NewEstimator(), ContextWindow: 32768, MaxTokens: 1024})
		a.SetFlowStore(bound, nil)
		return a
	}
	first := makeAgent(1)
	defer first.Shutdown()
	first.SetHistory([]providers.Message{{Role: "user", Content: "ORIGINAL_CONVO_BYTES"}})
	_, err = first.Run(t.Context(), "/think")
	require.NoError(t, err)
	_, err = first.Run(t.Context(), isolatedItems)
	require.ErrorContains(t, err, "exceeded max steps")
	require.EqualValues(t, 1, counter.writes.Load())
	state, err := bound.Load(flowIDFor(metadata.ID, isolatedItems))
	require.NoError(t, err)
	require.NotNil(t, state)
	require.NotNil(t, state.Sequential)
	require.Equal(t, 1, state.Sequential.Current)
	require.Equal(t, WorkItemDone, state.Sequential.Items[0].Status)
	require.Equal(t, WorkItemActive, state.Sequential.Items[1].Status)
	require.Equal(t, WorkItemPending, state.Sequential.Items[2].Status)
	require.True(t, state.Sequential.Started)
	require.NoError(t, validateSequentialCheckpoint(state.Sequential))
	require.Equal(t, isolatedItems, state.UserInput)
	persisted, err := sessions.Load(t.Context(), metadata.ID)
	require.NoError(t, err)
	raw, _ := json.Marshal(persisted.Messages)
	require.Contains(t, string(raw), "AUTH_COMPLETED_ONCE")
	require.Contains(t, string(raw), "ORIGINAL_CONVO_BYTES")
	jobs, err := queue.ListSession(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "queued", jobs[0].Status)
	time.Sleep(3 * time.Millisecond)
	lease, err := queue.Claim(t.Context(), state.ID)
	require.NoError(t, err)
	require.NotNil(t, lease)
	defer lease.Close()
	restarted := makeAgent(3)
	defer restarted.Shutdown()
	events, err := restarted.ContinueStream(t.Context(), lease.State)
	require.NoError(t, err)
	done := 0
	for event := range events {
		require.NotEqual(t, EventError, event.Type, fmt.Sprint(event.Error))
		if event.Type == EventDone {
			done++
			require.Contains(t, event.Result.Response, "3 done")
		}
	}
	require.Equal(t, 1, done)
	require.NoError(t, lease.Finish(t.Context(), "completed", ""))
	require.EqualValues(t, 1, counter.writes.Load())
	mu.Lock()
	require.Equal(t, map[string]int{"auth": 1, "billing": 2, "reports": 1}, requests)
	mu.Unlock()
	raw, _ = json.Marshal(restarted.History())
	for _, marker := range []string{"ORIGINAL_CONVO_BYTES", "AUTH_COMPLETED_ONCE", "BILLING_COMPLETED_ONCE", "REPORTS_COMPLETED_ONCE", "only-write", "performed exactly once"} {
		require.Contains(t, string(raw), marker)
	}
}
func TestSequentialResumeRejectsCorruptCursorBeforeMutation(t *testing.T) {
	a := New(Config{SessionID: "s", Model: "fixture"})
	defer a.Shutdown()
	original := []providers.Message{{Role: "user", Content: "original"}}
	a.SetHistory(original)
	state := &FlowState{SessionID: "s", Model: "fixture", History: []providers.Message{{Role: "user", Content: "replacement"}}, Sequential: &SequentialCheckpoint{Input: isolatedItems, Items: sequentialItems(isolatedItems), Current: 99, Scope: strings.Repeat("a", 32)}}
	_, err := a.ContinueStream(t.Context(), state)
	require.Error(t, err)
	require.Equal(t, original, a.History())
	require.False(t, a.IsBusy())
}
