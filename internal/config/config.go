package config

import "time"

// ThinkingLevel controls how much internal reasoning/thinking the model
// exposes. Not all providers support all levels — unsupported levels
// are silently downgraded to the nearest supported option.
type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "x-high"
)

// ThinkingBudgetTokens maps each ThinkingLevel to a budget_tokens value
// for Anthropic (and any other provider that uses token-budget thinking).
func (t ThinkingLevel) BudgetTokens() int {
	switch t {
	case ThinkingMinimal:
		return 1024
	case ThinkingLow:
		return 2048
	case ThinkingMedium:
		return 4096
	case ThinkingHigh:
		return 8192
	case ThinkingXHigh:
		return 16384
	default:
		return 0
	}
}

// Valid returns whether the level is a known value.
func (t ThinkingLevel) Valid() bool {
	switch t {
	case ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh:
		return true
	default:
		return false
	}
}

// Next returns the next level in the cycle. Wraps from x-high back to off.
func (t ThinkingLevel) Next() ThinkingLevel {
	switch t {
	case ThinkingOff:
		return ThinkingMinimal
	case ThinkingMinimal:
		return ThinkingLow
	case ThinkingLow:
		return ThinkingMedium
	case ThinkingMedium:
		return ThinkingHigh
	case ThinkingHigh:
		return ThinkingXHigh
	case ThinkingXHigh:
		return ThinkingOff
	default:
		return ThinkingOff
	}
}

type Config struct {
	Version     int               `toml:"version"`
	Agent       AgentConfig       `toml:"agent"`
	Thinking    ThinkingConfig    `toml:"thinking"`
	Providers   []ProviderConfig  `toml:"providers"`
	TTS         TTSConfig         `toml:"tts"`
	Personality PersonalityConfig `toml:"personality"`
	Security    SecurityConfig    `toml:"security"`
	Session     SessionConfig     `toml:"session"`
	Cost        CostConfig        `toml:"cost"`
	Compaction  CompactionConfig  `toml:"compaction"`
	MCP         MCPConfig         `toml:"mcp"`
	LSP         LSPConfig         `toml:"lsp"`
	Skills      SkillsConfig      `toml:"skills"`
	UI          UIConfig          `toml:"ui"`
	Sync        SyncConfig        `toml:"sync"`
	Share       ShareConfig       `toml:"share"`
	ACP         ACPConfig         `toml:"acp"`
	Plugins     PluginsConfig     `toml:"plugins"`
	Slack       SlackConfig       `toml:"slack"`
	Gateways    GatewayConfig     `toml:"gateways"`
	Vision      VisionConfig      `toml:"vision"`
	Browser     BrowserConfig     `toml:"browser"`
	Rewriter    RewriterConfig    `toml:"rewriter"`
	Ouroboros   OuroborosConfig   `toml:"ouroboros"`
	ImageGen    ImageGenConfig    `toml:"image_gen"`

	// DatabaseURL is the PostgreSQL connection string for persistent stores
	// (learning corrections, etc.). Falls back to DATABASE_URL env var.
	// Example: "postgres://user:***@localhost:5432/overkill?sslmode=disable"
	DatabaseURL string `toml:"database_url"`
}

// ThinkingConfig governs extended thinking / chain-of-thought visibility.
// Providers that support thinking (Anthropic, DeepSeek R1, OpenAI o-series)
// surface internal reasoning tokens as the agent streams.
type ThinkingConfig struct {
	// Level sets the thinking budget. Supported values:
	// off, minimal, low, medium, high, x-high.
	// Default is "off" (no thinking tokens surfaced).
	Level ThinkingLevel `toml:"level"`
}

// OuroborosConfig governs the Ouroboros adversarial-review wall
// (master plan §6.5 Wall 1). The wall MUST run against a different
// provider/model than the main agent — otherwise it's just the agent
// grading its own homework. Off by default.
type OuroborosConfig struct {
	Enabled    bool   `toml:"enabled"`
	Provider   string `toml:"provider"`    // e.g. "openai", "anthropic"
	Model      string `toml:"model"`       // model id on the review provider
	APIKey     string `toml:"api_key"`     // falls back to <PROVIDER>_API_KEY
	BaseURL    string `toml:"base_url"`    // optional override
	StrictMode bool   `toml:"strict_mode"` // when true, warnings also block
}

