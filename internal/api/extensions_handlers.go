package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
)

func (s *Server) handleExtensions(ctx context.Context, method string, data []byte) (any, *RPCError) {
	var params struct {
		SessionID string          `json:"session_id"`
		Kind      extensions.Kind `json:"kind,omitempty"`
		ID        string          `json:"id,omitempty"`
		Enabled   *bool           `json:"enabled,omitempty"`
	}
	if len(data) == 0 || string(data) == "null" {
		data = []byte(`{}`)
	}
	if err := coordinationParams(data, &params); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
	}
	manager := s.extensionsMgr
	if params.SessionID != "" {
		if _, rpcErr := s.getOrCreateAgent(ctx, params.SessionID); rpcErr != nil {
			return nil, rpcErr
		}
		s.mu.RLock()
		runtime := s.sessionRuntimes[params.SessionID]
		if runtime != nil {
			manager = runtime.ExtensionsManager
		}
		s.mu.RUnlock()
	}
	if manager == nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Extension inventory unavailable."}
	}
	if method == "extensions.toggle" {
		if params.ID == "" || params.Enabled == nil {
			return nil, &RPCError{Code: InvalidParams, Message: "kind, id and enabled are required."}
		}
		var err error
		if *params.Enabled {
			err = manager.Enable(params.Kind, params.ID)
		} else {
			err = manager.Disable(params.Kind, params.ID)
		}
		if err != nil {
			code := InvalidRequest
			if errors.Is(err, extensions.ErrNotFound) {
				code = InvalidParams
			}
			return nil, &RPCError{Code: code, Message: err.Error()}
		}
		item, err := manager.Get(params.Kind, params.ID)
		if err != nil {
			return nil, &RPCError{Code: InternalError, Message: err.Error()}
		}
		return map[string]any{"extension": item}, nil
	}
	items, err := manager.List()
	if err != nil {
		return nil, &RPCError{Code: InvalidRequest, Message: fmt.Sprintf("Extension inventory incomplete: %s", err)}
	}
	// Returning typed records also keeps argument/environment secrets out of MCP
	// inventory: adapters expose status, never executable configuration payloads.
	if items == nil {
		items = []extensions.Extension{}
	}
	return map[string]any{"extensions": items, "session_id": params.SessionID}, nil
}
