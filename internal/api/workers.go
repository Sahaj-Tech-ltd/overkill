package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// Human-client controls address only the cached, application-owned chat
// mailbox. Polling never creates an agent, model call or new incarnation.
func (s *Server) handleWorkers(ctx context.Context, method string, params json.RawMessage) (any, *RPCError) {
	var in struct {
		SessionID string          `json:"session_id"`
		Input     json.RawMessage `json:"input,omitempty"`
	}
	d := json.NewDecoder(strings.NewReader(string(params)))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
	}
	if in.SessionID == "" || len(in.SessionID) > 128 || strings.ContainsAny(in.SessionID, "/\\") {
		return nil, &RPCError{Code: InvalidParams, Message: "Choose a valid chat"}
	}
	s.mu.RLock()
	bound := s.sessionRuntimes[in.SessionID]
	s.mu.RUnlock()
	if bound == nil || bound.WorkerMessages == nil {
		if method == "workers.list" {
			return map[string]any{"available": false, "workers": []subagent.WorkerRecord{}, "reason": "No worker inbox is initialized for this chat. Start a chat turn to initialize it."}, nil
		}
		return nil, &RPCError{Code: InvalidRequest, Message: "Worker inbox unavailable for this chat"}
	}
	if method == "workers.list" {
		workers, err := bound.WorkerMessages.Workers(ctx, true)
		if err != nil {
			return nil, &RPCError{Code: InternalError, Message: err.Error()}
		}
		handoffs, err := bound.WorkerMessages.TerminalHandoffs(ctx)
		if err != nil {
			return nil, &RPCError{Code: InternalError, Message: err.Error()}
		}
		return map[string]any{"available": true, "workers": workers, "terminal_handoffs": handoffs}, nil
	}
	var operation struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(in.Input, &operation); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "Worker operation required"}
	}
	expected := map[string]string{"workers.inbox": "inbox", "workers.send": "send", "workers.acknowledge": "acknowledge"}[method]
	if expected == "" || operation.Action != expected {
		return nil, &RPCError{Code: InvalidParams, Message: "Worker operation mismatch"}
	}
	result, err := tools.NewWorkerMessageTool(bound.WorkerMessages, subagent.ParentWorker).Execute(ctx, in.Input)
	if err != nil {
		return nil, &RPCError{Code: InvalidRequest, Message: err.Error()}
	}
	return result, nil
}
