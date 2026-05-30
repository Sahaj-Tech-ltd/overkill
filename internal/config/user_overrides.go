// Package config — v2.0 user-overrides layer.
//
// The legacy config (config.go) is TOML and centred on system-level
// wiring: provider credentials, daemon listen addresses, MCP servers
// that ship with the binary. The v2.0 user-overrides layer is YAML
// and centred on what users want to tweak at runtime: model picker,
// scanner toggles, system-prompt patches, persona, board pipeline.
//
// It lives at `~/.config/overkill/user.yaml`, loads on boot, and is
// hot-reloaded on save via the hotreload package. Atomic writes so a
// crash mid-save never strands the file.
//
// Precedence (lowest → highest, last wins on conflict):
//  1. baked defaults (DefaultUserOverrides)
//  2. system overrides    /etc/overkill/user.yaml
//  3. user overrides      ~/.config/overkill/user.yaml
//  4. workspace overrides $PWD/.overkill/user.yaml
//  5. admin-enforced      /etc/overkill/enforced.yaml (LOCKED — can't be overridden by user/workspace)
//
// Admin-enforced sits on top so an org deployment can lock down a
// subset (e.g. scanners always-on) while users still control the
// rest (e.g. model picker, persona, vim mode).
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

// UserOverridesSchemaVersion bumps on breaking renames. The loader
// runs forward migrations transparently; the file is rewritten with
// the new version on save.
const UserOverridesSchemaVersion = 1

// UserOverrides is the v2.0 user-controlled configuration. Every
// field is optional with a zero-value-means-default convention so
// partial files load cleanly.
type UserOverrides struct {
	SchemaVersion int              `yaml:"schema_version"`
	Profile       string           `yaml:"profile,omitempty"` // yolo|default|paranoid|enterprise (see ApplyProfile)
	Basic         BasicSettings    `yaml:"basic"`
	Advanced      AdvancedSettings `yaml:"advanced"`
}

// BasicSettings — the 8 controls a daily user touches. Fits one screen.
type BasicSettings struct {
	Model              string          `yaml:"model,omitempty"`
	ContextBudget      float64         `yaml:"context_budget,omitempty"` // 0.0-1.0 fraction of the model's window
	Tools              map[string]bool `yaml:"tools,omitempty"`          // name → enabled
	Theme              string          `yaml:"theme,omitempty"`
	VimMode            *bool           `yaml:"vim_mode,omitempty"`
	CostCapMonthly     float64         `yaml:"cost_cap_monthly,omitempty"` // USD; 0 = no cap (just track)
	AutoCompactPercent float64         `yaml:"auto_compact_percent,omitempty"`
	ConfirmWrites      *bool           `yaml:"confirm_writes,omitempty"`
}

// AdvancedSettings — everything else. Nil sub-sections mean "use default."
type AdvancedSettings struct {
	SystemPrompt PromptOverride                `yaml:"system_prompt,omitempty"`
	Tools        map[string]ToolUserConfig     `yaml:"tools,omitempty"`
	Scanners     ScannerToggles                `yaml:"scanners,omitempty"`
	Compaction   CompactionUserConfig          `yaml:"compaction,omitempty"`
	MCPServers   []MCPServerUserConfig         `yaml:"mcp_servers,omitempty"`
	Skills       SkillsUserConfig              `yaml:"skills,omitempty"`
	Hooks        HooksUserConfig               `yaml:"hooks,omitempty"`
	Memory       MemoryUserConfig              `yaml:"memory,omitempty"`
	Providers    map[string]ProviderUserConfig `yaml:"providers,omitempty"`
	Persona      PersonaUserConfig             `yaml:"persona,omitempty"`
	Telemetry    TelemetryUserConfig           `yaml:"telemetry,omitempty"`
	Permissions  PermissionsUserConfig         `yaml:"permissions,omitempty"`
	Board        BoardUserConfig               `yaml:"board,omitempty"`
}

// PromptOverride lets the user patch or replace the baked system
// prompt. Mode picks which strategy applies; the other fields are
// ignored on a mode mismatch.
type PromptOverride struct {
	Mode      string            `yaml:"mode,omitempty"`      // "default" | "patch" | "replace"
	Patch     string            `yaml:"patch,omitempty"`     // unified diff over the baseline
	Replace   string            `yaml:"replace,omitempty"`   // full override (Go text/template)
	Variables map[string]string `yaml:"variables,omitempty"` // template vars exposed to Replace
}

