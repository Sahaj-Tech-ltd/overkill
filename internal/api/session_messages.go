package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

type SessionMessagesResult struct {
	Snapshot string              `json:"snapshot,omitempty"`
	ID       string              `json:"id"`
	Messages []providers.Message `json:"messages"`
}

// handleSessionMessages reads an independent committed snapshot where supported.
// Other stores retain admission so mutable live history cannot leak through.
func (s *Server) handleSessionMessages(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		ID string `json:"id"`
	}
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "id is required"}
	}
	if reader, ok := s.sessionStore.(session.HistoryReader); ok {
		messages, err := reader.LoadMessages(ctx, p.ID)
		if errors.Is(err, session.ErrNotFound) {
			return nil, &RPCError{Code: InvalidParams, Message: "session not found"}
		}
		if err != nil {
			return nil, &RPCError{Code: InternalError, Message: fmt.Sprintf("failed to read saved history: %v", err)}
		}
		if messages == nil {
			messages = []providers.Message{}
		}
		return &SessionMessagesResult{ID: p.ID, Messages: messages, Snapshot: "committed"}, nil
	}
	release, err := s.beginSessionOperation(p.ID)
	if err != nil {
		return nil, &RPCError{Code: InvalidRequest, Message: err.Error()}
	}
	defer release()
	sess, err := s.sessionStore.Load(ctx, p.ID)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: fmt.Sprintf("failed to load session: %v", err)}
	}
	if sess == nil {
		return nil, &RPCError{Code: InvalidParams, Message: "session not found"}
	}
	messages := sess.Messages
	if messages == nil {
		messages = []providers.Message{}
	}
	return &SessionMessagesResult{ID: p.ID, Messages: messages}, nil
}
