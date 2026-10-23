package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/web"
)

var canvasSessionID = regexp.MustCompile(`^[\w-]{1,128}$`)

// Canvas parameters are strings only; duplicate and case-folded keys must not
// silently change a session or mutation at this authenticated application edge.
func canvasParams(raw []byte, apply bool) (map[string]string, error) {
	if len(raw) > 1024*1024 {
		return nil, fmt.Errorf("canvas parameters too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("canvas parameter object required")
	}
	result := map[string]string{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok || (name != "session_id" && !(apply && name == "jsonl")) {
			return nil, fmt.Errorf("unknown canvas parameter")
		}
		if _, seen := result[name]; seen {
			return nil, fmt.Errorf("duplicate canvas parameter")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if len(value) == 0 || value[0] != '"' {
			return nil, fmt.Errorf("canvas parameter must be a string")
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return nil, err
		}
		result[name] = text
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("one canvas parameter object required")
	}
	if !canvasSessionID.MatchString(result["session_id"]) {
		return nil, fmt.Errorf("valid session_id required")
	}
	if apply && (result["jsonl"] == "" || len(result["jsonl"]) > web.CanvasMaxStateBytes) {
		return nil, fmt.Errorf("bounded jsonl mutation required")
	}
	return result, nil
}

func (s *Server) handleCanvas(ctx context.Context, method string, raw []byte) (any, *RPCError) {
	params, err := canvasParams(raw, method == "canvas.apply")
	if err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
	}
	if s.canvasStore == nil || s.sessionStore == nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Canvas storage is unavailable."}
	}
	sid := params["session_id"]
	// Reads inspect committed storage and never wait for a model turn/admission.
	// Mutations share admission with turns, deletion and other session mutations.
	if method != "canvas.state" {
		release, err := s.beginSessionOperation(sid)
		if err != nil {
			return nil, &RPCError{Code: InvalidRequest, Message: err.Error()}
		}
		defer release()
	}
	metadata, loadErr := s.sessionStore.Load(ctx, sid)
	if errors.Is(loadErr, session.ErrNotFound) || (loadErr == nil && (metadata == nil || metadata.ID != sid)) {
		return nil, &RPCError{Code: InvalidParams, Message: "Canvas chat was not found."}
	}
	if loadErr != nil {
		return nil, &RPCError{Code: InternalError, Message: "Canvas chat could not be read."}
	}

	var state web.CanvasState
	switch method {
	case "canvas.state":
		state, err = s.canvasStore.Get(ctx, sid)
	case "canvas.apply":
		state, err = s.canvasStore.Apply(ctx, sid, params["jsonl"])
	case "canvas.reset":
		state, err = s.canvasStore.Reset(ctx, sid)
	default:
		return nil, &RPCError{Code: MethodNotFound, Message: "Unknown canvas operation."}
	}
	if err != nil {
		if errors.Is(err, web.ErrCanvasInvalid) || errors.Is(err, web.ErrCanvasSessionNotFound) {
			return nil, &RPCError{Code: InvalidParams, Message: "Invalid canvas update or missing chat."}
		}
		return nil, &RPCError{Code: InternalError, Message: "Canvas storage operation failed."}
	}
	return map[string]any{"state": state}, nil
}