// ToolUserConfig is the per-tool user override. Enabled mirrors the
// Basic-tab toggle for visibility; Config is the kv blob the tool's
// ApplyConfig handler consumes.
type ToolUserConfig struct {
	Enabled bool           `yaml:"enabled"`
	Config  map[string]any `yaml:"config,omitempty"`
}

// ScannerToggles controls each scanner family. Defaults are dictated
// by the active profile (see ApplyProfile).
type ScannerToggles struct {
	Command             ScannerOnOff `yaml:"command,omitempty"`
	Injection           ScannerOnOff `yaml:"injection,omitempty"`
	PromptInjectBrowser ScannerOnOff `yaml:"prompt_inject_browser,omitempty"`
}

type ScannerOnOff struct {
	Enabled *bool `yaml:"enabled"`
}

type CompactionUserConfig struct {
	Enabled       *bool   `yaml:"enabled,omitempty"`
	Model         string  `yaml:"model,omitempty"`
	SoftThreshold float64 `yaml:"soft_threshold,omitempty"`
	HardThreshold float64 `yaml:"hard_threshold,omitempty"`
	PreserveLast  int     `yaml:"preserve_last,omitempty"`
	Strategy      string  `yaml:"strategy,omitempty"` // lcm | summarize-and-truncate | aggressive
}

type MCPServerUserConfig struct {
	Name                string   `yaml:"name"`
	Enabled             *bool    `yaml:"enabled,omitempty"`
	AllowedTools        []string `yaml:"allowed_tools,omitempty"`
	AllowedPathPrefixes []string `yaml:"allowed_path_prefixes,omitempty"`
	MaxBytesPerCall     int      `yaml:"max_bytes_per_call,omitempty"`
	Trusted             *bool    `yaml:"trusted,omitempty"`
}

type SkillsUserConfig struct {
	CustomPath string          `yaml:"custom_path,omitempty"`
	AutoLoad   *bool           `yaml:"auto_load,omitempty"`
	Active     map[string]bool `yaml:"active,omitempty"`
}

type HooksUserConfig struct {
	PreToolUse  []HookEntry `yaml:"pre_tool_use,omitempty"`
	PostToolUse []HookEntry `yaml:"post_tool_use,omitempty"`
	OnError     []HookEntry `yaml:"on_error,omitempty"`
	OnStop      []HookEntry `yaml:"on_stop,omitempty"`
}

type HookEntry struct {
	Matcher     string `yaml:"matcher,omitempty"`
	Command     string `yaml:"command"`
	Description string `yaml:"description,omitempty"`
}

type MemoryUserConfig struct {
	Enabled        *bool  `yaml:"enabled,omitempty"`
	MaxEntries     int    `yaml:"max_entries,omitempty"`
	TTLDays        int    `yaml:"ttl_days,omitempty"`
	EmbeddingModel string `yaml:"embedding_model,omitempty"`
}

// ProviderUserConfig — credentials reference. Actual secrets live in
// the sibling secrets.yaml so a casual `cat user.yaml` paste doesn't
// leak keys. ${secret:openai_key} indirection.
type ProviderUserConfig struct {
	APIKeyRef string         `yaml:"api_key,omitempty"` // "${secret:openai_key}" or literal
	BaseURL   string         `yaml:"base_url,omitempty"`
	Models    map[string]any `yaml:"models,omitempty"`
}

type PersonaUserConfig struct {
	Tone      string `yaml:"tone,omitempty"`      // terse | normal | verbose
	Style     string `yaml:"style,omitempty"`     // senior | pair | tutor | brutal
	Directive string `yaml:"directive,omitempty"` // appended to system prompt
}

type TelemetryUserConfig struct {
	EventLog       *bool `yaml:"event_log,omitempty"`
	FlightRecorder *bool `yaml:"flight_recorder,omitempty"`
	RetentionDays  int   `yaml:"retention_days,omitempty"`
	VerifyOnBoot   *bool `yaml:"verify_on_boot,omitempty"`
}

type PermissionsUserConfig struct {
	AutoApproveAll         *bool `yaml:"auto_approve_all,omitempty"`
	SkipDestructiveConfirm *bool `yaml:"skip_destructive_confirm,omitempty"`
	// RequireApprovalTools lists tool names that must always prompt for
	// operator approval even when AutoApproveAll is false. Used by the
	// "remote" profile to gate shell, patch, and git-push variants.
	RequireApprovalTools []string `yaml:"require_approval_tools,omitempty"`
	// DeniedTools lists tool names that are unconditionally blocked.
	DeniedTools []string `yaml:"denied_tools,omitempty"`
	// AllowedWebDomains restricts web-fetch calls to this domain list.
	// An empty slice means no restriction (fetch anything). The "remote"
	// profile leaves this empty by default; operators fill it in.
	AllowedWebDomains []string `yaml:"allowed_web_domains,omitempty"`
}

