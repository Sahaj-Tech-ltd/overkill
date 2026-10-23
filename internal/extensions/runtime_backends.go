package extensions

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/mcp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plugin"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
)

// NewConfiguredManager inventories configuration without starting executables.
// A session replaces the hook adapter with its actual executing registry; an API
// server replaces MCP with its supervised live manager.
func NewConfiguredManager(reg *skills.Registry, cfg *config.Config, home string) *Manager {
	manager := NewManager()
	manager.AddBackend(NewSkillsBackend(reg))
	if cfg == nil {
		cfg = config.Default()
	}
	root := cfg.Plugins.Dir
	if root == "" && home != "" {
		root = filepath.Join(home, "plugins")
	}
	manager.AddBackend(&PluginsBackend{root: root, disabled: append([]string(nil), cfg.Plugins.Disabled...)})
	hooksDirectory := ""
	if home != "" {
		hooksDirectory = filepath.Join(home, "hooks")
	}
	manager.AddBackend(&HookFilesBackend{directory: hooksDirectory})
	manager.AddBackend(NewMCPBackend(cfg.MCP, nil))
	return manager
}

// PluginsBackend uses real discovery. The application has not connected a
// plugin process manager, so discovery is never presented as active execution.
type PluginsBackend struct {
	root     string
	disabled []string
}

func (*PluginsBackend) Kind() Kind { return KindPlugin }
func (b *PluginsBackend) List() ([]Extension, error) {
	found, err := plugin.Discover(b.root)
	if err != nil {
		return nil, err
	}
	out := make([]Extension, 0, len(found))
	for _, item := range found {
		enabled := true
		for _, disabled := range b.disabled {
			if disabled == item.Name {
				enabled = false
			}
		}
		description := "Discovered plugin"
		version := ""
		if item.StaticManifest != nil {
			description = item.StaticManifest.Description
			version = item.StaticManifest.Version
		}
		out = append(out, Extension{Kind: KindPlugin, ID: item.Name, Name: item.Name, Description: description, Source: b.root, UnsupportedReason: "Plugin runtime is not connected; edit startup configuration to change policy.", Metadata: map[string]string{"runtime_state": "discovered_not_running", "configured_enabled": strconv.FormatBool(enabled), "version": version}})
	}
	return out, nil
}
func (b *PluginsBackend) Get(id string) (*Extension, error) { return findExtension(b, id) }
func (b *PluginsBackend) Enable(id string) error            { return unsupported(b, id) }
func (b *PluginsBackend) Disable(id string) error           { return unsupported(b, id) }

// HookFilesBackend lists scripts accepted by the production trusted-file loader.
// These files apply to newly constructed sessions, not a hidden dummy runtime.
type HookFilesBackend struct{ directory string }

func (*HookFilesBackend) Kind() Kind { return KindHook }
func (b *HookFilesBackend) List() ([]Extension, error) {
	registry, err := hooks.NewRegistryFromDir(b.directory)
	if err != nil {
		return nil, err
	}
	items, err := NewHooksBackend(registry).List()
	for index := range items {
		items[index].Enabled = false
		items[index].CanToggle = false
		items[index].ToggleScope = ""
		items[index].UnsupportedReason = "Select a session to change its executing hook registry."
		items[index].Metadata["runtime_state"] = "available_for_new_sessions"
	}
	return items, err
}
func (b *HookFilesBackend) Get(id string) (*Extension, error) { return findExtension(b, id) }
func (b *HookFilesBackend) Enable(id string) error            { return unsupported(b, id) }
func (b *HookFilesBackend) Disable(id string) error           { return unsupported(b, id) }

// HooksBackend retains disabled file-hook closures so enable restores the same
// trusted loader's execution checks. Built-in policy hooks remain owner-managed.
type HooksBackend struct {
	mu       sync.Mutex
	registry *hooks.Registry
	known    map[string]hooks.Hook
}