// ImageGenConfig governs text-to-image generation (image_gen tool).
// Provider can be "openai" (DALL-E 3), "stability" (Stability AI),
// or "replicate" (Flux Schnell). API keys fall back to env vars:
// OPENAI_API_KEY, STABILITY_API_KEY, REPLICATE_API_TOKEN.
type ImageGenConfig struct {
	Provider       string `toml:"provider"`        // "openai" | "stability" | "replicate"
	OpenAIKey      string `toml:"openai_key"`      // falls back to OPENAI_API_KEY
	StabilityKey   string `toml:"stability_key"`   // falls back to STABILITY_API_KEY
	ReplicateToken string `toml:"replicate_token"` // falls back to REPLICATE_API_TOKEN
}

// TTSConfig governs text-to-speech settings for the tts.speak tool.
type TTSConfig struct {
	Provider      string `toml:"provider"`       // default provider: "edge"|"kittentts"|"openai"|"elevenlabs"
	OpenAIKey     string `toml:"openai_key"`     // falls back to OPENAI_API_KEY
	ElevenLabsKey string `toml:"elevenlabs_key"` // falls back to ELEVENLABS_API_KEY
	Voice         string `toml:"voice"`          // default voice for the provider
}

// RewriterConfig governs the prompt rewriter middleware (master plan §4.10).
// Disabled by default. When Enabled is true the agent pipes each user message
// through the rewriter before constructing the provider request.
type RewriterConfig struct {
	Enabled          bool   `toml:"enabled"`
	StripSycophancy  bool   `toml:"strip_sycophancy"`
	AntiPatternGuard bool   `toml:"anti_pattern_guard"`
	LLMRewrite       bool   `toml:"llm_rewrite"`
	Model            string `toml:"model"`
}

// BrowserConfig governs the agentic browser. Off by default — tools are not
// registered when Enabled is false.
type BrowserConfig struct {
	Enabled      bool          `toml:"enabled"`
	Headless     bool          `toml:"headless"`
	ChromePath   string        `toml:"chrome_path"`
	UserAgent    string        `toml:"user_agent"`
	AllowedHosts []string      `toml:"allowed_hosts"`
	BlockedHosts []string      `toml:"blocked_hosts"`
	Timeout      time.Duration `toml:"timeout"`
}

// SlackConfig governs the optional Slack bot daemon (`overkill slack`).
// Off by default; tokens may also be supplied via SLACK_APP_TOKEN /
// SLACK_BOT_TOKEN env vars at runtime.
type SlackConfig struct {
	Enabled         bool     `toml:"enabled"`
	AppToken        string   `toml:"app_token"`        // xapp-... (Socket Mode)
	BotToken        string   `toml:"bot_token"`        // xoxb-... (Web API)
	AllowedChannels []string `toml:"allowed_channels"` // empty = all where invited
}

// TelegramConfig governs the optional Telegram bot gateway.
// Off by default; token may also come from TELEGRAM_BOT_TOKEN at runtime.
type TelegramConfig struct {
	Enabled      bool    `toml:"enabled"`
	BotToken     string  `toml:"bot_token"`
	AllowedChats []int64 `toml:"allowed_chats"` // empty = any chat the bot is in
	// NotifyChatID is the chat the gateway pushes unsolicited
	// notifications to (§7.1 Layer 6 task-completion alerts). When
	// zero, Telegram is not used for push delivery even if the
	// gateway is otherwise enabled.
	NotifyChatID int64 `toml:"notify_chat_id"`
}

