package plugin

import (
	"encoding/json"
	"fmt"
)

// Manifest is the plugin self-description returned from plugin.initialize.
// Permissions are enforced — any host RPC that touches a config key, an
// outbound tool call, or an event subscription is gated against this list.
type Manifest struct {
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Description string      `json:"description,omitempty"`
	Permissions Permissions `json:"permissions"`
}

// Permissions is the declared scope of host capabilities the plugin needs.
// Anything outside these lists is rejected with ErrCodePermissionDenied.
type Permissions struct {
	ConfigKeys []string `json:"config_keys,omitempty"`
	ToolsCall  []string `json:"tools_call,omitempty"`
	Events     []string `json:"events,omitempty"`
}

// AllowsConfigKey reports whether the manifest declared the given config key.
func (p Permissions) AllowsConfigKey(key string) bool {
	for _, k := range p.ConfigKeys {
		if k == key {
			return true
		}
	}
	return false
}

// AllowsTool reports whether the plugin may invoke the given host tool by
// name through its tool-call permission. (Plugins always own the tools they
// register themselves; this gate is for invoking *other* tools the host
// exposes.)
func (p Permissions) AllowsTool(name string) bool {
	for _, t := range p.ToolsCall {
		if t == name {
			return true
		}
	}
	return false
}

// AllowsEvent reports whether the plugin declared an interest in an event.
// Subscribing to an undeclared event is rejected.
func (p Permissions) AllowsEvent(ev string) bool {
	for _, e := range p.Events {
		if e == ev {
			return true
		}
	}
	return false
}

// ValidateManifest performs basic structural checks. Returns a permission
// denial only for clearly-invalid manifests; everything else is up to the
// caller (e.g. matching against a static plugin.toml).
func ValidateManifest(m Manifest) error {
	if m.Name == "" {
		return fmt.Errorf("plugin: manifest missing name")
	}
	if m.Version == "" {
		return fmt.Errorf("plugin: manifest missing version")
	}
	return nil
}

// permissionError builds a JSON-RPC error with a structured reason payload.
func permissionError(reason string) *RPCError {
	data, _ := json.Marshal(map[string]string{"reason": reason})
	return &RPCError{
		Code:    ErrCodePermissionDenied,
		Message: "permission denied: " + reason,
		Data:    data,
	}
}