func NewHooksBackend(reg *hooks.Registry) *HooksBackend {
	return &HooksBackend{registry: reg, known: map[string]hooks.Hook{}}
}
func (*HooksBackend) Kind() Kind { return KindHook }
func (b *HooksBackend) List() ([]Extension, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.registry == nil {
		return nil, nil
	}
	active := map[string]bool{}
	for _, list := range b.registry.ListAll() {
		for _, hook := range list {
			b.known[hook.Name] = hook
			active[hook.Name] = true
		}
	}
	out := make([]Extension, 0, len(b.known))
	for name, hook := range b.known {
		item := Extension{Kind: KindHook, ID: name, Name: name, Description: string(hook.Point) + " hook", Source: "runtime", Enabled: active[name], Metadata: map[string]string{"hook_point": string(hook.Point), "runtime_state": "registered"}}
		if !active[name] {
			item.Metadata["runtime_state"] = "disabled"
		}
		if strings.HasPrefix(name, "file:") {
			item.CanToggle = true
			item.ToggleScope = "session"
		} else {
			item.UnsupportedReason = "Application-owned hooks cannot be toggled."
		}
		out = append(out, item)
	}
	return out, nil
}
func (b *HooksBackend) Get(id string) (*Extension, error) { return findExtension(b, id) }
func (b *HooksBackend) Enable(id string) error {
	item, err := b.Get(id)
	if err != nil {
		return err
	}
	if !item.CanToggle {
		return fmt.Errorf("%w: %s", ErrUnsupported, item.UnsupportedReason)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, list := range b.registry.ListAll() {
		for _, hook := range list {
			if hook.Name == id {
				return nil
			}
		}
	}
	return b.registry.Register(b.known[id])
}
func (b *HooksBackend) Disable(id string) error {
	item, err := b.Get(id)
	if err != nil {
		return err
	}
	if !item.CanToggle {
		return fmt.Errorf("%w: %s", ErrUnsupported, item.UnsupportedReason)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.registry.Unregister(id)
	return nil
}

// MCPBackend exposes configured servers plus supervised connection state, never
// command arguments or environment secrets. Per-server runtime toggles do not
// exist in mcp.Manager and are explicitly unsupported here.
type MCPBackend struct {
	servers []config.MCPServer
	manager *mcp.Manager
}

func NewMCPBackend(cfg config.MCPConfig, manager *mcp.Manager) *MCPBackend {
	servers := make([]config.MCPServer, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		entry := config.MCPServer{Name: server.Name}
		if server.Enabled != nil {
			enabled := *server.Enabled
			entry.Enabled = &enabled
		}
		servers = append(servers, entry)
	}
	return &MCPBackend{servers: servers, manager: manager}
}
func (*MCPBackend) Kind() Kind { return KindMCP }
func (b *MCPBackend) List() ([]Extension, error) {
	statuses := map[string]mcp.ServerStatus{}
	if b.manager != nil {
		for _, status := range b.manager.Status() {
			statuses[status.Name] = status
		}
	}
	out := make([]Extension, 0, len(b.servers))
	for _, server := range b.servers {
		configured := server.Enabled == nil || *server.Enabled
		status := statuses[server.Name]
		state := "not_started"
		if !configured {
			state = "configured_disabled"
		}
		if status.Connected {
			state = "connected"
		} else if status.LastError != "" {
			state = "failed"
		}
		out = append(out, Extension{Kind: KindMCP, ID: server.Name, Name: server.Name, Description: "MCP server", Source: "configuration", Enabled: status.Connected, UnsupportedReason: "MCP per-server runtime toggling is unsupported; edit configuration and restart.", Metadata: map[string]string{"configured_enabled": strconv.FormatBool(configured), "runtime_state": state, "tools_count": strconv.Itoa(status.ToolsCount)}})
	}
	return out, nil
}
func (b *MCPBackend) Get(id string) (*Extension, error) { return findExtension(b, id) }
func (b *MCPBackend) Enable(id string) error            { return unsupported(b, id) }
func (b *MCPBackend) Disable(id string) error           { return unsupported(b, id) }
func findExtension(b Backend, id string) (*Extension, error) {
	items, err := b.List()
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == id {
			return &item, nil
		}
	}
	return nil, ErrNotFound
}
func unsupported(b Backend, id string) error {
	item, err := b.Get(id)
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: %s", ErrUnsupported, item.UnsupportedReason)
}
