package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/audit"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// SessionRuntimeFactory binds dependencies to immutable persisted metadata.
// It must not call back into Server: agent creation holds the server lock.
type SessionRuntimeFactory func(context.Context, *session.Session) (*SessionRuntime, error)

// SessionRuntime owns resources used by one agent, never another session.
// Cleanup must be idempotent; shared stores belong to the application owner.
type SessionRuntime struct {
	WorkerMessages          *subagent.CommunicationStore
	BoundaryInbox           agent.BoundaryInbox
	Terminals               *terminal.Manager
	EvolutionFactory        func(providers.Provider) (*learning.Engine, error)
	HistoryCompactorFactory func(providers.Provider) (agent.HistoryCompactor, bool, error)
	// CheckCurrent runs under caller session admission before cached agent reuse.
	CheckCurrent          func(context.Context) (bool, error)
	ExtensionsManager     *extensions.Manager
	Tools                 *tools.Registry
	Hooks                 *hooks.Registry
	PostWriteVerifier     agent.PostWriteVerifier
	CheckpointSnapshotter agent.CheckpointSnapshotter
	FlowStore             agent.FlowStore
	CompletionAuditor     *audit.Auditor
	GoalStore             *agent.GoalStore
	SubagentManager       agent.SubagentManager
	Cleanup               func()
}

// CanonicalSession validates identity and resolves a real workspace directory
// without changing process cwd. A copy prevents factories mutating persisted
// model/provider/history state while binding their tools.
func CanonicalSession(sess *session.Session) (*session.Session, error) {
	if sess == nil || strings.TrimSpace(sess.ID) == "" || sess.ID == "." || sess.ID == ".." || strings.ContainsAny(sess.ID, "/\\") {
		return nil, fmt.Errorf("runtime: valid session ID required")
	}
	if sess.Folder == "" || !filepath.IsAbs(sess.Folder) {
		return nil, fmt.Errorf("runtime: session %q requires an absolute workspace", sess.ID)
	}
	folder, err := filepath.EvalSymlinks(sess.Folder)
	if err != nil {
		return nil, fmt.Errorf("runtime: resolve workspace: %w", err)
	}
	info, err := os.Stat(folder)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("runtime: workspace is not a directory")
	}
	copy := &session.Session{ID: sess.ID, Title: sess.Title, Folder: filepath.Clean(folder), ParentID: sess.ParentID, CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt, Model: sess.Model, Provider: sess.Provider, TokenCount: sess.TokenCount, CostUSD: sess.CostUSD, TurnCount: sess.TurnCount, Status: sess.Status, BranchedAtTurn: sess.BranchedAtTurn, Children: append([]string(nil), sess.Children...), Messages: append([]providers.Message(nil), sess.Messages...)}
	if sess.Metadata != nil {
		copy.Metadata = make(map[string]string, len(sess.Metadata))
		for key, value := range sess.Metadata {
			copy.Metadata[key] = value
		}
	}
	for i := range copy.Messages {
		copy.Messages[i].ToolCalls = append([]providers.ToolCall(nil), sess.Messages[i].ToolCalls...)
		copy.Messages[i].Attachments = append([]providers.Attachment(nil), sess.Messages[i].Attachments...)
		for j := range copy.Messages[i].Attachments {
			copy.Messages[i].Attachments[j].Data = append([]byte(nil), sess.Messages[i].Attachments[j].Data...)
		}
	}
	return copy, nil
}

func (s *Server) closeSessionRuntimes() {
	s.mu.Lock()
	agents := s.agents
	resources := s.sessionRuntimes
	s.agents = make(map[string]*agent.Agent)
	s.sessionRuntimes = make(map[string]*SessionRuntime)
	s.mu.Unlock()
	for _, a := range agents {
		a.Shutdown()
	}
	for _, runtime := range resources {
		if runtime.Cleanup != nil {
			runtime.Cleanup()
		}
	}
}

// Caller holds session admission, covering both persistence and cache rebuild.
func (s *Server) discardSessionRuntime(id string) {
	s.mu.Lock()
	a, runtime := s.agents[id], s.sessionRuntimes[id]
	delete(s.agents, id)
	delete(s.sessionRuntimes, id)
	s.mu.Unlock()
	if a != nil {
		a.Shutdown()
	}
	if runtime != nil && runtime.Cleanup != nil {
		runtime.Cleanup()
	}
}
