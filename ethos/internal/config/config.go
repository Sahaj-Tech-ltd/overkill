package config

import "time"

type Config struct {
	Version     int               `toml:"version"`
	Agent       AgentConfig       `toml:"agent"`
	Providers   []ProviderConfig  `toml:"providers"`
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

// BridgeConfig governs the HTTP webhook bridge for sidecar gateways
// (Baileys WhatsApp, discord.js, SMS relays). Listens on loopback by
// default; expose only behind a reverse proxy if you need remote access.
type BridgeConfig struct {
	Enabled bool   `toml:"enabled"`
	Listen  string `toml:"listen"` // default 127.0.0.1:7799
	Token   string `toml:"token"`  // shared secret; empty disables auth
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
	// delivers webhooks to HTTPS endpoints. Default 127.0.0.1:7798.
	Listen string `toml:"listen"`
}

// GatewayConfig wires all remote messaging gateways. Each sub-section is
// independently togglable so users can run telegram alone, discord
// alone, whatsapp alone, the bridge alone, or any combination.
type GatewayConfig struct {
	Telegram TelegramConfig `toml:"telegram"`
	Discord  DiscordConfig  `toml:"discord"`
	WhatsApp WhatsAppConfig `toml:"whatsapp"`
	Bridge   BridgeConfig   `toml:"bridge"`
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
	Animations bool `toml:"animations"`
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
	Name            string `toml:"name"`
	DefaultProvider string `toml:"default_provider"`
	DefaultModel    string `toml:"default_model"`
	MaxTurns        int    `toml:"max_turns"`
	SpecDriven      bool   `toml:"spec_driven"`
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
	DailyLimitUSD    float64 `toml:"daily_limit_usd"`
	PerTaskLimitUSD  float64 `toml:"per_task_limit_usd"`
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
	SoftTriggerPercent int  `toml:"soft_trigger_percent"`
	HardTriggerPercent int  `toml:"hard_trigger_percent"`
	PreserveMessages   int  `toml:"preserve_messages"`
	MaxSummaryTokens   int  `toml:"max_summary_tokens"`
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
