package main

import (
	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	pluginpkg "github.com/Sahaj-Tech-ltd/ethos/internal/plugin"
)

// pluginHostBridge satisfies plugin.HostBridge. It holds a pointer-to-pointer
// to the live config so config reloads (via /config) are reflected on the
// next host.config_get call.
type pluginHostBridge struct {
	cfgRef **config.Config
}

func (b *pluginHostBridge) SessionInfo() pluginpkg.SessionInfo {
	// Without a circular dep into the agent we keep this minimal; the TUI
	// can replace this bridge with a richer one once it has the agent.
	return pluginpkg.SessionInfo{ID: "ethos", Title: "ethos session"}
}

func (b *pluginHostBridge) ConfigValue(key string) (any, bool) {
	if b == nil || b.cfgRef == nil || *b.cfgRef == nil {
		return nil, false
	}
	cfg := *b.cfgRef
	switch key {
	case "agent.default_model":
		return cfg.Agent.DefaultModel, true
	case "agent.default_provider":
		return cfg.Agent.DefaultProvider, true
	case "personality.level":
		return cfg.Personality.Level, true
	case "personality.language":
		return cfg.Personality.Language, true
	}
	return nil, false
}

func (b *pluginHostBridge) Toast(kind, text string) {
	// Toasts are best-effort; without a TUI program reference here we drop
	// them. The TUI wires its own bridge with PushNotification access.
	_ = kind
	_ = text
}