// TwilioConfig describes a Twilio SMS sidecar that pipes into the bridge.
// The sidecar is a ~30-line Python/Node script that receives Twilio webhooks,
// POSTs to /v1/in, and SSE-reads from /v1/out. This section is reference
// config for the sidecar — Overkill itself doesn't call the Twilio API.
type TwilioConfig struct {
	// Enabled toggles the Twilio sidecar. When true, the bridge accepts
	// messages from the 'sms' channel and the sidecar script should be
	// pointed at the bridge listen address.
	Enabled bool `toml:"enabled"`
	// AccountSID is the Twilio account identifier.
	AccountSID string `toml:"account_sid"`
	// AuthToken is the Twilio API secret.
	AuthToken string `toml:"auth_token"`
	// FromNumber is the Twilio phone number messages are sent from.
	FromNumber string `toml:"from_number"`
	// WebhookPath is the public URL Twilio POSTs incoming SMS to.
	// The sidecar listens here and relays to the bridge.
	WebhookPath string `toml:"webhook_path"`
}

// IMessageConfig describes an iMessage sidecar that pipes into the bridge.
// Supports BlueBubbles (self-hosted iMessage server for macOS) or any
// iMessage relay that speaks the bridge HTTP protocol. This section is
// reference config for the sidecar — Overkill itself doesn't call
// BlueBubbles or Apple APIs.
type IMessageConfig struct {
	// Enabled toggles the iMessage sidecar.
	Enabled bool `toml:"enabled"`
	// ServerURL is the BlueBubbles server address (e.g. http://10.0.1.5:1234).
	ServerURL string `toml:"server_url"`
	// Password is the BlueBubbles server password.
	Password string `toml:"password"`
}

// BridgeConfig governs the HTTP webhook bridge for sidecar gateways
// (Baileys WhatsApp, discord.js, SMS relays, iMessage bridges).
// Listens on loopback by default; expose only behind a reverse proxy
// if you need remote access.
type BridgeConfig struct {
	Enabled  bool           `toml:"enabled"`
	Listen   string         `toml:"listen"` // default 127.0.0.1:7799
	Token    string         `toml:"token"`  // shared secret; empty disables auth
	Twilio   TwilioConfig   `toml:"twilio"`
	iMessage IMessageConfig `toml:"imessage"`
}

// DiscordConfig governs the optional Discord bot gateway.
// Off by default; token may also come from DISCORD_BOT_TOKEN at runtime.
//
// The bot responds in DMs unconditionally and in guild channels only
// when @mentioned. AllowedGuilds/AllowedChannels restrict where the
// bot replies — empty lists mean "any guild/channel the bot can see".
type DiscordConfig struct {
	Enabled         bool     `toml:"enabled"`
	BotToken        string   `toml:"bot_token"`
	AllowedGuilds   []string `toml:"allowed_guilds"`   // empty = any
	AllowedChannels []string `toml:"allowed_channels"` // empty = any
	// RequireMention, when true, makes the bot ignore guild messages
	// that don't @mention it. DMs always work regardless. Default true
	// (set explicitly in config if you want the bot to react to every
	// channel message — usually a bad idea).
	RequireMention bool `toml:"require_mention"`
	// NotifyChannelID is the channel the gateway pushes unsolicited
	// notifications to (§7.1 Layer 6). Empty = no Discord push
	// delivery.
	NotifyChannelID string `toml:"notify_channel_id"`
}

// WhatsAppConfig governs the optional WhatsApp gateway. Two backends:
//
//   - "whatsmeow": Go-native unofficial client. Personal use only;
//     TOS gray-area. Strict upgrade over Baileys-Node-sidecar — same
//     posture, no Node runtime. Requires a one-time QR pair via
//     `overkill whatsapp pair`.
//   - "cloud": WhatsApp Business Cloud API. Official, production-
//     grade, paid per conversation at scale. 24h messaging window
//     applies for free-form messages. Requires Meta Business
//     verification + a public HTTPS webhook (reverse proxy).
//
// Backend is a config-level switch — pick at config write time, not
// per-message. Both backends share the same Inbound/Reply contract
// through the gateway dispatcher.
type WhatsAppConfig struct {
	Enabled bool   `toml:"enabled"`
	Backend string `toml:"backend"` // "whatsmeow" | "cloud"

	// AllowedFrom restricts which sender phone numbers the bot
	// responds to (E.164 form, no leading +). Empty = any.
	AllowedFrom []string `toml:"allowed_from"`

	// Whatsmeow sub-config — only consulted when Backend = "whatsmeow".
	Whatsmeow WhatsAppWhatsmeowConfig `toml:"whatsmeow"`
	// Cloud sub-config — only consulted when Backend = "cloud".
	Cloud WhatsAppCloudConfig `toml:"cloud"`
	// NotifyJID is the WhatsApp JID (e.g. "1234567890@s.whatsapp.net")
	// the gateway pushes unsolicited notifications to (§7.1 Layer 6).
	// Empty = no WhatsApp push delivery.
	NotifyJID string `toml:"notify_jid"`
}

