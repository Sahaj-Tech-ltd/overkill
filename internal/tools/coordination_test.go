package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func coordinationToolsFixture(t *testing.T) (*coordination.Store, coordination.Peer, coordination.Peer, map[string]Tool, map[string]Tool) {
	t.Helper()
	db, _ := testpg.Open(t)
	store, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	frontend, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "project", Name: "frontend", Role: "UI", Workspace: t.TempDir()})
	require.NoError(t, err)
	backend, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "project", Name: "backend", Role: "API", Workspace: t.TempDir()})
	require.NoError(t, err)
	toolMap := func(peerID string) map[string]Tool {
		result := map[string]Tool{}
		for _, tool := range NewCoordinationTools(store, peerID) {
			result[tool.Name()] = tool
		}
		return result
	}
	return store, frontend, backend, toolMap(frontend.ID), toolMap(backend.ID)
}
func coordInput(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
func coordResultMessage(t *testing.T, result json.RawMessage) coordination.Message {
	t.Helper()
	var output struct {
		Message coordination.Message `json:"message"`
	}
	require.NoError(t, json.Unmarshal(result, &output))
	require.NotEmpty(t, output.Message.ID)
	return output.Message
}
func coordSendFixture(t *testing.T, tool Tool, key string) coordination.Message {
	t.Helper()
	output, err := tool.Execute(t.Context(), coordInput(t, map[string]any{"to": "backend", "kind": "request", "payload": map[string]string{"question": "Is GET /users ready?"}, "idempotency_key": key}))
	require.NoError(t, err)
	return coordResultMessage(t, output)
}

func TestCoordinationToolsDurableSendInboxClaimReplyAndStatus(t *testing.T) {
	store, frontend, backend, frontTools, backTools := coordinationToolsFixture(t)
	sent := coordSendFixture(t, frontTools["agent_send"], "request-1")
	require.Equal(t, coordination.Accepted, sent.State)
	require.Equal(t, frontend.ID, sent.From)
	require.Equal(t, backend.ID, sent.To)
	retry := coordSendFixture(t, frontTools["agent_send"], "request-1")
	require.Equal(t, sent.ID, retry.ID)
	inbox, err := backTools["agent_inbox"].Execute(t.Context(), json.RawMessage(`{}`))
	require.NoError(t, err)
	var incoming struct {
		Messages []coordination.Message `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(inbox, &incoming))
	require.Len(t, incoming.Messages, 1)
	claimed, err := store.Claim(t.Context(), backend.ID)
	require.NoError(t, err)
	require.Equal(t, sent.ID, claimed.ID)
	status, err := frontTools["agent_task_status"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID}))
	require.NoError(t, err)
	require.Equal(t, coordination.Started, coordResultMessage(t, status).State)
	replyInput := coordInput(t, map[string]any{"message_id": sent.ID, "payload": map[string]string{"answer": "GET /users is ready"}, "idempotency_key": "reply-1"})
	result, err := backTools["agent_reply"].Execute(t.Context(), replyInput)
	require.NoError(t, err)
	answer := coordResultMessage(t, result)
	require.Equal(t, backend.ID, answer.From)
	require.Equal(t, frontend.ID, answer.To)
	require.Equal(t, sent.ID, answer.InReplyTo)
	require.Equal(t, sent.ConversationID, answer.ConversationID)
	replay, err := backTools["agent_reply"].Execute(t.Context(), replyInput)
	require.NoError(t, err)
	require.Equal(t, answer.ID, coordResultMessage(t, replay).ID)
	completed, err := store.Read(t.Context(), backend.ID, claimed.ID)
	require.NoError(t, err)
	require.Equal(t, coordination.Completed, completed.State, "answer and request completion commit atomically")
	result, err = frontTools["agent_task_status"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID}))
	require.NoError(t, err)
	require.Equal(t, coordination.Completed, coordResultMessage(t, result).State)
	var finished struct {
		Events []coordination.Event `json:"events"`
	}
	require.NoError(t, json.Unmarshal(result, &finished))
	require.Equal(t, "completed", finished.Events[len(finished.Events)-1].Type)
	require.JSONEq(t, string(coordInput(t, map[string]string{"reply_id": answer.ID})), string(finished.Events[len(finished.Events)-1].Payload))
	firstPage, err := frontTools["agent_task_status"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID, "limit": 1}))
	require.NoError(t, err)
	var page struct {
		Events []coordination.Event `json:"events"`
		Next   int64                `json:"next_revision"`
		More   bool                 `json:"has_more"`
	}
	require.NoError(t, json.Unmarshal(firstPage, &page))
	require.True(t, page.More)
	require.Len(t, page.Events, 1)
	nextPage, err := frontTools["agent_task_status"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID, "after_revision": page.Next}))
	require.NoError(t, err)
	var remaining struct {
		Events []coordination.Event `json:"events"`
		More   bool                 `json:"has_more"`
	}
	require.NoError(t, json.Unmarshal(nextPage, &remaining))
	require.False(t, remaining.More)
	for _, event := range remaining.Events {
		require.Greater(t, event.Revision, page.Next)
	}

}

func TestCoordinationToolsRejectIdentityUnknownAndUnboundedInputs(t *testing.T) {
	_, _, _, tools, _ := coordinationToolsFixture(t)
	valid := map[string]map[string]any{
		"agent_send":       {"to": "backend", "kind": "request", "payload": map[string]string{"question": "q"}, "idempotency_key": "key"},
		"agent_reply":      {"message_id": "message", "payload": map[string]string{"answer": "a"}, "idempotency_key": "key"},
		"agent_task_graph": {"message_id": "message"}, "agent_inbox": {}, "agent_task_status": {"message_id": "message"}, "agent_await": {"message_id": "message"}, "agent_cancel": {"message_id": "message"},
	}
	for name, input := range valid {
		for _, field := range []string{"from", "token", "peer_id", "project_id", "unknown"} {
			t.Run(name+"/"+field, func(t *testing.T) {
				input[field] = "forged"
				defer delete(input, field)
				_, err := tools[name].Execute(t.Context(), coordInput(t, input))
				require.ErrorContains(t, err, "unknown field")
			})
		}
	}
	for _, input := range []string{`null`, `[]`, `{} {}`, `{"to":"backend","kind":"request","payload":{},"idempotency_key":""}`, `{"to":"backend","kind":"request","payload":{},"idempotency_key":"   "}`} {
		_, err := tools["agent_send"].Execute(t.Context(), json.RawMessage(input))
		require.Error(t, err)
	}
	_, err := tools["agent_send"].Execute(t.Context(), coordInput(t, map[string]any{"to": "backend", "kind": "request", "payload": strings.Repeat("x", 65536), "idempotency_key": "large"}))
	require.ErrorContains(t, err, "64 KiB")
	for _, limit := range []int{0, -1, 201} {
		_, err := tools["agent_inbox"].Execute(t.Context(), coordInput(t, map[string]any{"limit": limit}))
		require.Error(t, err)
	}
	for _, seconds := range []float64{-1, 30.1} {
		_, err := tools["agent_await"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": "id", "wait_seconds": seconds}))
		require.Error(t, err)
	}
	for _, tool := range tools {
		metadata, ok := tool.(interface {
			Schema() json.RawMessage
			Description() string
		})
		require.True(t, ok)
		require.True(t, json.Valid(metadata.Schema()))
		require.NotEmpty(t, metadata.Description())
		var schema map[string]any
		require.NoError(t, json.Unmarshal(metadata.Schema(), &schema))
		require.Equal(t, false, schema["additionalProperties"])
	}
}

func TestCoordinationToolsAwaitAndCancellation(t *testing.T) {
	store, _, backend, tools, _ := coordinationToolsFixture(t)
	sent := coordSendFixture(t, tools["agent_send"], "await-1")
	for _, seconds := range []float64{0, 0.01} {
		output, err := tools["agent_await"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID, "wait_seconds": seconds}))
		require.NoError(t, err)
		var result struct {
			Message  coordination.Message `json:"message"`
			TimedOut bool                 `json:"timed_out"`
		}
		require.NoError(t, json.Unmarshal(output, &result))
		require.True(t, result.TimedOut)
		require.Equal(t, sent.ID, result.Message.ID)
	}
	progressed := make(chan error, 1)
	go func() { _, err := store.Claim(t.Context(), backend.ID); progressed <- err }()
	output, err := tools["agent_await"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID, "after_revision": sent.Revision, "wait_seconds": 1}))
	require.NoError(t, err)
	require.NoError(t, <-progressed)
	require.Equal(t, coordination.Started, coordResultMessage(t, output).State)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	_, err = tools["agent_await"].Execute(ctx, coordInput(t, map[string]any{"message_id": sent.ID, "wait_seconds": 30}))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = tools["agent_cancel"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID, "expected_revision": sent.Revision}))
	require.ErrorIs(t, err, coordination.ErrConflict)
	output, err = tools["agent_cancel"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID, "payload": map[string]string{"reason": "frontend no longer needs the work"}}))
	require.NoError(t, err)
	require.Equal(t, coordination.Cancelled, coordResultMessage(t, output).State)
	_, err = tools["agent_cancel"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID}))
	require.Error(t, err, "repeated cancellation must not falsely succeed")
	output, err = tools["agent_await"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": sent.ID, "wait_seconds": 0}))
	require.NoError(t, err)
	var final struct {
		TimedOut bool `json:"timed_out"`
	}
	require.NoError(t, json.Unmarshal(output, &final))
	require.False(t, final.TimedOut)
}

func TestCoordinationToolsOutsiderCannotInspectOrCancel(t *testing.T) {
	store, _, _, tools, _ := coordinationToolsFixture(t)
	message := coordSendFixture(t, tools["agent_send"], "private")
	for _, project := range []string{"project", "another-project"} {
		peer, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: project, Name: "outsider", Role: "unrelated", Workspace: t.TempDir()})
		require.NoError(t, err)
		for _, tool := range NewCoordinationTools(store, peer.ID) {
			if tool.Name() == "agent_inbox" {
				output, err := tool.Execute(t.Context(), json.RawMessage(`{}`))
				require.NoError(t, err)
				require.NotContains(t, string(output), message.ID)
				continue
			}
			if tool.Name() == "agent_send" {
				continue
			}
			input := map[string]any{"message_id": message.ID}
			if tool.Name() == "agent_reply" {
				input["payload"] = map[string]string{"answer": "forged"}
				input["idempotency_key"] = "forged"
			}
			_, err := tool.Execute(t.Context(), coordInput(t, input))
			require.Error(t, err, fmt.Sprintf("%s must reject outsider", tool.Name()))
		}
	}
}

func TestInboxExposesDurableBudgetEscalationPages(t *testing.T) {
	store, front, back, frontTools, backTools := coordinationToolsFixture(t)
	for i := 0; i < coordination.MaxPeerQueue; i++ {
		coordSendFixture(t, frontTools["agent_send"], fmt.Sprintf("fill-%d", i))
	}
	for i := 0; i < 2; i++ {
		_, err := frontTools["agent_send"].Execute(t.Context(), coordInput(t, map[string]any{"to": "backend", "kind": "request", "payload": "overflow", "idempotency_key": fmt.Sprintf("overflow-%d", i)}))
		require.ErrorIs(t, err, coordination.ErrQueueFull)
	}
	var page struct {
		Escalations []coordination.Escalation `json:"escalations"`
		Cursor      int64                     `json:"next_escalation_cursor"`
		More        bool                      `json:"has_more_escalations"`
	}
	result, err := backTools["agent_inbox"].Execute(t.Context(), coordInput(t, map[string]any{"limit": 1}))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(result, &page))
	require.Len(t, page.Escalations, 1)
	require.True(t, page.More)
	require.True(t, page.Escalations[0].RequiresReview)
	result, err = backTools["agent_inbox"].Execute(t.Context(), coordInput(t, map[string]any{"limit": 1, "after_escalation": page.Cursor}))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(result, &page))
	require.Len(t, page.Escalations, 1)
	require.False(t, page.More)
	third, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: front.ProjectID, Name: "unrelated", Role: "observer", Workspace: t.TempDir()})
	require.NoError(t, err)
	hidden, err := store.ReadEscalations(t.Context(), third.ID, 0, 10)
	require.NoError(t, err)
	require.Empty(t, hidden)
	require.Equal(t, back.ID, page.Escalations[0].To)
}

func TestCoordinationToolsDependencyGraphAndEligibility(t *testing.T) {
	store, front, back, frontTools, _ := coordinationToolsFixture(t)
	first := coordSendFixture(t, frontTools["agent_send"], "prerequisite")
	output, err := frontTools["agent_send"].Execute(t.Context(), coordInput(t, map[string]any{"to": "backend", "kind": "request", "payload": "use result", "idempotency_key": "dependent", "depends_on": []string{first.ID}}))
	require.NoError(t, err)
	second := coordResultMessage(t, output)
	require.Equal(t, []string{first.ID}, second.DependsOn)
	readGraph := func() coordination.DependencyGraph {
		data, err := frontTools["agent_task_graph"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": second.ID, "limit": 100}))
		require.NoError(t, err)
		var graph coordination.DependencyGraph
		require.NoError(t, json.Unmarshal(data, &graph))
		return graph
	}
	graph := readGraph()
	require.Len(t, graph.Nodes, 2)
	require.Equal(t, "waiting", graph.Nodes[0].Readiness)
	claimed, err := store.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, first.ID, claimed.ID)
	_, err = store.Transition(t.Context(), back.ID, first.ID, claimed.Revision, "completed", json.RawMessage(`{"result":"ready"}`))
	require.NoError(t, err)
	graph = readGraph()
	require.Equal(t, "ready", graph.Nodes[0].Readiness)
	claimed, err = store.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, second.ID, claimed.ID)
	_, err = frontTools["agent_task_graph"].Execute(t.Context(), coordInput(t, map[string]any{"message_id": second.ID, "limit": 201}))
	require.Error(t, err)
	stranger, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "project", Name: "stranger", Role: "other", Workspace: t.TempDir()})
	require.NoError(t, err)
	for _, tool := range NewCoordinationTools(store, stranger.ID) {
		if tool.Name() == "agent_task_graph" {
			_, err = tool.Execute(t.Context(), coordInput(t, map[string]any{"message_id": second.ID}))
			require.Error(t, err)
		}
	}
	require.Equal(t, front.ID, second.From)
}
