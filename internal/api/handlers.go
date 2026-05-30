package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/events"
	eventsinks "github.com/Sahaj-Tech-ltd/overkill/internal/events/sinks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hotreload"
	"github.com/Sahaj-Tech-ltd/overkill/internal/input"
	"github.com/Sahaj-Tech-ltd/overkill/internal/prompt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/prompt/chips"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/rewriter"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

// handleAgentSend runs the agent loop for a session and returns the result.
// The agent is created lazily on first use for the given session ID.
func (s *Server) handleAgentSend(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p SendMessageParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.Message == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "message is required"}
	}

	// Default to cwd if no session specified.
	sessionID := p.SessionID
	if sessionID == "" {
		sess := session.NewSession(getCwd())
		if createErr := s.sessionStore.Create(ctx, sess); createErr != nil {
			return nil, &RPCError{Code: InternalError, Message: fmt.Sprintf("failed to create session: %v", createErr)}
		}
		sessionID = sess.ID
	}

	a, rpcErr := s.getOrCreateAgent(ctx, sessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}

	// Discard any idle-time speculation — user is now actively engaging.
	a.DiscardSpeculation()

	// Enforce a 5-minute execution timeout to prevent runaway agent loops.
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	result, err := a.Run(runCtx, p.Message)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}

	// Persist session state (message count, model, cost).
	s.saveSessionState(ctx, sessionID, a)

	// P3: credit assignment — fold per-turn actions into the in-memory
	// analyzer so the TUI can surface lift/frequency stats (§8.6 Wave 4).
	s.recordCreditTurn(a, s.cfg.Agent.DefaultProvider, s.cfg.Agent.DefaultModel)

	// Mirror CLI path: optional non-blocking sync push after each turn.
	s.maybeAutoPush(sessionID)

	return &SendMessageResult{
		Response:    result.Response,
		ToolCalls:   result.ToolCalls,
		TotalTokens: result.TotalTokens,
		Steps:       result.Steps,
		Model:       result.Model,
		Blocked:     result.Blocked,
		BlockReason: result.BlockReason,
	}, nil
}

// handleEStop fires an emergency stop on ALL running agents. No session ID
// required — this is the "kill everything" command for the TUI command palette.
func (s *Server) handleEStop(_ context.Context, _ []byte) (interface{}, *RPCError) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, a := range s.agents {
		a.EStop()
		count++
	}
	return map[string]interface{}{
		"status":  "estopped",
		"stopped": count,
	}, nil
}

// handleAgentAbort fires an emergency stop on the agent running for the given session.
func (s *Server) handleAgentAbort(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p AbortParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "session_id is required"}
	}

	s.mu.RLock()
	a, ok := s.agents[p.SessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, &RPCError{Code: InvalidParams, Message: "no active agent for session"}
	}

	a.EStop()
	return map[string]string{"status": "aborted"}, nil
}

// handleAgentUndo removes the last user→assistant exchange from the agent's
// history. Used by the TUI command palette Undo command.
func (s *Server) handleAgentUndo(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p AbortParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "session_id is required"}
	}

	s.mu.RLock()
	a, ok := s.agents[p.SessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, &RPCError{Code: InvalidParams, Message: "no active agent for session"}
	}

	removed := a.PopLastExchange()
	return map[string]interface{}{
		"status":       "undone",
		"removed_text": removed,
	}, nil
}

// handleSessionList returns all sessions from the store.
func (s *Server) handleSessionList(ctx context.Context, _ []byte) (interface{}, *RPCError) {
	if s.sessionStore == nil {
		return &SessionListResult{Sessions: []SessionInfo{}}, nil
	}
	sessions, err := s.sessionStore.List(ctx, session.ListOptions{})
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}

	infos := make([]SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		infos = append(infos, toSessionInfo(sess))
	}
	return &SessionListResult{Sessions: infos}, nil
}

// handleSessionCreate creates a new session in the store.
func (s *Server) handleSessionCreate(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p SessionCreateParams
	if len(params) > 0 {
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
	}

	folder := p.Folder
	if folder == "" {
		folder = getCwd()
	}

	sess := session.NewSession(folder)
	if p.Title != "" {
		sess.Title = p.Title
	}
	if p.Model != "" {
		sess.Model = p.Model
	} else {
		sess.Model = s.cfg.Agent.DefaultModel
	}
	if p.Provider != "" {
		sess.Provider = p.Provider
	} else {
		sess.Provider = s.cfg.Agent.DefaultProvider
	}

	if err := s.sessionStore.Create(ctx, sess); err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}

	return &SessionCreateResult{Session: toSessionInfo(sess)}, nil
}

// handleSessionDelete removes a session by ID.
func (s *Server) handleSessionDelete(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p SessionDeleteParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "id is required"}
	}

	// Shut down any running agent for this session.
	s.mu.Lock()
	if a, ok := s.agents[p.ID]; ok {
		a.Shutdown()
		delete(s.agents, p.ID)
	}
	s.mu.Unlock()

	if err := s.sessionStore.Delete(ctx, p.ID); err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}
	return map[string]string{"status": "deleted"}, nil
}