// WhatsAppWhatsmeowConfig holds settings for the unofficial backend.
type WhatsAppWhatsmeowConfig struct {
	// StorePath is the SQLite file holding the device keys. Created
	// by `overkill whatsapp pair`. Default: ~/.overkill/whatsapp.db
	StorePath string `toml:"store_path"`
}

// WhatsAppCloudConfig holds settings for the Meta Cloud API backend.
type WhatsAppCloudConfig struct {
	PhoneNumberID string `toml:"phone_number_id"`
	AccessToken   string `toml:"access_token"` // env: WHATSAPP_CLOUD_ACCESS_TOKEN
	AppSecret     string `toml:"app_secret"`   // env: WHATSAPP_CLOUD_APP_SECRET
	VerifyToken   string `toml:"verify_token"` // env: WHATSAPP_CLOUD_VERIFY_TOKEN
	// Listen is the HTTP bind address for the webhook server. Place
	// behind a reverse proxy that terminates HTTPS — Meta only
	// delivers webhooks to HTTPS endpoints. Default from config.DefaultWhatsAppCloudAddr.
	Listen string `toml:"listen"`
}

// SignalConfig governs the optional Signal gateway via signal-cli REST API.
// Off by default; requires signal-cli daemon running with --rest-api.
type SignalConfig struct {
	Enabled    bool   `toml:"enabled"`
	RestAPIURL string `toml:"rest_api_url"` // default http://localhost:8080
	Account    string `toml:"account"`      // E.164 phone number
	AuthToken  string `toml:"auth_token"`   // Bearer token for signal-cli auth
}

// MatrixConfig governs the optional Matrix (Element, etc.) gateway via
// raw HTTP against the Matrix Client-Server API. Off by default.
// If AccessToken is empty but Password is set, the bot auto-logs in
// with m.login.password to obtain a token at startup.
type MatrixConfig struct {
	Enabled       bool   `toml:"enabled"`
	HomeserverURL string `toml:"homeserver_url"` // default https://matrix.org
	UserID        string `toml:"user_id"`        // @user:homeserver
	AccessToken   string `toml:"access_token"`   // env: MATRIX_ACCESS_TOKEN
	Password      string `toml:"password"`       // for auto-login if no token
}

// MattermostConfig governs the optional Mattermost bot gateway.
// Uses the Mattermost WebSocket API for real-time events and the
// REST API for posting messages. Off by default.
type MattermostConfig struct {
	Enabled   bool   `toml:"enabled"`
	ServerURL string `toml:"server_url"` // e.g. https://mattermost.example.com
	BotToken  string `toml:"bot_token"`  // env: MATTERMOST_BOT_TOKEN
	TeamName  string `toml:"team_name"`  // team the bot joins
}

// GatewayConfig wires all remote messaging gateways. Each sub-section is
// independently togglable so users can run telegram alone, discord
// alone, whatsapp alone, the bridge alone, or any combination.
type GatewayConfig struct {
	Telegram   TelegramConfig   `toml:"telegram"`
	Discord    DiscordConfig    `toml:"discord"`
	Slack      SlackConfig      `toml:"slack"`
	WhatsApp   WhatsAppConfig   `toml:"whatsapp"`
	Bridge     BridgeConfig     `toml:"bridge"`
	Signal     SignalConfig     `toml:"signal"`
	Matrix     MatrixConfig     `toml:"matrix"`
	Mattermost MattermostConfig `toml:"mattermost"`
}

