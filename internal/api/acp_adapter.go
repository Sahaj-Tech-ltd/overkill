package api

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// ResolveACPSender reuses the same session service and runtime factory as HTTP
// and desktop clients. ACP never builds a separate, differently wired agent.
func (s *Server) ResolveACPSender(ctx context.Context, id string) (acp.Sender, error) {
	if id == "" {
		sess := session.NewSession(getCwd())
		s.mu.RLock()
		sess.Model, sess.Provider = s.cfg.Agent.DefaultModel, s.cfg.Agent.DefaultProvider
		s.mu.RUnlock()
		if err := s.sessionStore.Create(ctx, sess); err != nil {
			return nil, fmt.Errorf("acp: create session: %w", err)
		}
		id = sess.ID
	}
	sess, err := s.sessionStore.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("acp: load session: %w", err)
	}
	if sess == nil {
		return nil, fmt.Errorf("acp: session not found")
	}
	return &acpSessionSender{server: s, id: id, model: sess.Model}, nil
}

type acpSessionSender struct {
	server    *Server
	id, model string
}

func (s *acpSessionSender) SessionID() string { return s.id }
func (s *acpSessionSender) Model() string     { return s.model }

func (s *acpSessionSender) StreamACP(ctx context.Context, input string) (<-chan acp.AgentEvent, error) {
	release, err := s.server.beginSessionOperation(s.id)
	if err != nil {
		return nil, err
	}
	a, rpcErr := s.server.getOrCreateAdmittedAgent(ctx, s.id)
	if rpcErr != nil {
		release()
		return nil, fmt.Errorf("acp: %s", rpcErr.Message)
	}
	events, err := a.Stream(ctx, input)
	if err != nil {
		release()
		return nil, err
	}
	out := make(chan acp.AgentEvent, 64)
	go func() {
		defer close(out)
		defer release()
		emit := func(event acp.AgentEvent) bool {
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var terminal *acp.AgentEvent
		failed := false
		for {
			select {
			case <-ctx.Done():
				s.server.drainChannel(events)
				_ = s.server.saveSessionState(context.WithoutCancel(ctx), s.id, a)
				return
			case event, ok := <-events:
				if !ok {
					if err := s.server.saveSessionState(context.WithoutCancel(ctx), s.id, a); err != nil {
						emit(acp.AgentEvent{Type: acp.AgentEventError, Error: err})
						return
					}
					if !failed {
						if terminal != nil {
							emit(*terminal)
						} else {
							emit(acp.AgentEvent{Type: acp.AgentEventError, Error: fmt.Errorf("agent stream ended without completion")})
						}
					}
					return
				}
				mapped := acp.AgentEvent{}
				switch event.Type {
				case agent.EventToken:
					mapped.Type, mapped.Content = acp.AgentEventToken, event.Content
				case agent.EventToolStart, agent.EventToolOutput:
					mapped.Type, mapped.ToolName, mapped.Content = acp.AgentEventToolOutput, event.ToolName, event.ToolOutput
					if event.ToolCall != nil {
						mapped.ToolName, mapped.ToolArgs = event.ToolCall.Name, event.ToolCall.Arguments
					}
				case agent.EventError:
					mapped.Type, mapped.Error = acp.AgentEventError, event.Error
					failed = true
				case agent.EventDone:
					terminal = &acp.AgentEvent{Type: acp.AgentEventDone}
					continue
				default:
					continue
				}
				if !emit(mapped) {
					s.server.drainChannel(events)
					_ = s.server.saveSessionState(context.WithoutCancel(ctx), s.id, a)
					return
				}
			}
		}
	}()
	return out, nil
}

// Close releases service-owned agents and integrations. The database pool and
// application-wide services remain owned by the caller.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		if err := s.stopBridge(); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("bridge shutdown: %v", err)
		}
		s.stopBackground()
		if s.mcpManager != nil {
			s.mcpManager.Stop()
		}
		s.closeSessionRuntimes()
	})
}