// handleAgentFork creates a new session branched from an existing session.
// It forks the parent's message history at the current turn and creates a
// child session the user can explore independently.
func (s *Server) handleAgentFork(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p ForkParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "session_id is required"}
	}

	brancher, ok := s.sessionStore.(session.Brancher)
	if !ok {
		return nil, &RPCError{Code: InternalError, Message: "session store does not support branching"}
	}

	// Load parent to find the current turn count.
	parent, err := s.sessionStore.Load(ctx, p.SessionID)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: fmt.Sprintf("load parent: %v", err)}
	}
	if parent == nil {
		return nil, &RPCError{Code: InvalidParams, Message: "session not found"}
	}

	child, err := brancher.Branch(ctx, p.SessionID, parent.TurnCount)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: fmt.Sprintf("fork: %v", err)}
	}

	// Override the auto-generated title if the caller supplied a name.
	if p.Name != "" {
		child.Title = p.Name
		_ = s.sessionStore.Save(ctx, child)
	}

	return &ForkResult{Session: toSessionInfo(child)}, nil
}

// handleConfigGet returns the current configuration.
func (s *Server) handleConfigGet(_ context.Context, _ []byte) (interface{}, *RPCError) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agentInfo := map[string]interface{}{
		"default_provider": s.cfg.Agent.DefaultProvider,
		"default_model":    s.cfg.Agent.DefaultModel,
		"max_turns":        s.cfg.Agent.MaxTurns,
		"spec_driven":      s.cfg.Agent.SpecDriven,
	}
	uiInfo := map[string]interface{}{
		"animations": s.cfg.UI.Animations,
		"theme":      s.cfg.UI.Theme,
	}
	thinkingInfo := map[string]interface{}{
		"level":         string(s.cfg.Thinking.Level),
		"budget_tokens": s.cfg.Thinking.Level.BudgetTokens(),
	}
	result := &ConfigGetResult{
		Version:      s.cfg.Version,
		Agent:        agentInfo,
		UI:           uiInfo,
		Thinking:     thinkingInfo,
		SystemPrompt: s.cfg.Agent.SystemPrompt,
	}
	if s.cfg.Security.AutonomyLevel != "" {
		result.Security = map[string]interface{}{
			"autonomy_level":  s.cfg.Security.AutonomyLevel,
			"sandbox_enabled": s.cfg.Security.SandboxEnabled,
		}
	}
	result.Session = map[string]interface{}{
		"auto_title": s.cfg.Session.AutoTitle,
	}
	if s.cfg.Cost.DailyLimitUSD > 0 {
		result.Cost = map[string]interface{}{
			"daily_limit": s.cfg.Cost.DailyLimitUSD,
		}
	}
	if s.cfg.Compaction.SoftTriggerPercent > 0 {
		result.Compaction = map[string]interface{}{
			"threshold": s.cfg.Compaction.HardTriggerPercent,
		}
	}
	return result, nil
}

// handleConfigUpdate patches the current configuration and persists it.
func (s *Server) handleConfigUpdate(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p ConfigUpdateParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if agentPatch, ok := p.Patch["agent"].(map[string]interface{}); ok {
		if v, exists := agentPatch["default_provider"]; exists {
			if str, ok := v.(string); ok {
				s.cfg.Agent.DefaultProvider = str
			}
		}
		if v, exists := agentPatch["default_model"]; exists {
			if str, ok := v.(string); ok {
				s.cfg.Agent.DefaultModel = str
			}
		}
		if v, exists := agentPatch["max_turns"]; exists {
			if num, ok := toInt(v); ok {
				s.cfg.Agent.MaxTurns = num
			}
		}
		if v, exists := agentPatch["spec_driven"]; exists {
			if b, ok := v.(bool); ok {
				s.cfg.Agent.SpecDriven = b
			}
		}
	}
	if uiPatch, ok := p.Patch["ui"].(map[string]interface{}); ok {
		if v, exists := uiPatch["animations"]; exists {
			if b, ok := v.(bool); ok {
				s.cfg.UI.Animations = b
			}
		}
		if v, exists := uiPatch["theme"]; exists {
			if str, ok := v.(string); ok {
				s.cfg.UI.Theme = str
			}
		}
	}
	if secPatch, ok := p.Patch["security"].(map[string]interface{}); ok {
		if v, exists := secPatch["autonomy_level"]; exists {
			if str, ok := v.(string); ok {
				s.cfg.Security.AutonomyLevel = str
			}
		}
		if v, exists := secPatch["sandbox_enabled"]; exists {
			if b, ok := v.(bool); ok {
				s.cfg.Security.SandboxEnabled = b
			}
		}
	}
	if sessPatch, ok := p.Patch["session"].(map[string]interface{}); ok {
		if v, exists := sessPatch["auto_title"]; exists {
			if b, ok := v.(bool); ok {
				s.cfg.Session.AutoTitle = b
			}
		}
	}
	if thinkingPatch, ok := p.Patch["thinking"].(map[string]interface{}); ok {
		if v, exists := thinkingPatch["level"]; exists {
			if str, ok := v.(string); ok {
				tl := config.ThinkingLevel(str)
				if tl.Valid() {
					s.cfg.Thinking.Level = tl
					// Apply to all active agents immediately.
					for _, a := range s.agents {
						a.SetThinkingLevel(str)
					}
				}
			}
		}
	}
	if v, exists := p.Patch["system_prompt"].(string); exists {
		if v != s.cfg.Agent.SystemPrompt {
			s.cfg.Agent.SystemPrompt = v
			// System prompt changed — clear history on all active sessions.
			// The next turn will pick up the new system prompt.
			for sid, a := range s.agents {
				a.ClearHistory()
				log.Printf("api: system prompt changed — cleared history for session %s", sid)
			}
		}
	}

	// Persist to disk.
	cfgPath, err := config.ConfigPath()
	if err == nil {
		_ = s.cfg.Save(cfgPath)
	}

	return s.handleConfigGet(ctx, nil)
}