// VisionConfig governs the standalone vision describer used by remote
// gateways for inbound photos and by the vision_describe tool. Off by
// default; provider must be "anthropic" today (more to come).
type VisionConfig struct {
	Enabled  bool   `toml:"enabled"`
	Provider string `toml:"provider"` // "anthropic"
	APIKey   string `toml:"api_key"`  // falls back to ANTHROPIC_API_KEY
	Model    string `toml:"model"`    // e.g. claude-sonnet-4-5-20250929
}

// PluginsConfig governs the subprocess plugin runtime. Disabled is a list
// of plugin names to skip during discovery (used by /plugins toggle).
type PluginsConfig struct {
	Disabled []string `toml:"disabled"`
	Dir      string   `toml:"dir"`
}

// SyncConfig governs cross-machine session sync. Backend can be one of
// "" (disabled), "file", "s3", or "git".
type SyncConfig struct {
	Backend  string         `toml:"backend"`
	AutoPush bool           `toml:"auto_push"`
	S3       SyncS3Config   `toml:"s3"`
	Git      SyncGitConfig  `toml:"git"`
	File     SyncFileConfig `toml:"file"`
}

type SyncS3Config struct {
	Endpoint  string `toml:"endpoint"`
	Bucket    string `toml:"bucket"`
	Region    string `toml:"region"`
	AccessKey string `toml:"access_key"`
	SecretKey string `toml:"secret_key"`
	Prefix    string `toml:"prefix"`
	UseSSL    bool   `toml:"use_ssl"`
}

type SyncGitConfig struct {
	RemoteURL string `toml:"remote_url"`
	Branch    string `toml:"branch"`
	LocalDir  string `toml:"local_dir"`
}

type SyncFileConfig struct {
	Path string `toml:"path"`
}

// ShareConfig governs the /share command and `overkill share` CLI.
type ShareConfig struct {
	Backend     string `toml:"backend"`      // "gist" | "transfer-sh"
	GitHubToken string `toml:"github_token"` // PAT with gist scope
}

// ACPConfig governs the Agent Communication Protocol server.
type ACPConfig struct {
	Listen         string   `toml:"listen"`
	Token          string   `toml:"token"`
	AllowedOrigins []string `toml:"allowed_origins"`
	Enabled        bool     `toml:"enabled"`
}

// UIConfig controls TUI-level cosmetics. Animations is a soft kill-switch for
// every animated component — useful over slow SSH or in dumb terminals.
type UIConfig struct {
	Animations bool   `toml:"animations"`
	Theme      string `toml:"theme"`
}

type SkillsConfig struct {
	Enabled []string `toml:"enabled"`
}

type MCPConfig struct {
	Servers []MCPServer `toml:"servers"`
}

type MCPServer struct {
	Name    string            `toml:"name"`
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	Enabled bool              `toml:"enabled"`
	// mcpshield capability — empty/zero means "trusted, no caps"
	// (YOLO default). Users locking down a server set one or more
	// of these to opt into the policy gate.
	AllowedTools        []string `toml:"allowed_tools,omitempty"`
	AllowedPathPrefixes []string `toml:"allowed_path_prefixes,omitempty"`
	MaxBytesPerCall     int      `toml:"max_bytes_per_call,omitempty"`
	Trusted             *bool    `toml:"trusted,omitempty"` // nil = default trusted
}

type LSPConfig struct {
	Servers []LSPServer `toml:"servers"`
}

type LSPServer struct {
	Language  string   `toml:"language"`
	Command   string   `toml:"command"`
	Args      []string `toml:"args"`
	Filetypes []string `toml:"filetypes"`
}

type AgentConfig struct {
	Name                     string            `toml:"name"`
	DefaultProvider          string            `toml:"default_provider"`
	DefaultModel             string            `toml:"default_model"`
	MaxTurns                 int               `toml:"max_turns"`
	SpecDriven               bool              `toml:"spec_driven"`
	SystemPromptOverrides    map[string]string `toml:"system_prompt_overrides"`
	// SystemPrompt is a user-editable base system prompt. When non-empty,
	// it's prepended to the personality-generated prompt. Changing it
	// mid-session triggers context compaction + a fresh conversation.
	SystemPrompt string `toml:"system_prompt"`
}

