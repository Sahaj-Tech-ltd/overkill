package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	bridgegw "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/bridge"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

func (s *Server) newAPIBridge(ctx context.Context) (*bridgegw.Bridge, error) {
	home, err := config.ConfigDir()
	if err != nil {
		return nil, err
	}
	router, err := gateway.NewSessionRouter(filepath.Join(home, "gateway-router.json"))
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	cfg := s.cfg.Gateways.Bridge
	limits := s.cfg.Gateways
	s.mu.RUnlock()
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = getCwd()
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	canonical, err := CanonicalSession(session.NewSession(workspace))
	if err != nil {
		return nil, fmt.Errorf("bridge workspace: %w", err)
	}
	if s.sessionStore == nil {
		return nil, fmt.Errorf("bridge: persistent session store required")
	}
	state := &bridgeState{server: s, workspace: canonical.Folder, lifecycle: ctx, cancels: make(map[string]context.CancelFunc)}
	dispatcher := gateway.NewDispatcher(&bridgeAgent{state: state}, router)
	dispatcher.ApplyGatewayLimits(gateway.GatewayLimitsCfg{MaxTextLen: limits.MaxTextLen, MaxImageBytes: limits.MaxImageBytes, RateLimitPerMin: limits.RateLimitPerMin, UpdateEveryMs: limits.UpdateEveryMs})
	dispatcher.CostTracker = s.costTracker
	listen := cfg.Listen
	if listen == "" {
		listen = "127.0.0.1:7799"
	}
	return bridgegw.New(dispatcher, cfg.Token, listen), nil
}

type bridgeState struct {
	server    *Server
	workspace string
	lifecycle context.Context
	sessionMu sync.Mutex
	mu        sync.Mutex
	cancels   map[string]context.CancelFunc
}

// bridgeAgent binds each turn/command to one immutable resolved chat session.
// The root adapter is only a provider of bound adapters; no live session is
// shared or swapped between concurrent chats.
type bridgeAgent struct {
	state *bridgeState
	sidMu sync.RWMutex
	sid   string
}

var _ gateway.AgentSender = (*bridgeAgent)(nil)
var _ gateway.SessionAgentProvider = (*bridgeAgent)(nil)

func (ba *bridgeAgent) AgentForSession(ctx context.Context, id string) (gateway.AgentSender, error) {
	if err := ba.state.lifecycle.Err(); err != nil {
		return nil, err
	}
	if err := ba.ensureSession(ctx, id); err != nil {
		return nil, err
	}
	bound := &bridgeAgent{state: ba.state, sid: id}
	// An idle resolution may assemble the runtime for pre-turn gateway callbacks.
	// Busy/stop controls must still resolve during an admitted run, without any
	// cache inspection or rebuild of that active agent.
	if _, rpcErr := ba.state.server.getOrCreateAgent(ctx, id); rpcErr != nil {
		if rpcErr.Code != InvalidRequest || !bound.IsBusy() {
			return nil, fmt.Errorf("bridge agent: %s", rpcErr.Message)
		}
	}
	return bound, nil
}

func (ba *bridgeAgent) ensureSession(ctx context.Context, id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") {
		return fmt.Errorf("bridge: invalid resolved session ID")
	}
	ba.state.sessionMu.Lock()
	defer ba.state.sessionMu.Unlock()
	existing, err := ba.state.server.sessionStore.Load(ctx, id)
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		return err
	}
	if existing != nil {
		return nil
	}
	metadata := session.NewSession(ba.state.workspace)
	metadata.ID = id
	ba.state.server.mu.RLock()
	metadata.Model = ba.state.server.cfg.Agent.DefaultModel
	metadata.Provider = ba.state.server.cfg.Agent.DefaultProvider
	ba.state.server.mu.RUnlock()
	if err := ba.state.server.sessionStore.Create(ctx, metadata); err != nil {
		return fmt.Errorf("bridge: persist session: %w", err)
	}
	return nil
}

