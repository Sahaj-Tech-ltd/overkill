package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/google/uuid"
)

// Each pending question is one-shot and generation-bound. A delayed answer can
// never satisfy the next question in the same session. Polling never consumes it.
func (s *Server) wireQuestionFunc(a *agent.Agent, sessionID string) {
	a.SetQuestionFunc(func(ctx context.Context, q agent.Question) agent.Answer {
		if ctx.Err() != nil || strings.TrimSpace(q.Prompt) == "" || len(q.Prompt) > 8192 || len(q.Choices) > 32 {
			return agent.Answer{Cancel: true}
		}
		for _, choice := range q.Choices {
			if len(choice) > 1024 {
				return agent.Answer{Cancel: true}
			}
		}
		q.Choices = append([]string(nil), q.Choices...)
		answerCh := make(chan agent.Answer, 1)
		s.mu.Lock()
		if s.pendingQuestions == nil {
			s.pendingQuestions = make(map[string]chan agent.Answer)
		}
		if s.pendingQuestionIDs == nil {
			s.pendingQuestionIDs = make(map[string]string)
		}
		if s.pendingQuestions[sessionID] != nil {
			s.mu.Unlock()
			return agent.Answer{Cancel: true}
		}
		s.pendingQuestionMu.Lock()
		if s.pendingQuestionData == nil {
			s.pendingQuestionData = make(map[string]*agent.Question)
		}
		s.pendingQuestions[sessionID] = answerCh
		s.pendingQuestionIDs[sessionID] = uuid.NewString()
		s.pendingQuestionData[sessionID] = &q
		s.pendingQuestionMu.Unlock()
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.pendingQuestions[sessionID] != answerCh {
				return
			}
			s.pendingQuestionMu.Lock()
			defer s.pendingQuestionMu.Unlock()
			delete(s.pendingQuestions, sessionID)
			delete(s.pendingQuestionIDs, sessionID)
			delete(s.pendingQuestionData, sessionID)
		}()
		select {
		case answer := <-answerCh:
			return answer
		case <-ctx.Done():
			return agent.Answer{Cancel: true}
		}
	})
}

func decodeClarification(data []byte, target any) *RPCError {
	if len(data) == 0 || len(data) > 16384 {
		return &RPCError{Code: InvalidParams, Message: "bounded clarification params required"}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return &RPCError{Code: InvalidParams, Message: "invalid clarification params"}
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return &RPCError{Code: InvalidParams, Message: "one clarification object required"}
	}
	return nil
}

func (s *Server) handleClarifyPoll(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	// No params retains the trusted terminal's global poll. Desktop always binds
	// polling to its selected session through a fixed IPC operation.
	if len(params) > 0 && string(params) != "null" {
		if err := decodeClarification(params, &p); err != nil {
			return nil, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.pendingQuestionMu.RLock()
	defer s.pendingQuestionMu.RUnlock()
	for sid, q := range s.pendingQuestionData {
		if q != nil && s.pendingQuestions[sid] != nil && (p.SessionID == "" || sid == p.SessionID) {
			return map[string]any{"session_id": sid, "question_id": s.pendingQuestionIDs[sid], "question": q.Prompt, "choices": append([]string{}, q.Choices...)}, nil
		}
	}
	return nil, nil
}

func (s *Server) handleAgentAnswer(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		SessionID  string `json:"session_id"`
		QuestionID string `json:"question_id"`
		Text       string `json:"text"`
		Index      *int   `json:"index"`
		Cancel     bool   `json:"cancel"`
	}
	if err := decodeClarification(params, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "session_id is required"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.pendingQuestions[p.SessionID]
	if ch == nil {
		return nil, &RPCError{Code: InvalidParams, Message: "no pending question for session"}
	}
	if p.QuestionID == "" || p.QuestionID != s.pendingQuestionIDs[p.SessionID] {
		return nil, &RPCError{Code: InvalidParams, Message: "question_id is stale or missing"}
	}
	if p.Index == nil || *p.Index < -1 || len(p.Text) > 8192 {
		return nil, &RPCError{Code: InvalidParams, Message: "invalid question answer"}
	}
	s.pendingQuestionMu.Lock()
	defer s.pendingQuestionMu.Unlock()
	q := s.pendingQuestionData[p.SessionID]
	if q == nil {
		return nil, &RPCError{Code: InvalidParams, Message: "no pending question for session"}
	}
	text := p.Text
	if p.Cancel {
		if text != "" || *p.Index != -1 {
			return nil, &RPCError{Code: InvalidParams, Message: "cancel must not include an answer"}
		}
	} else if *p.Index >= 0 {
		if *p.Index >= len(q.Choices) || (text != "" && text != q.Choices[*p.Index]) {
			return nil, &RPCError{Code: InvalidParams, Message: "invalid question choice"}
		}
		text = q.Choices[*p.Index]
	} else if strings.TrimSpace(text) == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "answer text is required"}
	}
	select {
	case ch <- agent.Answer{Text: text, Index: *p.Index, Cancel: p.Cancel}:
		delete(s.pendingQuestions, p.SessionID)
		delete(s.pendingQuestionIDs, p.SessionID)
		delete(s.pendingQuestionData, p.SessionID)
		return map[string]string{"status": "answered"}, nil
	default:
		return nil, &RPCError{Code: InvalidParams, Message: "question already answered"}
	}
}
