package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

func coordinationParams(data []byte, target any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || len(data) > 72*1024 || trimmed[0] != '{' {
		return fmt.Errorf("a bounded parameter object is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("exactly one parameter object is required")
	}
	return nil
}

func coordinationError(err error) *RPCError {
	switch {
	case errors.Is(err, coordination.ErrNotFound), errors.Is(err, coordination.ErrUnauthorized):
		return &RPCError{Code: InvalidParams, Message: "Agent or message was not found in this chat's correspondence."}
	case errors.Is(err, coordination.ErrConflict), errors.Is(err, coordination.ErrIdempotency):
		return &RPCError{Code: InvalidRequest, Message: "The agent identity, message revision or retry key conflicts with persisted state. Refresh before retrying."}
	default:
		return &RPCError{Code: InvalidRequest, Message: err.Error()}
	}
}

// These are authenticated application controls. Agent tools never call these
// registration endpoints, and sender identity always comes from a persisted
// session binding rather than a sender/project field in message content.
func (s *Server) handleCoordination(ctx context.Context, method string, data []byte) (any, *RPCError) {
	if s.coordinationStore == nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Agent coordination is unavailable."}
	}
	if method == "coordination.register" {
		var p struct {
			SessionID       string `json:"session_id"`
			Name            string `json:"name"`
			Role            string `json:"role"`
			RepositoryScope bool   `json:"repository_scope,omitempty"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		release, err := s.beginSessionOperation(p.SessionID)
		if err != nil {
			return nil, &RPCError{Code: InvalidRequest, Message: err.Error()}
		}
		defer release()
		// A background writer must stop before the workspace enters coordinated
		// ownership. Closing only after registration would leave a write window.
		s.mu.RLock()
		runtime := s.sessionRuntimes[p.SessionID]
		s.mu.RUnlock()
		if runtime != nil && runtime.Terminals != nil {
			if err := runtime.Terminals.Stop(); err != nil {
				return nil, &RPCError{Code: InvalidRequest, Message: "Cannot register while terminal shutdown is unconfirmed: " + err.Error()}
			}
		}
		var peer coordination.Peer
		if p.RepositoryScope {
			peer, err = s.coordinationStore.RegisterRepositorySessionPeer(ctx, p.SessionID, p.Name, p.Role)
		} else {
			peer, err = s.coordinationStore.RegisterSessionPeer(ctx, p.SessionID, p.Name, p.Role)
		}
		if err != nil {
			return nil, coordinationError(err)
		}
		// A chat may have run before registration. Rebuild its next agent with
		// immutable bound tools/role, while preserving the committed history.
		s.discardSessionRuntime(p.SessionID)
		return map[string]any{"peer": peer}, nil
	}
	if method == "coordination.peers" {
		var p struct {
			SessionID string `json:"session_id"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		if err := s.refreshRuntimePresence(ctx); err != nil {
			return nil, coordinationError(err)
		}
		peers, err := s.coordinationStore.LocalPeersForSession(ctx, p.SessionID)
		if err != nil {
			return nil, coordinationError(err)
		}
		var self *coordination.Peer
		for _, peer := range peers {
			if peer.SessionID == p.SessionID {
				copy := peer.Peer
				self = &copy
			}
		}
		return map[string]any{"self": self, "peers": peers}, nil
	}
	var identity struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "Invalid session parameters."}
	}
	peer, err := s.coordinationStore.PeerForSession(ctx, identity.SessionID)
	if err != nil {
		return nil, coordinationError(err)
	}
	switch method {
	case "coordination.graph":
		var p struct {
			SessionID string `json:"session_id"`
			MessageID string `json:"message_id"`
			Limit     *int   `json:"limit"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		limit := 100
		if p.Limit != nil {
			limit = *p.Limit
		}
		if limit < 1 || limit > 200 {
			return nil, &RPCError{Code: InvalidParams, Message: "limit must be 1..200"}
		}
		graph, err := s.coordinationStore.Graph(ctx, peer.ID, p.MessageID, limit)
		if err != nil {
			return nil, coordinationError(err)
		}
		return graph, nil
	case "coordination.message":
		var p struct {
			SessionID string `json:"session_id"`
			MessageID string `json:"message_id"`
			After     int64  `json:"after_revision"`
			Limit     *int   `json:"limit"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		input, _ := json.Marshal(struct {
			MessageID string `json:"message_id"`
			After     int64  `json:"after_revision"`
			Limit     *int   `json:"limit,omitempty"`
		}{p.MessageID, p.After, p.Limit})
		for _, tool := range tools.NewCoordinationTools(s.coordinationStore, peer.ID) {
			if tool.Name() == "agent_task_status" {
				result, err := tool.Execute(ctx, input)
				if err != nil {
					return nil, coordinationError(err)
				}
				return result, nil
			}
		}
		return nil, &RPCError{Code: InternalError, Message: "Task status unavailable."}
	case "coordination.review":
		var p struct {
			SessionID string                          `json:"session_id"`
			Input     coordination.ReviewRequestInput `json:"input"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		var presence struct {
			Input struct {
				Limits map[string]json.RawMessage `json:"limits"`
			} `json:"input"`
		}
		if err := json.Unmarshal(data, &presence); err != nil || len(presence.Input.Limits) != 2 || len(presence.Input.Limits["tokens"]) == 0 || len(presence.Input.Limits["cost_usd"]) == 0 || bytes.Equal(bytes.TrimSpace(presence.Input.Limits["tokens"]), []byte("null")) || bytes.Equal(bytes.TrimSpace(presence.Input.Limits["cost_usd"]), []byte("null")) {
			return nil, &RPCError{Code: InvalidParams, Message: "Explicit token and cost limits are required for review."}
		}
		result, err := s.coordinationStore.ReviewRequest(ctx, peer.ID, p.Input)
		if err != nil {
			return nil, coordinationError(err)
		}
		return result, nil
	case "coordination.usage":
		var p struct {
			SessionID string `json:"session_id"`
			MessageID string `json:"message_id"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		usage, err := s.coordinationStore.UsageForMessage(ctx, peer.ID, p.MessageID)
		if err != nil {
			return nil, coordinationError(err)
		}
		return map[string]any{"usage": usage}, nil
	case "coordination.escalations":
		var p struct {
			SessionID string `json:"session_id"`
			After     int64  `json:"after"`
			Limit     *int   `json:"limit"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		limit := 50
		if p.Limit != nil {
			limit = *p.Limit
		}
		if limit < 1 || limit > 200 || p.After < 0 {
			return nil, &RPCError{Code: InvalidParams, Message: "nonnegative cursor and limit 1..200 required"}
		}
		items, err := s.coordinationStore.ReadEscalations(ctx, peer.ID, p.After, limit+1)
		if err != nil {
			return nil, coordinationError(err)
		}
		more := len(items) > limit
		if more {
			items = items[:limit]
		}
		cursor := p.After
		if len(items) > 0 {
			cursor = items[len(items)-1].Sequence
		}
		return map[string]any{"escalations": items, "next_cursor": cursor, "has_more": more}, nil
	case "coordination.contract":
		var p struct {
			SessionID string          `json:"session_id"`
			Input     json.RawMessage `json:"input"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		output, err := tools.NewCoordinationContractTool(s.coordinationStore, peer.ID).Execute(ctx, p.Input)
		if err != nil {
			return nil, coordinationError(err)
		}
		return output, nil
	case "coordination.events":
		var p struct {
			SessionID string `json:"session_id"`
			After     int64  `json:"after"`
			Limit     int    `json:"limit"`
			WaitMS    int    `json:"wait_ms"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		if p.Limit == 0 {
			p.Limit = 50
		}
		if p.After < 0 || p.Limit < 1 || p.Limit > 200 || p.WaitMS < 0 || p.WaitMS > 25000 {
			return nil, &RPCError{Code: InvalidParams, Message: "Use a nonnegative cursor, limit 1..200 and wait_ms 0..25000."}
		}
		// The client persists its last processed cursor. Reading events never
		// claims work or acknowledges execution, so reconnect/retry is harmless.
		deadline := time.Now().Add(time.Duration(p.WaitMS) * time.Millisecond)
		for {
			events, err := s.coordinationStore.Events(ctx, peer.ID, p.After, p.Limit+1)
			if err != nil {
				return nil, coordinationError(err)
			}
			if len(events) > 0 || !time.Now().Before(deadline) {
				hasMore := len(events) > p.Limit
				if hasMore {
					events = events[:p.Limit]
				}
				next := p.After
				if len(events) > 0 {
					next = events[len(events)-1].Sequence
				}
				return map[string]any{"events": events, "next_cursor": next, "has_more": hasMore}, nil
			}
			timer := time.NewTimer(min(250*time.Millisecond, time.Until(deadline)))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, coordinationError(ctx.Err())
			case <-timer.C:
			}
		}
	case "coordination.messages":
		var p struct {
			SessionID string `json:"session_id"`
			BeforeID  string `json:"before_id"`
			Limit     int    `json:"limit"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		if p.Limit == 0 {
			p.Limit = 50
		}
		result, err := s.coordinationStore.Correspondence(ctx, peer.ID, p.BeforeID, p.Limit)
		if err != nil {
			return nil, coordinationError(err)
		}
		return result, nil
	case "coordination.send":
		var p struct {
			SessionID   string                    `json:"session_id"`
			UsageBudget *coordination.UsageLimits `json:"usage_budget,omitempty"`
			coordination.SendInput
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		var message coordination.Message
		var err error
		if p.UsageBudget != nil {
			message, err = s.coordinationStore.SendWithBudget(ctx, peer.ID, p.SendInput, *p.UsageBudget)
		} else {
			message, err = s.coordinationStore.Send(ctx, peer.ID, p.SendInput)
		}
		if err != nil {
			return nil, coordinationError(err)
		}
		return map[string]any{"message": message}, nil
	case "coordination.cancel":
		var p struct {
			SessionID string `json:"session_id"`
			MessageID string `json:"message_id"`
			Revision  int64  `json:"expected_revision"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		message, err := s.coordinationStore.Transition(ctx, peer.ID, p.MessageID, p.Revision, "cancelled", json.RawMessage(`{"reason":"user_cancelled"}`))
		if err != nil {
			return nil, coordinationError(err)
		}
		return map[string]any{"message": message}, nil
	default:
		return nil, &RPCError{Code: MethodNotFound, Message: "Unknown coordination operation."}
	}
}