// handleProvidersList returns the configured providers with their model lists.
func (s *Server) handleProvidersList(_ context.Context, _ []byte) (interface{}, *RPCError) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	provs := make([]ProviderInfo, 0, len(s.cfg.Providers))
	for _, pc := range s.cfg.Providers {
		models := s.resolveProviderModels(pc)
		mInfos := make([]ModelInfo, 0, len(models))
		for _, m := range models {
			mInfos = append(mInfos, toModelInfo(&m))
		}
		provs = append(provs, ProviderInfo{
			Name:   pc.Name,
			Type:   pc.Type,
			Models: mInfos,
		})
	}
	return &ProvidersListResult{Providers: provs}, nil
}

// handleModelsList returns models for a specific provider.
func (s *Server) handleModelsList(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p ModelsListParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.Provider == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "provider is required"}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, pc := range s.cfg.Providers {
		if pc.Name != p.Provider {
			continue
		}
		models := s.resolveProviderModels(pc)
		mInfos := make([]ModelInfo, 0, len(models))
		for _, m := range models {
			mInfos = append(mInfos, toModelInfo(&m))
		}
		return &ModelsListResult{Models: mInfos}, nil
	}
	return nil, &RPCError{Code: InvalidParams, Message: fmt.Sprintf("provider %q not found", p.Provider)}
}

// handleStatusHealth returns a simple health check.
func (s *Server) handleStatusHealth(_ context.Context, _ []byte) (interface{}, *RPCError) {
	return &HealthResult{Status: "ok", Version: s.cfg.Version}, nil
}

// handleConfigExists checks whether the Overkill config file exists on disk.
func (s *Server) handleConfigExists(_ context.Context, _ []byte) (interface{}, *RPCError) {
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return &ConfigExistsResult{Exists: false}, nil
	}
	_, statErr := os.Stat(cfgPath)
	return &ConfigExistsResult{Exists: statErr == nil}, nil
}

// handleConfigCreate accepts a partial config payload and writes the initial
// config file. Used by the onboarding wizard to persist the user's choices.
func (s *Server) handleConfigCreate(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p ConfigCreateParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}

	// Build a config from defaults, then overlay the provided values.
	cfg := config.Default()

	// Providers + models.
	if len(p.Providers) > 0 || len(p.Models) > 0 {
		cfg.Providers = nil
		for _, pc := range p.Providers {
			// Overlay per-provider models if the top-level models list is set.
			if len(p.Models) > 0 && len(pc.Models) == 0 {
				pc.Models = p.Models
			}
			cfg.Providers = append(cfg.Providers, pc)
			// Use the first provider as the default.
			if cfg.Agent.DefaultProvider == "" || cfg.Agent.DefaultProvider == config.Default().Agent.DefaultProvider {
				cfg.Agent.DefaultProvider = pc.Name
				if len(pc.Models) > 0 {
					cfg.Agent.DefaultModel = pc.Models[0].ID
				}
			}
		}
	}

	// Gateways.
	if p.Gateways != nil {
		if p.Gateways.Discord != nil {
			cfg.Gateways.Discord = config.DiscordConfig{
				Enabled:         p.Gateways.Discord.Enabled,
				BotToken:        p.Gateways.Discord.BotToken,
				NotifyChannelID: p.Gateways.Discord.NotifyChannelID,
			}
		}
		if p.Gateways.Telegram != nil {
			cfg.Gateways.Telegram = config.TelegramConfig{
				Enabled:      p.Gateways.Telegram.Enabled,
				BotToken:     p.Gateways.Telegram.BotToken,
				NotifyChatID: p.Gateways.Telegram.NotifyChatID,
			}
		}
		if p.Gateways.WhatsApp != nil {
			cfg.Gateways.WhatsApp = config.WhatsAppConfig{
				Enabled: p.Gateways.WhatsApp.Enabled,
				Backend: p.Gateways.WhatsApp.Backend,
			}
		}
	}

	// Write the config file.
	cfgPath := ""
	if path, err := config.ConfigPath(); err == nil {
		cfgPath = path
	}
	if cfgPath == "" {
		return nil, &RPCError{Code: InternalError, Message: "cannot determine config path"}
	}

	if err := cfg.Save(cfgPath); err != nil {
		return nil, &RPCError{Code: InternalError, Message: fmt.Sprintf("failed to save config: %v", err)}
	}

	// Update the in-memory config so the running server reflects the change.
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	return map[string]string{"status": "created", "path": cfgPath}, nil
}

// handleAgentSubagents returns the list of currently active subagents.
func (s *Server) handleAgentSubagents(_ context.Context, _ []byte) (interface{}, *RPCError) {
	if s.subagentManager == nil {
		return &SubagentListResult{Subagents: []SubagentInfo{}}, nil
	}
	children := s.subagentManager.ActiveChildren()
	infos := make([]SubagentInfo, 0, len(children))
	for _, c := range children {
		infos = append(infos, SubagentInfo{
			Name:      c.Goal,
			Status:    c.Status,
			Model:     c.Model,
			ElapsedMs: 0, // can't easily compute here
		})
	}
	return &SubagentListResult{Subagents: infos}, nil
}

