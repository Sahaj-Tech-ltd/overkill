package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
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

	result, err := a.Run(ctx, p.Message)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}

	// Persist session state (message count, model, cost).
	s.saveSessionState(ctx, sessionID, a)

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

// handleSessionList returns all sessions from the store.
func (s *Server) handleSessionList(ctx context.Context, _ []byte) (interface{}, *RPCError) {
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

// handleConfigGet returns the current configuration.
func (s *Server) handleConfigGet(_ context.Context, _ []byte) (interface{}, *RPCError) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agentInfo := map[string]interface{}{
		"name":             s.cfg.Agent.Name,
		"default_provider": s.cfg.Agent.DefaultProvider,
		"default_model":    s.cfg.Agent.DefaultModel,
		"max_turns":        s.cfg.Agent.MaxTurns,
		"spec_driven":      s.cfg.Agent.SpecDriven,
	}
	uiInfo := map[string]interface{}{
		"animations": s.cfg.UI.Animations,
	}
	return &ConfigGetResult{
		Version: s.cfg.Version,
		Agent:   agentInfo,
		UI:      uiInfo,
	}, nil
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
// Stubbed: returns an empty list until the agent tracks subagent state.
func (s *Server) handleAgentSubagents(_ context.Context, _ []byte) (interface{}, *RPCError) {
	return &SubagentListResult{Subagents: []SubagentInfo{}}, nil
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
	})
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