type BoardUserConfig struct {
	Enabled         *bool               `yaml:"enabled,omitempty"`
	AutoDispatch    *bool               `yaml:"auto_dispatch,omitempty"`
	MaxConcurrent   int                 `yaml:"max_concurrent,omitempty"`
	DefaultEffort   string              `yaml:"default_effort,omitempty"`
	DefaultPriority string              `yaml:"default_priority,omitempty"`
	ReviewPipeline  []BoardPipelineStep `yaml:"review_pipeline,omitempty"`
	OvenActions     []BoardOvenAction   `yaml:"oven_actions,omitempty"`
}

type BoardPipelineStep struct {
	Subagent    string         `yaml:"subagent"`
	BlockOnFail bool           `yaml:"block_on_fail"`
	Args        map[string]any `yaml:"args,omitempty"`
}

type BoardOvenAction struct {
	Kind     string         `yaml:"kind"`
	AutoFire bool           `yaml:"auto_fire,omitempty"`
	Args     map[string]any `yaml:"args,omitempty"`
}

// DefaultUserOverrides returns the baked-in starting point. Identical
// to applying the "yolo" profile against an empty struct — see
// ApplyProfile for the actual field values.
func DefaultUserOverrides() *UserOverrides {
	u := &UserOverrides{SchemaVersion: UserOverridesSchemaVersion}
	_ = ApplyProfile(u, "yolo")
	return u
}

// LoadUserOverrides reads the user.yaml at path. Missing file is fine
// (returns DefaultUserOverrides); parse errors are surfaced because
// silently dropping a hand-written config is worse than failing loud.
func LoadUserOverrides(path string) (*UserOverrides, error) {
	if path == "" {
		return DefaultUserOverrides(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultUserOverrides(), nil
		}
		return nil, fmt.Errorf("config: read user.yaml: %w", err)
	}
	if len(data) == 0 {
		return DefaultUserOverrides(), nil
	}
	out := DefaultUserOverrides()
	if err := yaml.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("config: parse user.yaml: %w", err)
	}
	migrateUserOverrides(out)
	// If the file declares a profile, apply it FIRST so the profile's
	// defaults set the baseline, then the file's explicit fields win
	// on top (yaml.Unmarshal already did the per-field merge).
	// We re-apply by hand to handle the precedence cleanly:
	if out.Profile != "" && out.Profile != "default" {
		// Profile-derived defaults are applied as a fresh struct, then
		// the parsed file overlays. We already unmarshaled into a
		// default'd struct, so the file's explicit "true" wins over
		// a profile-implied "false" and vice versa. Good enough for
		// v1; an explicit two-pass merge can land later.
	}
	return out, nil
}

// SaveUserOverrides writes the file atomically. Bumps SchemaVersion
// to the current one so a fresh write always reflects the binary's
// expected shape.
func SaveUserOverrides(path string, u *UserOverrides) error {
	if u == nil {
		return fmt.Errorf("config: nil user overrides")
	}
	u.SchemaVersion = UserOverridesSchemaVersion
	data, err := yaml.Marshal(u)
	if err != nil {
		return fmt.Errorf("config: marshal user.yaml: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: mkdir for user.yaml: %w", err)
	}
	return atomicfile.WriteFile(path, data, 0o644)
}

// UserOverridesPath returns the canonical XDG path. Honours
// $XDG_CONFIG_HOME; falls back to ~/.config/overkill/user.yaml.
func UserOverridesPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "overkill", "user.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: home dir: %w", err)
	}
	return filepath.Join(home, ".config", "overkill", "user.yaml"), nil
}

// migrateUserOverrides runs forward migrations on a parsed file so
// older versions transparently upgrade. Each migration is responsible
// for renaming/defaulting fields it knows about, then bumping the
// SchemaVersion.
func migrateUserOverrides(u *UserOverrides) {
	if u == nil {
		return
	}
	// v0 → v1: no-op (initial release).
	if u.SchemaVersion < 1 {
		u.SchemaVersion = 1
	}
}