// handleGatewayTest tests a gateway token by making a lightweight API call.
// Supported gateways: discord, telegram, slack.
func (s *Server) handleGatewayTest(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p GatewayTestParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.Gateway == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "gateway is required"}
	}
	if p.Token == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "token is required"}
	}

	switch p.Gateway {
	case "discord":
		return s.testDiscord(ctx, p.Token)
	case "telegram":
		return s.testTelegram(ctx, p.Token)
	case "slack":
		return s.testSlack(ctx, p.Token)
	default:
		return nil, &RPCError{Code: InvalidParams, Message: fmt.Sprintf("unsupported gateway: %s (use discord, telegram, or slack)", p.Gateway)}
	}
}

func (s *Server) testDiscord(ctx context.Context, token string) (*GatewayTestResult, *RPCError) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://discord.com/api/v10/users/@me", nil)
	if err != nil {
		return &GatewayTestResult{OK: false, Error: err.Error()}, nil
	}
	req.Header.Set("Authorization", "Bot "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &GatewayTestResult{OK: false, Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &GatewayTestResult{OK: false, Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))}, nil
	}

	// Read body once, then unmarshal from bytes (B121: avoid double-read
	// that would cause json.Decoder to fail on an empty stream).
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &GatewayTestResult{OK: false, Error: fmt.Sprintf("failed to read response: %v", err)}, nil
	}

	var user struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(bodyBytes, &user); err != nil {
		return &GatewayTestResult{OK: false, Error: fmt.Sprintf("failed to parse response: %v", err)}, nil
	}

	return &GatewayTestResult{OK: true, User: user.Username}, nil
}

func (s *Server) testTelegram(ctx context.Context, token string) (*GatewayTestResult, *RPCError) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &GatewayTestResult{OK: false, Error: err.Error()}, nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &GatewayTestResult{OK: false, Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &GatewayTestResult{OK: false, Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))}, nil
	}

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &GatewayTestResult{OK: false, Error: fmt.Sprintf("failed to parse response: %v", err)}, nil
	}
	if !result.OK {
		return &GatewayTestResult{OK: false, Error: "telegram API returned ok=false"}, nil
	}

	return &GatewayTestResult{OK: true, User: result.Result.Username}, nil
}

