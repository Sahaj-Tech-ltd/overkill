// Package extensions — stub backends for plugins / hooks / MCP.
//
// These exist so the Manager's surface is shaped correctly while the
// real adapters land. Each stub:
//   - returns its Kind so the registry slot is reserved,
//   - returns an empty List (no leakage of partial state),
//   - returns ErrUnsupported on Enable/Disable so callers know runtime
//     toggling isn't supported via the unified surface (use the
//     backend's own CLI for now).
//
// When the real adapter ships, replace the stub via Manager.AddBackend
// (last-wins semantics).
package extensions

// PluginsStubBackend reserves the plugins slot until the plugin registry
// gains a runtime-toggle surface. Plugin lifecycle today is config-driven
// (~/.overkill/config.toml [plugins] table + filesystem scan), so
// runtime Enable/Disable returns ErrUnsupported.
type PluginsStubBackend struct{}

func (PluginsStubBackend) Kind() Kind                       { return KindPlugin }
func (PluginsStubBackend) List() ([]Extension, error)       { return nil, nil }
func (PluginsStubBackend) Enable(id string) error           { _ = id; return ErrUnsupported }
func (PluginsStubBackend) Disable(id string) error          { _ = id; return ErrUnsupported }

// HooksStubBackend reserves the hooks slot. Hooks today are loaded from
// ~/.overkill/hooks/<point>/*.sh — runtime toggling means removing the
// file, not flipping an in-memory flag. Surfacing as ErrUnsupported is
// the right answer until a hooks Enable/Disable verb exists.
type HooksStubBackend struct{}

func (HooksStubBackend) Kind() Kind                       { return KindHook }
func (HooksStubBackend) List() ([]Extension, error)       { return nil, nil }
func (HooksStubBackend) Enable(id string) error           { _ = id; return ErrUnsupported }
func (HooksStubBackend) Disable(id string) error          { _ = id; return ErrUnsupported }

// MCPStubBackend reserves the MCP slot. MCP servers come and go on
// their own lifecycle (started by config, managed by internal/mcp).
// Manager Enable/Disable returns ErrUnsupported; use `overkill mcp`
// commands for direct control.
type MCPStubBackend struct{}

func (MCPStubBackend) Kind() Kind                       { return KindMCP }
func (MCPStubBackend) List() ([]Extension, error)       { return nil, nil }
func (MCPStubBackend) Enable(id string) error           { _ = id; return ErrUnsupported }
func (MCPStubBackend) Disable(id string) error          { _ = id; return ErrUnsupported }
