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

// SlackConfig governs the optional Slack bot daemon (`ethos slack`).
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
}

// BridgeConfig governs the HTTP webhook bridge for sidecar gateways
// (Baileys WhatsApp, discord.js, SMS relays). Listens on loopback by
// default; expose only behind a reverse proxy if you need remote access.
type BridgeConfig struct {
	Enabled bool   `toml:"enabled"`
	Listen  string `toml:"listen"` // default 127.0.0.1:7799
	Token   string `toml:"token"`  // shared secret; empty disables auth
}

// GatewayConfig wires all remote messaging gateways. Each sub-section is
// independently togglable so users can run telegram alone, bridge alone,
// or both.
type GatewayConfig struct {
	Telegram TelegramConfig `toml:"telegram"`
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

// ShareConfig governs the /share command and `ethos share` CLI.
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
	RollingWindowHrs int     `toml:"rolling_window_hrs"`
	WarnAtPercent    int     `toml:"warn_at_percent"`
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