func (s *Server) testSlack(ctx context.Context, token string) (*GatewayTestResult, *RPCError) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/auth.test", nil)
	if err != nil {
		return &GatewayTestResult{OK: false, Error: err.Error()}, nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &GatewayTestResult{OK: false, Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	var result struct {
		OK   bool   `json:"ok"`
		User string `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &GatewayTestResult{OK: false, Error: fmt.Sprintf("failed to parse response: %v", err)}, nil
	}
	if !result.OK {
		return &GatewayTestResult{OK: false, Error: "slack API returned ok=false"}, nil
	}

	return &GatewayTestResult{OK: true, User: result.User}, nil
}

// handleAgentSteer injects a guidance message into a running agent's steering
// queue. The message arrives mid-task on the next tool-iteration drain, allowing
// the user to redirect the agent without stopping it.
func (s *Server) handleAgentSteer(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p SteerParams
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "session_id is required"}
	}
	if p.Message == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "message is required"}
	}

	s.mu.RLock()
	a, ok := s.agents[p.SessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, &RPCError{Code: InvalidParams, Message: "no active agent for session"}
	}

	role := p.Role
	if role == "" {
		role = "user"
	}
	a.InjectSteer(p.Message, role)
	return map[string]string{"status": "steered"}, nil
}

// handleSelfEvalStatus returns the current self-evaluate loop state for the
// active agent session. Used by the TUI self-eval pane for real-time display.
func (s *Server) handleSelfEvalStatus(_ context.Context, params []byte) (interface{}, *RPCError) {
	type SelfEvalStatusResult struct {
		SessionID      string  `json:"session_id"`
		Active         bool    `json:"active"`
		Phase          string  `json:"phase"`
		Status         string  `json:"status"`
		Confidence     float64 `json:"confidence"`
		Iteration      int     `json:"iteration"`
		MaxIterations  int     `json:"max_iterations"`
		RedTeamPassed  bool    `json:"red_team_passed"`
		RedTeamTotal   int     `json:"red_team_total"`
		RedTeamFailed  int     `json:"red_team_failed"`
		ReflectionNote string  `json:"reflection_note"`
		StartedAt      string  `json:"started_at"`
		Message        string  `json:"message"`
	}

	// For now, return a placeholder — the TUI pane handles this gracefully.
	// The real data comes from wiring the agent's loop state into the server.
	return &SelfEvalStatusResult{
		Active:  false,
		Status:  "idle",
		Message: "Self-evaluation not active. Use /auto or /build to start.",
	}, nil
}

// handleTestResults returns the latest red-team test results for the active
// agent session. Used by the TUI test pane.
func (s *Server) handleTestResults(_ context.Context, params []byte) (interface{}, *RPCError) {
	type TestEntryResult struct {
		Name     string `json:"name"`
		File     string `json:"file"`
		Passed   bool   `json:"passed"`
		Duration string `json:"duration"`
		Category string `json:"category"`
		Error    string `json:"error,omitempty"`
	}

	type TestResultsResult struct {
		SessionID string            `json:"session_id"`
		Total     int               `json:"total"`
		Passed    int               `json:"passed"`
		Failed    int               `json:"failed"`
		Running   bool              `json:"running"`
		PassRate  float64           `json:"pass_rate"`
		Tests     []TestEntryResult `json:"tests"`
		Message   string            `json:"message"`
	}

	// Placeholder — the TUI pane handles this gracefully.
	return &TestResultsResult{
		Running: false,
		Message: "No active test run. Tests run during self-evaluate loop in auto/build mode.",
	}, nil
}

// handleSequentialQueue returns the current sequential processing queue state
// for the TUI Queue pane (§8.6.1).
func (s *Server) handleSequentialQueue(_ context.Context, params []byte) (interface{}, *RPCError) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.agents {
		if snap := a.QueueSnapshot(); snap.Total > 0 || snap.Active {
			type queueItem struct {
				Index       int    `json:"index"`
				Description string `json:"description"`
				Status      string `json:"status"`
				Error       string `json:"error,omitempty"`
				ElapsedMs   int64  `json:"elapsed_ms"`
			}
			type queueStatus struct {
				Active bool        `json:"active"`
				Total  int         `json:"total"`
				Done   int         `json:"done"`
				Failed int         `json:"failed"`
				Items  []queueItem `json:"items"`
			}
			items := make([]queueItem, len(snap.Items))
			for i, item := range snap.Items {
				items[i] = queueItem{
					Index:       item.Index,
					Description: item.Description,
					Status:      item.Status,
					Error:       item.Error,
					ElapsedMs:   item.ElapsedMs,
				}
			}
			return &queueStatus{
				Active: snap.Active,
				Total:  snap.Total,
				Done:   snap.Done,
				Failed: snap.Failed,
				Items:  items,
			}, nil
		}
	}
	return map[string]interface{}{
		"active": false,
		"total":  0,
		"done":   0,
		"failed": 0,
		"items":  []interface{}{},
	}, nil
}

// wireQuestionFunc sets up the agent's QuestionFunc to bridge to the TUI
// clarify dialog. When the agent calls AskQuestion(), it blocks until the
// user answers via the TUI and the answer is sent back through agent.answer.
func (s *Server) wireQuestionFunc(a *agent.Agent, sessionID string) {
	a.SetQuestionFunc(func(ctx context.Context, q agent.Question) agent.Answer {
		answerCh := make(chan agent.Answer, 1)

		// Store the question for polling.
		s.pendingQuestionMu.Lock()
		s.pendingQuestionData[sessionID] = &q
		s.pendingQuestionMu.Unlock()

		s.mu.Lock()
		s.pendingQuestions[sessionID] = answerCh
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			delete(s.pendingQuestions, sessionID)
			s.mu.Unlock()
			s.pendingQuestionMu.Lock()
			delete(s.pendingQuestionData, sessionID)
			s.pendingQuestionMu.Unlock()
		}()

		select {
		case answer := <-answerCh:
			return answer
		case <-ctx.Done():
			return agent.Answer{Cancel: true}
		}
	})
}

// handleClarifyPoll returns any pending clarify question for a session.
// Polled by the TUI every 500ms.
func (s *Server) handleClarifyPoll(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if params != nil {
		json.Unmarshal(params, &p)
	}

	// Return the first pending question found.
	s.pendingQuestionMu.RLock()
	defer s.pendingQuestionMu.RUnlock()

	for sid, q := range s.pendingQuestionData {
		if p.SessionID == "" || sid == p.SessionID {
			type clarifyResult struct {
				SessionID string   `json:"session_id"`
				Question  string   `json:"question"`
				Choices   []string `json:"choices"`
			}
			return &clarifyResult{
				SessionID: sid,
				Question:  q.Prompt,
				Choices:   q.Choices,
			}, nil
		}
	}

	return nil, nil
}

// handleSessionUsage returns token/cost usage for a session, today, or all time.
// Scope: "session" (default, requires session_id), "daily", or "all".
func (s *Server) handleSessionUsage(ctx context.Context, params []byte) (interface{}, *RPCError) {
	if s.costTracker == nil {
		return nil, &RPCError{Code: MethodNotFound, Message: "usage tracking not wired — configure [cost] in your config"}
	}
	var p SessionUsageParams
	if len(params) > 0 {
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
	}
	scope := p.Scope
	if scope == "" {
		scope = "session"
	}
	switch scope {
	case "daily":
		daily, err := s.costTracker.DailyCost(ctx)
		if err != nil {
			return nil, &RPCError{Code: InternalError, Message: err.Error()}
		}
		return &SessionUsageResult{Daily: &daily}, nil
	case "all":
		report, err := s.costTracker.Usage(ctx, cost.UsageOptions{})
		if err != nil {
			return nil, &RPCError{Code: InternalError, Message: err.Error()}
		}
		return &SessionUsageResult{Report: report}, nil
	default: // "session"
		sessionID := p.SessionID
		if sessionID == "" {
			return nil, &RPCError{Code: InvalidParams, Message: "session_id required for session scope"}
		}
		report, err := s.costTracker.Usage(ctx, cost.UsageOptions{SessionID: sessionID})
		if err != nil {
			return nil, &RPCError{Code: InternalError, Message: err.Error()}
		}
		return &SessionUsageResult{Report: report}, nil
	}
}

// handleAgentAnswer receives the user's answer to a clarify question and
// unblocks the waiting QuestionFunc.
func (s *Server) handleAgentAnswer(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
		Index     int    `json:"index"`
	}
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "session_id is required"}
	}

	s.mu.RLock()
	ch, ok := s.pendingQuestions[p.SessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, &RPCError{Code: InvalidParams, Message: "no pending question for session"}
	}

	// B044: Use select to avoid blocking forever if the reader has
	// abandoned the channel. A 1s timeout is generous for an in-process
	// channel — if the receiver isn't ready by then, it never will be.
	select {
	case ch <- agent.Answer{Text: p.Text, Index: p.Index}:
	case <-time.After(1 * time.Second):
		return nil, &RPCError{Code: InternalError, Message: "answer channel stalled"}
	}
	return map[string]string{"status": "answered"}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func unmarshalParams(data []byte, v interface{}) *RPCError {
	if data == nil {
		return &RPCError{Code: InvalidParams, Message: "missing params"}
	}
	if err := json.Unmarshal(data, v); err != nil {
		return &RPCError{Code: InvalidParams, Message: fmt.Sprintf("invalid params: %v", err)}
	}
	return nil
}

func toSessionInfo(s *session.Session) SessionInfo {
	return SessionInfo{
		ID:        s.ID,
		Title:     s.Title,
		Folder:    s.Folder,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		Model:     s.Model,
		Provider:  s.Provider,
		Status:    s.Status,
		ParentID:  s.ParentID,
		Children:  s.Children,
	}
}

func toModelInfo(m *providers.Model) ModelInfo {
	return ModelInfo{
		ID:               m.ID,
		Name:             m.Name,
		Family:           m.Family,
		ContextWindow:    m.ContextWindow,
		DefaultMaxTokens: m.DefaultMaxTokens,
		SupportsTools:    m.SupportsTools,
		SupportsVision:   m.SupportsVision,
		Reasoning:        m.Reasoning,
		InputModalities:  m.InputModalities,
		OutputModalities: m.OutputModalities,
	}
}

func getCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// toInt converts a JSON-decoded numeric value (float64 from encoding/json) to int.
func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

// resolveProviderModels returns the models for a provider config. If the
// config specifies explicit models those are used; otherwise built-in model
// lists from the providers package are returned.
func (s *Server) resolveProviderModels(pc config.ProviderConfig) []providers.Model {
	if len(pc.Models) > 0 {
		models := make([]providers.Model, 0, len(pc.Models))
		for _, mc := range pc.Models {
			m := providers.Model{
				ID:     mc.ID,
				Name:   mc.Name,
				CostIn: mc.CostIn, CostOut: mc.CostOut,
				CostCacheIn: mc.CostCacheIn, CostCacheOut: mc.CostCacheOut,
			}
			if mc.MaxTokens > 0 {
				m.ContextWindow = mc.MaxTokens
				m.DefaultMaxTokens = mc.MaxTokens
			}
			models = append(models, m)
		}
		return models
	}
	return s.builtinModels(pc.Type)
}

// builtinModels returns the default model list for a provider type.
func (s *Server) builtinModels(providerType string) []providers.Model {
	switch providerType {
	case "openai":
		return providers.OpenAIModels()
	case "anthropic":
		return providers.AnthropicModels()
	case "gemini":
		return providers.GeminiModels()
	case "deepseek":
		return providers.DeepSeekModels()
	case "ollama":
		return providers.OllamaModels()
	case "openrouter":
		return providers.OpenRouterModels()
	default:
		return nil
	}
}

// createAgent builds a new agent.Agent for the given session ID using the
// server's config and provider setup.
func (s *Server) createAgent(ctx context.Context, sessionID string) (*agent.Agent, error) {
	// Load session for metadata (model, provider).
	var model string
	var providerName string
	sess, err := s.sessionStore.Load(ctx, sessionID)
	if err == nil {
		model = sess.Model
		providerName = sess.Provider
	}
	if model == "" {
		model = s.cfg.Agent.DefaultModel
	}
	if providerName == "" {
		providerName = s.cfg.Agent.DefaultProvider
	}

	// Create providers from config.
	provMap := s.createProviders()
	if len(provMap) == 0 {
		return nil, fmt.Errorf("api: no providers configured")
	}

	// Resolve the provider for this session.
	var prov providers.Provider
	if p, ok := provMap[providerName]; ok {
		prov = p
	} else {
		// Fall back to the default provider.
		if p, ok := provMap[s.cfg.Agent.DefaultProvider]; ok {
			prov = p
		} else {
			// Fall back to the first available.
			for _, p := range provMap {
				prov = p
				break
			}
		}
	}

	a := agent.New(agent.Config{
		Provider:  prov,
		Model:     model,
		Tools:     s.toolRegistry,
		SessionID: sessionID,
		Hooks:     hooks.NewRegistry(),
		Scanners: []security.Scanner{
			security.NewCommandScanner(
				security.WithExtraDenyPatterns(s.cfg.Security.DenyPatterns),
				security.WithForbiddenPaths(s.cfg.Security.ForbiddenPaths),
				security.WithMaxCommandLen(s.cfg.Security.MaxCommandLen),
			),
			security.NewInjectionScanner(),
		},
	})

	// Wire context chips: directory, git branch, git diff.
	cm := prompt.NewChipManager()
	cm.Register(chips.NewDirectoryChip())
	cm.Register(chips.NewGitBranchChip())
	cm.Register(chips.NewGitDiffChip())
	a.SetChipManager(cm)

	// Wire the QuestionFunc so the agent can ask the user questions
	// via the TUI clarify dialog (§8.6).
	s.wireQuestionFunc(a, sessionID)

	// Wire the learning store if configured (§6.5).
	if s.learningStore != nil {
		a.SetLearningStore(s.learningStore)
	}

	// P0: context compaction — wire LCM-based compactor.
	if prov != nil {
		compactor := compaction.NewAgentCompactor(prov, tokenizer.NewEstimator(), 20)
		a.SetCompactor(compactor, true)
	}

	// P0: input classifier — shell vs NL routing.
	a.SetInputClassifier(func(raw string) agent.InputKind {
		return agent.InputKind(input.Classify(raw))
	})

	// P1: events/sinks — completion event emitter.
	emit := events.NewEmitter(eventsinks.NewLogSink(log.Default()))
	a.SetCompletionEmitter(emit, nil)

	// P1: feature flags.
	if s.featureMgr != nil {
		a.SetFeatureManager(s.featureMgr)
	}

	// P2: speculative read cache.
	if s.readCache != nil {
		a.SetReadCache(s.readCache)
	}

	// P2: extensions manager.
	if s.extensionsMgr != nil {
		a.SetExtensionsManager(wrapExtensions(s.extensionsMgr))
	}

	// Wire sub-agent manager so the agent and TUI can track active sub-agents.
	if s.subagentManager != nil {
		a.SetSubagentManager(s.subagentManager)
	}

	// Bonus: hotreload — wire config file watcher into the agent so
	// user.yaml changes (model, persona) are live-applied at turn
	// boundaries. Mirrors the CLI path in cmd/overkill/run.go.
	if s.hotReloadBus != nil {
		homeDir, _ := config.ConfigDir()
		if homeDir != "" {
			userYAML := filepath.Join(homeDir, "user.yaml")
			if _, err := hotreload.WireAgent(context.Background(), s.hotReloadBus, a, userYAML, hotreload.DiscardReporter()); err != nil {
				log.Printf("hotreload: wire agent: %v", err)
			}
		}
	}

	// PCA-5: Hydrate the thinking level from persisted config so the
	// user's preference is active immediately on agent creation — not
	// just after they manually toggle in the current session.
	a.SetThinkingLevel(string(s.cfg.Thinking.Level))

	// PCA-7: Prompt rewriter middleware ($4.10). When enabled the agent
	// pipes every user message through the rewriter — stripping
	// sycophancy, catching anti-patterns, and optionally expanding
	// ambiguous/complex prompts via LLM.
	if s.cfg.Rewriter.Enabled {
		rwModel := s.cfg.Rewriter.Model
		if rwModel == "" {
			rwModel = model
		}
		rw := rewriter.NewLLMRewriter(prov, rwModel)
		a.SetRewriter(rw)
	}

	return a, nil
}

// createProviders instantiates providers from the config using the real
// providers.NewProvider factory.
func (s *Server) createProviders() map[string]providers.Provider {
	result := make(map[string]providers.Provider)
	for _, pc := range s.cfg.Providers {
		models := s.resolveProviderModels(pc)
		p, err := providers.NewProvider(providers.FactoryConfig{
			Name:    pc.Name,
			Type:    pc.Type,
			APIKey:  pc.APIKey,
			BaseURL: pc.BaseURL,
			Models:  models,
			Headers: pc.Headers,
		})
		if err != nil {
			continue
		}
		result[pc.Name] = p
	}
	return result
}

// wrapExtensions adapts *extensions.Manager to agent.ExtensionsManager.
func wrapExtensions(m *extensions.Manager) agent.ExtensionsManager {
	return &extensionsAdapter{mgr: m}
}

type extensionsAdapter struct {
	mgr *extensions.Manager
}

func (e *extensionsAdapter) ListEnabled() []agent.ExtensionMeta {
	if e.mgr == nil {
		return nil
	}
	exts, _ := e.mgr.List()
	out := make([]agent.ExtensionMeta, 0, len(exts))
	for _, ext := range exts {
		if ext.Enabled {
			out = append(out, agent.ExtensionMeta{
				ID:          ext.ID,
				Name:        ext.Name,
				Kind:        string(ext.Kind),
				Description: ext.Description,
			})
		}
	}
	return out
}

// handleConfigTheme gets or sets the current theme.
// GET (no params) → returns the current theme name.
// SET (params.theme) → updates the theme and persists.
func (s *Server) handleConfigTheme(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		Theme string `json:"theme"`
	}
	// If params is empty or null, return current theme (GET).
	if len(params) == 0 || string(params) == "null" {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return map[string]string{"theme": s.cfg.UI.Theme}, nil
	}
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.Theme == "" {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return map[string]string{"theme": s.cfg.UI.Theme}, nil
	}
	// Validate theme name.
	validThemes := map[string]bool{
		"dark": true, "light": true, "cyberpunk": true, "ocean": true,
		"catppuccin-mocha": true,
	}
	// Also accept any custom theme installed in ~/.overkill/themes/.
	if dir, err := config.ThemesDir(); err == nil {
		if entries, readErr := os.ReadDir(dir); readErr == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
					name := strings.TrimSuffix(e.Name(), ".toml")
					if name != "" {
						validThemes[name] = true
					}
				}
			}
		}
	}
	if !validThemes[p.Theme] {
		return nil, &RPCError{Code: InvalidParams, Message: fmt.Sprintf("unknown theme: %q", p.Theme)}
	}
	s.mu.Lock()
	s.cfg.UI.Theme = p.Theme
	s.mu.Unlock()
	// Persist to disk.
	cfgPath, err := config.ConfigPath()
	if err == nil {
		_ = s.cfg.Save(cfgPath)
	}
	return map[string]string{"theme": p.Theme}, nil
}

// handleThinkingSetLevel sets the extended thinking level for all active agents
// and persists the choice to config. Valid levels: off, minimal, low, medium, high, x-high.
func (s *Server) handleThinkingSetLevel(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		Level string `json:"level"`
	}
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	tl := config.ThinkingLevel(p.Level)
	if !tl.Valid() {
		return nil, &RPCError{Code: InvalidParams, Message: fmt.Sprintf("invalid thinking level: %q (use off|minimal|low|medium|high|x-high)", p.Level)}
	}

	// Persist to config.
	s.mu.Lock()
	s.cfg.Thinking.Level = tl
	s.mu.Unlock()

	cfgPath, err := config.ConfigPath()
	if err == nil {
		_ = s.cfg.Save(cfgPath)
	}

	// Apply to all active agents.
	s.mu.RLock()
	for _, a := range s.agents {
		a.SetThinkingLevel(p.Level)
	}
	s.mu.RUnlock()

	return map[string]string{"level": p.Level}, nil
}

// handleModeSet toggles between plan mode and build mode.
// Plan mode: agent plans/analyzes only, no write execution.
// Build mode: full execution enabled (default).
func (s *Server) handleModeSet(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		Mode string `json:"mode"` // "plan" or "build"
	}
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.Mode != "plan" && p.Mode != "build" {
		return nil, &RPCError{Code: InvalidParams, Message: "mode must be 'plan' or 'build'"}
	}

	s.mu.Lock()
	s.currentMode = p.Mode
	// Propagate to all active agents so gateways can check mode.
	for _, a := range s.agents {
		a.SetMode(p.Mode)
	}
	s.mu.Unlock()

	return map[string]string{"mode": p.Mode}, nil
}

// handleModelsSelect updates the agent's model for a given session.
// Called by the TUI model switcher (models.select).
func (s *Server) handleModelsSelect(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.Provider == "" && p.Model == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "provider or model is required"}
	}

	// Store model selection in config for persistence across sessions.
	s.mu.Lock()
	if p.Provider != "" {
		s.cfg.Agent.DefaultProvider = p.Provider
	}
	if p.Model != "" {
		s.cfg.Agent.DefaultModel = p.Model
	}
	s.mu.Unlock()

	// Persist to config file.
	cfgPath, err := config.ConfigPath()
	if err == nil {
		_ = s.cfg.Save(cfgPath)
	}

	return map[string]interface{}{
		"status":   "ok",
		"provider": s.cfg.Agent.DefaultProvider,
		"model":    s.cfg.Agent.DefaultModel,
	}, nil
}

// handleSessionLoad loads a session by ID and returns its info.
// Called by the TUI session manager when a user selects a session.
func (s *Server) handleSessionLoad(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		ID string `json:"id"`
	}
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "id is required"}
	}

	sess, err := s.sessionStore.Load(ctx, p.ID)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: fmt.Sprintf("failed to load session: %v", err)}
	}
	if sess == nil {
		return nil, &RPCError{Code: InvalidParams, Message: "session not found"}
	}

	return &SessionInfo{
		ID:        sess.ID,
		Title:     sess.Title,
		Folder:    sess.Folder,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
		Model:     sess.Model,
		Provider:  sess.Provider,
		Status:    sess.Status,
		ParentID:  sess.ParentID,
		Children:  sess.Children,
	}, nil
}

// handleAPIGoal returns goal data for the dashboard.
// GET /api/goal
func (s *Server) handleAPIGoal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Collect goal data from any active agent's goal store.
	var goalText string
	var status string = "active"

	s.mu.RLock()
	for _, a := range s.agents {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		g, err := a.GetGoal(ctx)
		cancel()
		if err == nil && g != "" {
			goalText = g
			break
		}
	}
	s.mu.RUnlock()

	if goalText == "" {
		status = "inactive"
		goalText = "no active goal — use /goal set <objective> to create one"
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"objective":    goalText,
		"status":       status,
		"token_budget": nil,
		"tokens_used":  0,
		"time_used_s":  0,
	})
}

// handleAPIPlan returns plan data for the dashboard.
// GET /api/plan
func (s *Server) handleAPIPlan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	plan := map[string]interface{}{
		"title": "Active Plan",
		"items": []map[string]interface{}{},
	}

	// Try to get plan info from an active agent.
	s.mu.RLock()
	for _, a := range s.agents {
		if am := a.AutoMode(); am != nil && am.Plan != nil {
			plan["title"] = am.Plan.Title
			items := make([]map[string]interface{}, 0, len(am.Plan.Phases))
			for _, ph := range am.Plan.Phases {
				itemStatus := "pending"
				if ph.Status == agent.PhaseDone {
					itemStatus = "done"
				} else if ph.Status == agent.PhaseRunning {
					itemStatus = "in_progress"
				}
				items = append(items, map[string]interface{}{
					"id":     fmt.Sprintf("%d", ph.Index),
					"text":   ph.Description,
					"status": itemStatus,
				})
			}
			plan["items"] = items
			break
		}
	}
	s.mu.RUnlock()

	json.NewEncoder(w).Encode(plan)
}