func (ba *bridgeAgent) SetSessionID(id string) { ba.sidMu.Lock(); ba.sid = id; ba.sidMu.Unlock() }
func (ba *bridgeAgent) SessionID() string      { ba.sidMu.RLock(); defer ba.sidMu.RUnlock(); return ba.sid }
func (ba *bridgeAgent) cached() *agent.Agent {
	s := ba.state.server
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agents[ba.SessionID()]
}

func (ba *bridgeAgent) Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error) {
	sid := ba.SessionID()
	if sid == "" {
		return nil, fmt.Errorf("bridge: session binding required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopLifecycle := context.AfterFunc(ba.state.lifecycle, cancel)
	defer stopLifecycle()
	ba.state.mu.Lock()
	if ba.state.cancels[sid] != nil {
		ba.state.mu.Unlock()
		return nil, fmt.Errorf("bridge: session %s is busy", sid)
	}
	ba.state.cancels[sid] = cancel
	ba.state.mu.Unlock()
	defer func() { ba.state.mu.Lock(); delete(ba.state.cancels, sid); ba.state.mu.Unlock() }()
	params, err := json.Marshal(SendMessageParams{SessionID: sid, Message: in})
	if err != nil {
		return nil, err
	}
	result, rpcErr := ba.state.server.handleAgentSend(runCtx, params)
	if rpcErr != nil {
		return nil, fmt.Errorf("agent send: %s", rpcErr.Message)
	}
	response, ok := result.(*SendMessageResult)
	if !ok || response == nil {
		return nil, fmt.Errorf("bridge: unexpected agent response %T", result)
	}
	if response.Blocked {
		return nil, fmt.Errorf("agent blocked: %s", response.BlockReason)
	}
	events := make(chan agent.StreamEvent, 2)
	events <- agent.StreamEvent{Type: agent.EventToken, Content: response.Response}
	events <- agent.StreamEvent{Type: agent.EventDone}
	close(events)
	return events, nil
}

func (ba *bridgeAgent) InterruptSession() error {
	ba.state.mu.Lock()
	cancel := ba.state.cancels[ba.SessionID()]
	ba.state.mu.Unlock()
	if cancel == nil && ba.IsBusy() {
		return fmt.Errorf("bridge: active operation belongs to another entry point; cancel it there")
	}
	ba.Interrupt()
	return nil
}
func (ba *bridgeAgent) EmergencyStop() error {
	ba.state.server.mu.RLock()
	var outside bool
	for sid, a := range ba.state.server.agents {
		ba.state.mu.Lock()
		managed := ba.state.cancels[sid] != nil
		ba.state.mu.Unlock()
		outside = outside || (a.IsBusy() && !managed)
	}
	ba.state.server.mu.RUnlock()
	ba.EStop()
	if outside {
		return fmt.Errorf("bridge: stop requested, but active operations outside this bridge require cancellation at their entry point")
	}
	return nil
}

func (ba *bridgeAgent) Interrupt() {
	ba.state.mu.Lock()
	cancel := ba.state.cancels[ba.SessionID()]
	ba.state.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if a := ba.cached(); a != nil {
		a.Interrupt()
	}
}
func (ba *bridgeAgent) EStop() {
	ba.state.mu.Lock()
	for _, cancel := range ba.state.cancels {
		cancel()
	}
	ba.state.mu.Unlock()
	_, _ = ba.state.server.handleEStop(context.Background(), nil)
}
func (ba *bridgeAgent) IsBusy() bool {
	sid := ba.SessionID()
	ba.state.server.admissionMu.Lock()
	busy := ba.state.server.sessionOperations[sid]
	ba.state.server.admissionMu.Unlock()
	if busy {
		return true
	}
	if a := ba.cached(); a != nil {
		return a.IsBusy()
	}
	return false
}
func (ba *bridgeAgent) command(ctx context.Context, persist bool, fn func(*agent.Agent) error) error {
	if err := ba.state.lifecycle.Err(); err != nil {
		return err
	}
	s := ba.state.server
	sid := ba.SessionID()
	release, err := s.beginSessionOperation(sid)
	if err != nil {
		return err
	}
	defer release()
	a, rpcErr := s.getOrCreateAdmittedAgent(ctx, sid)
	if rpcErr != nil {
		return fmt.Errorf("bridge: %s", rpcErr.Message)
	}
	if err := fn(a); err != nil {
		return err
	}
	if persist {
		return s.saveSessionState(ctx, sid, a)
	}
	return nil
}
func (ba *bridgeAgent) Undo() (result string, err error) {
	err = ba.command(context.Background(), true, func(a *agent.Agent) error { var e error; result, e = a.Undo(); return e })
	return
}
func bridgeUnsupported(command string) error {
	return fmt.Errorf("bridge: %s is unsupported on this entry point", command)
}
func (ba *bridgeAgent) Retry(context.Context) (string, error) {
	return "", bridgeUnsupported("retry; resend the message")
}
func (ba *bridgeAgent) Fork(string) (string, error) {
	return "", bridgeUnsupported("fork; use session.fork")
}
func (ba *bridgeAgent) Snapshot(string) (string, error) { return "", bridgeUnsupported("git snapshot") }
func (ba *bridgeAgent) Rollback(int) (string, error)    { return "", bridgeUnsupported("git rollback") }
func (ba *bridgeAgent) Snapshots() (string, error)      { return "", bridgeUnsupported("git snapshots") }
func (ba *bridgeAgent) ExportHistory(string) (string, error) {
	return "", bridgeUnsupported("history export")
}
func (ba *bridgeAgent) SetGoal(ctx context.Context, text string) error {
	return ba.command(ctx, false, func(a *agent.Agent) error { return a.SetGoal(ctx, text) })
}
func (ba *bridgeAgent) GetGoal(ctx context.Context) (result string, err error) {
	err = ba.command(ctx, false, func(a *agent.Agent) error { var e error; result, e = a.GetGoal(ctx); return e })
	return
}
func (ba *bridgeAgent) PauseGoal(ctx context.Context) error {
	return ba.command(ctx, false, func(a *agent.Agent) error { return a.PauseGoal(ctx) })
}
func (ba *bridgeAgent) ResumeGoal(ctx context.Context) error {
	return ba.command(ctx, false, func(a *agent.Agent) error { return a.ResumeGoal(ctx) })
}
func (ba *bridgeAgent) ClearGoal(ctx context.Context) error {
	return ba.command(ctx, false, func(a *agent.Agent) error { return a.ClearGoal(ctx) })
}
func (ba *bridgeAgent) Compact(ctx context.Context) (result *agent.CompactResult, err error) {
	err = ba.command(ctx, true, func(a *agent.Agent) error { var e error; result, e = a.Compact(ctx); return e })
	return
}
func (ba *bridgeAgent) Steer(text string) string {
	if a := ba.cached(); a != nil {
		return a.Steer(text)
	}
	return "bridge: steering unavailable without an active session agent"
}
func (ba *bridgeAgent) SetQuestionFunc(fn agent.QuestionFunc) {
	if a := ba.cached(); a != nil {
		a.SetQuestionFunc(fn)
	}
}
func (ba *bridgeAgent) SetThinkingLevel(level string) {
	if a := ba.cached(); a != nil {
		a.SetThinkingLevel(level)
	}
}
func (ba *bridgeAgent) ThinkingLevel() string {
	if a := ba.cached(); a != nil {
		return a.ThinkingLevel()
	}
	return "unavailable"
}
func (ba *bridgeAgent) Mode() string {
	if a := ba.cached(); a != nil {
		return a.Mode()
	}
	return "unavailable"
}
