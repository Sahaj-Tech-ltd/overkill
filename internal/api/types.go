package api

import (
	"encoding/json"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// JSON-RPC 2.0 envelope types.

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC error codes.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32603
	InternalError  = -32603
)

func errorString(code int) string {
	switch code {
	case ParseError:
		return "Parse error"
	case InvalidRequest:
		return "Invalid request"
	case MethodNotFound:
		return "Method not found"
	case InvalidParams:
		return "Invalid params"
	default:
		return "Internal error"
	}
}

// --- agent ---

type SendMessageParams struct {
	SessionID string `json:"session_id,omitempty"`
	Message   string `json:"message"`
}

type SendMessageResult struct {
	Response    string `json:"response"`
	ToolCalls   int    `json:"tool_calls"`
	TotalTokens int    `json:"total_tokens"`
	Steps       int    `json:"steps"`
	Model       string `json:"model"`
	Blocked     bool   `json:"blocked"`
	BlockReason string `json:"block_reason,omitempty"`
}

type AbortParams struct {
	SessionID string `json:"session_id"`
}

// --- session ---

type SessionInfo struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Folder    string    `json:"folder"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Model     string    `json:"model"`
	Provider  string    `json:"provider"`
	Status    string    `json:"status"`
}

type SessionListResult struct {
	Sessions []SessionInfo `json:"sessions"`
}

type SessionCreateParams struct {
	Folder   string `json:"folder,omitempty"`
	Title    string `json:"title,omitempty"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type SessionCreateResult struct {
	Session SessionInfo `json:"session"`
}

type SessionDeleteParams struct {
	ID string `json:"id"`
}

// --- config ---

type ConfigGetResult struct {
	Version int                    `json:"version"`
	Agent   map[string]interface{} `json:"agent"`
	UI      map[string]interface{} `json:"ui"`
}

type ConfigUpdateParams struct {
	Patch map[string]interface{} `json:"patch"`
}

// --- providers ---

type ProviderInfo struct {
	Name   string      `json:"name"`
	Type   string      `json:"type"`
	Models []ModelInfo `json:"models"`
}

type ModelInfo struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Family           string   `json:"family"`
	ContextWindow    int      `json:"context_window"`
	DefaultMaxTokens int      `json:"default_max_tokens"`
	SupportsTools    bool     `json:"supports_tools"`
	SupportsVision   bool     `json:"supports_vision"`
	Reasoning        bool     `json:"reasoning"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
}

type ProvidersListResult struct {
	Providers []ProviderInfo `json:"providers"`
}

type ModelsListParams struct {
	Provider string `json:"provider"`
}

type ModelsListResult struct {
	Models []ModelInfo `json:"models"`
}

// --- config.exists / config.create ---

type ConfigExistsResult struct {
	Exists bool `json:"exists"`
}

type ConfigCreateParams struct {
	Providers []config.ProviderConfig `json:"providers,omitempty"`
	Models    []config.ModelConfig    `json:"models,omitempty"`
	TTS       *TTSConfig              `json:"tts,omitempty"`
	Gateways  *GatewayConfigFields    `json:"gateways,omitempty"`
}

// TTSConfig holds text-to-speech settings written as part of config.create.
type TTSConfig struct {
	Provider string `json:"provider,omitempty"` // "openai" | "elevenlabs" | "edge"
	APIKey   string `json:"api_key,omitempty"`
	Voice    string `json:"voice,omitempty"`
}

// GatewayConfigFields holds gateway configuration for the onboarding wizard.
type GatewayConfigFields struct {
	Discord  *DiscordGatewayConfig  `json:"discord,omitempty"`
	Telegram *TelegramGatewayConfig `json:"telegram,omitempty"`
	WhatsApp *WhatsAppGatewayConfig `json:"whatsapp,omitempty"`
}

type DiscordGatewayConfig struct {
	BotToken        string `json:"bot_token,omitempty"`
	Enabled         bool   `json:"enabled"`
	NotifyChannelID string `json:"notify_channel_id,omitempty"`
}

type TelegramGatewayConfig struct {
	BotToken      string `json:"bot_token,omitempty"`
	Enabled       bool   `json:"enabled"`
	NotifyChatID  int64  `json:"notify_chat_id,omitempty"`
}

type WhatsAppGatewayConfig struct {
	Enabled  bool   `json:"enabled"`
	Backend  string `json:"backend,omitempty"` // "whatsmeow" | "cloud"
}

// --- agent.subagents ---

type SubagentInfo struct {
	Name      string `json:"name"`
	Status    string `json:"status"` // "running" | "completed" | "failed"
	ElapsedMs int64  `json:"elapsed_ms"`
	Model     string `json:"model"`
}

type SubagentListResult struct {
	Subagents []SubagentInfo `json:"subagents"`
}

// --- status ---

type HealthResult struct {
	Status  string `json:"status"`
	Version int    `json:"version"`
}
