package api

import (
	"context"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// PersistAgentSession is the shared CLI/API/ACP durable history commit. Title
// generation is deterministic so an optional provider call cannot consume the
// deadline needed to save a cancelled turn's partial history.
func PersistAgentSession(ctx context.Context, store session.Store, id string, a *agent.Agent) error {
	if store == nil {
		return fmt.Errorf("persist session: store unavailable")
	}
	metadata, err := store.Load(ctx, id)
	if err != nil {
		return fmt.Errorf("persist session: %w", err)
	}
	if metadata == nil {
		return fmt.Errorf("persist session: %w", session.ErrNotFound)
	}
	metadata.Model = a.Model()
	metadata.Messages = a.History()
	metadata.TurnCount = len(metadata.Messages)
	if metadata.Title == "" {
		for _, message := range metadata.Messages {
			if message.Role == "user" && message.Content != "" {
				metadata.AutoTitle(message.Content)
				break
			}
		}
	}
	if err := store.Save(ctx, metadata); err != nil {
		return fmt.Errorf("persist session: %w", err)
	}
	return nil
}