type ProviderConfig struct {
	Name     string            `toml:"name"`
	Type     string            `toml:"type"`
	APIKey   string            `toml:"api_key"`
	AuthType string            `toml:"auth_type"`
	BaseURL  string            `toml:"base_url"`
	Models   []ModelConfig     `toml:"models"`
	Headers  map[string]string `toml:"headers"`
}

type ModelConfig struct {
	ID           string  `toml:"id"`
	Name         string  `toml:"name"`
	MaxTokens    int     `toml:"max_tokens"`
	CostIn       float64 `toml:"cost_in"`
	CostOut      float64 `toml:"cost_out"`
	CostCacheIn  float64 `toml:"cost_cache_in"`
	CostCacheOut float64 `toml:"cost_cache_out"`
}

type PersonalityConfig struct {
	Level    string `toml:"level"`
	Language string `toml:"language"`
}

type SecurityConfig struct {
	AutonomyLevel  string   `toml:"autonomy_level"`
	DenyPatterns   []string `toml:"deny_patterns"`
	ForbiddenPaths []string `toml:"forbidden_paths"`
	MaxCommandLen  int      `toml:"max_command_len"`
	SandboxEnabled bool     `toml:"sandbox_enabled"`
}

type SessionConfig struct {
	AutoTitle   bool   `toml:"auto_title"`
	MaxSessions int    `toml:"max_sessions"`
	DataDir     string `toml:"data_dir"`
}

type CostConfig struct {
	DailyLimitUSD   float64 `toml:"daily_limit_usd"`
	PerTaskLimitUSD float64 `toml:"per_task_limit_usd"`
	// RollingWindowHrs is the look-back window for RollingLimitUSD.
	// Default 5 hours per master plan §4.5. Ignored when
	// RollingLimitUSD is 0.
	RollingWindowHrs int `toml:"rolling_window_hrs"`
	// RollingLimitUSD caps total spend over the last RollingWindowHrs.
	// 0 disables the rolling cap entirely. Exceeding the limit sets
	// BudgetStatus.ShouldAbort; crossing WarnAtPercent of it sets
	// ShouldWarn. Caps daily-spike protection independently of the
	// daily limit (different bucket: 5h vs 24h).
	RollingLimitUSD float64 `toml:"rolling_limit_usd"`
	WarnAtPercent   int     `toml:"warn_at_percent"`
}

type CompactionConfig struct {
	SoftTriggerPercent int `toml:"soft_trigger_percent"`
	HardTriggerPercent int `toml:"hard_trigger_percent"`
	PreserveMessages   int `toml:"preserve_messages"`
	MaxSummaryTokens   int `toml:"max_summary_tokens"`
	// Model overrides the model used for the summarisation LLM call.
	// Empty falls back to the provider's cheapest model at runtime
	// (via LCM compactor's pickCheapestModel).
	Model string `toml:"model,omitempty"`
	// UseLCM routes Agent.Compact through the LCM 3-level escalation
	// compactor (internal/compaction). When false, falls back to the legacy
	// ad-hoc single-LLM-call compact path. Defaults to true.
	UseLCM bool `toml:"use_lcm"`
	// PromptCompress, when true, runs the assembled system prompt through
	// the LLMLingua-style compressor on high-utilization turns (≥0.7).
	// Off by default — adds a per-turn LLM round-trip when triggered.
	PromptCompress bool `toml:"prompt_compress"`
}

const CurrentVersion = 1

// Network defaults — well-known ports for Overkill's sub-services.
// Each service uses a dedicated loopback port so they can coexist without
// port conflicts.
const (
	DefaultWebUIAddr         = "127.0.0.1:8420"
	DefaultSSEDashboardAddr  = "127.0.0.1:7802"
	DefaultSOPWebhookAddr    = "127.0.0.1:7801"
	DefaultWhatsAppCloudAddr = "127.0.0.1:7798"
	DefaultACPAddr           = "127.0.0.1:7777"
)
