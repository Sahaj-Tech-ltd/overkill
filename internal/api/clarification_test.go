package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/stretchr/testify/require"
)

func pendingClarification(t *testing.T, s *Server, sid string) map[string]any {
	t.Helper()
	var question map[string]any
	require.Eventually(t, func() bool {
		raw, _ := json.Marshal(map[string]string{"session_id": sid})
		v, e := s.handleClarifyPoll(t.Context(), raw)
		require.Nil(t, e)
		if v == nil {
			return false
		}
		question = v.(map[string]any)
		return true
	}, time.Second, time.Millisecond)
	return question
}
func answerClarification(s *Server, sid, id, text string, index int, cancel bool) *RPCError {
	raw, _ := json.Marshal(map[string]any{"session_id": sid, "question_id": id, "text": text, "index": index, "cancel": cancel})
	_, err := s.handleAgentAnswer(context.Background(), raw)
	return err
}
func TestClarificationGenerationFenceAndExplicitCancel(t *testing.T) {
	s := &Server{}
	a := &agent.Agent{}
	s.wireQuestionFunc(a, "one")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan agent.Answer, 1)
	go func() {
		first <- a.AskQuestion(ctx, agent.Question{Prompt: "Choose color", Choices: []string{"red", "blue"}})
	}()
	q := pendingClarification(t, s, "one")
	id := q["question_id"].(string)
	require.NotEmpty(t, id)
	require.NotNil(t, answerClarification(s, "other", id, "red", 0, false))
	require.NotNil(t, answerClarification(s, "one", "stale", "red", 0, false))
	require.NotNil(t, answerClarification(s, "one", id, "forged", 0, false))
	require.Nil(t, answerClarification(s, "one", id, "", 1, false))
	select {
	case got := <-first:
		require.Equal(t, agent.Answer{Text: "blue", Index: 1}, got)
	case <-time.After(time.Second):
		t.Fatal("answer not delivered")
	}
	require.NotNil(t, answerClarification(s, "one", id, "blue", 1, false))
	second := make(chan agent.Answer, 1)
	go func() { second <- a.AskQuestion(ctx, agent.Question{Prompt: "Next?"}) }()
	next := pendingClarification(t, s, "one")
	nextID := next["question_id"].(string)
	require.NotEqual(t, id, nextID)
	require.NotNil(t, answerClarification(s, "one", id, "old draft", -1, false))
	require.NotNil(t, answerClarification(s, "one", nextID, "", -1, false))
	require.Nil(t, answerClarification(s, "one", nextID, "", -1, true))
	select {
	case got := <-second:
		require.True(t, got.Cancel)
	case <-time.After(time.Second):
		t.Fatal("cancel not delivered")
	}
	raw, _ := json.Marshal(map[string]string{"session_id": "one"})
	got, err := s.handleClarifyPoll(ctx, raw)
	require.Nil(t, err)
	require.Nil(t, got)
}
func TestClarificationCancellationAndConcurrentQuestionPreserveOwner(t *testing.T) {
	s := &Server{}
	a := &agent.Agent{}
	s.wireQuestionFunc(a, "one")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan agent.Answer, 1)
	go func() { done <- a.AskQuestion(ctx, agent.Question{Prompt: "Original"}) }()
	q := pendingClarification(t, s, "one")
	require.True(t, a.AskQuestion(t.Context(), agent.Question{Prompt: "Concurrent"}).Cancel)
	unchanged := pendingClarification(t, s, "one")
	require.Equal(t, q, unchanged)
	cancel()
	select {
	case answer := <-done:
		require.True(t, answer.Cancel)
	case <-time.After(time.Second):
		t.Fatal("context cancellation leaked waiter")
	}
	_, err := s.handleAgentAnswer(t.Context(), []byte(`{"session_id":"one","question_id":"forged","from":"peer"}`))
	require.NotNil(t, err)
}
