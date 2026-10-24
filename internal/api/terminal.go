package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
)

// These routes are human-client controls. Worker tools bind their own owner
// directly and never accept a human-control flag from model input.
func (s *Server) handleTerminal(ctx context.Context, method string, params json.RawMessage) (interface{}, *RPCError) {
	var in struct {
		SessionID       string `json:"session_id"`
		ControlRevision uint64 `json:"control_revision"`
		Sequence        uint64 `json:"sequence"`
		ID              string `json:"id"`
		Data            string `json:"data"`
		Bytes           []byte `json:"bytes"`
		Cursor          uint64 `json:"cursor"`
		Limit           int    `json:"limit"`
		WaitMS          int    `json:"wait_ms"`
		Rows            uint16 `json:"rows"`
		Cols            uint16 `json:"cols"`
		Release         bool   `json:"release"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(params)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "Invalid terminal request: " + err.Error()}
	}
	if in.SessionID == "" || len(in.SessionID) > 128 || strings.ContainsAny(in.SessionID, "/\\") {
		return nil, &RPCError{Code: InvalidParams, Message: "session_id is required"}
	}
	if in.WaitMS < 0 || in.WaitMS > 1000 || in.Limit < 0 || in.Limit > 65536 {
		return nil, &RPCError{Code: InvalidParams, Message: "Invalid terminal output bounds"}
	}
	// Never rebuild a live runtime from a terminal polling request. The normal
	// chat admission path owns construction and coordination rebinding.
	s.mu.RLock()
	bound := s.sessionRuntimes[in.SessionID]
	s.mu.RUnlock()
	if bound == nil || bound.Terminals == nil {
		if method == "terminal.list" {
			return map[string]any{"available": false, "terminals": []terminal.Info{}, "reason": "No live terminal service for this chat. Start a chat turn to initialize it."}, nil
		}
		return nil, &RPCError{Code: InvalidRequest, Message: "Terminal service is unavailable; a backend restart interrupts old terminal handles."}
	}
	manager := bound.Terminals
	owner := terminal.Owner{Session: in.SessionID, Worker: "human-client"}
	var err error
	switch method {
	case "terminal.list":
		return map[string]any{"available": terminal.Supported(), "terminals": manager.List(owner, true)}, nil
	case "terminal.read":
		chunk, err := manager.Read(ctx, owner, in.ID, true, in.Cursor, in.Limit, time.Duration(in.WaitMS)*time.Millisecond)
		if err == nil {
			return chunk, nil
		}
		return nil, &RPCError{Code: InvalidRequest, Message: err.Error()}
	case "terminal.input":
		data := []byte(in.Data)
		if len(in.Bytes) > 0 {
			if in.Data != "" {
				return nil, &RPCError{Code: InvalidParams, Message: "Supply one input encoding"}
			}
			data = in.Bytes
		}
		receipt, writeErr := manager.WriteInput(ctx, owner, in.ID, true, in.ControlRevision, in.Sequence, data)
		if writeErr == nil {
			return receipt, nil
		}
		err = writeErr
	case "terminal.resize":
		err = manager.ResizeAt(owner, in.ID, true, in.ControlRevision, in.Rows, in.Cols)
	case "terminal.control":
		value, controlErr := manager.TakeControlInfo(owner, in.ID, true, in.ControlRevision, in.Release)
		if controlErr == nil {
			return value, nil
		}
		err = controlErr
	case "terminal.interrupt":
		value, writeErr := manager.WriteInput(ctx, owner, in.ID, true, in.ControlRevision, in.Sequence, []byte{3})
		if writeErr == nil {
			return value, nil
		}
		err = writeErr
	case "terminal.close":
		err = manager.CloseAt(owner, in.ID, true, in.ControlRevision)
	case "terminal.handoff":
		value, handoffErr := manager.HandoffToParent(ctx, owner, in.ID, true, in.ControlRevision)
		if handoffErr == nil {
			return value, nil
		}
		err = handoffErr
	case "terminal.forget":
		err = manager.Forget(owner, in.ID, true)
	default:
		err = fmt.Errorf("unknown terminal operation")
	}
	if err != nil {
		return nil, &RPCError{Code: InvalidRequest, Message: err.Error()}
	}
	return map[string]bool{"ok": true}, nil
}
