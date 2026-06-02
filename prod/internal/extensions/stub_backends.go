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

var _ Backend = PluginsStubBackend{}
var _ Backend = HooksStubBackend{}
var _ Backend = MCPStubBackend{}

type PluginsStubBackend struct{}

func (PluginsStubBackend) Kind() Kind                        { return KindPlugin }
func (PluginsStubBackend) List() ([]Extension, error)        { return nil, nil }
func (PluginsStubBackend) Get(id string) (*Extension, error) { return nil, ErrNotFound }
func (PluginsStubBackend) Enable(id string) error            { return ErrUnsupported }
func (PluginsStubBackend) Disable(id string) error           { return ErrUnsupported }

type HooksStubBackend struct{}

func (HooksStubBackend) Kind() Kind                        { return KindHook }
func (HooksStubBackend) List() ([]Extension, error)        { return nil, nil }
func (HooksStubBackend) Get(id string) (*Extension, error) { return nil, ErrNotFound }
func (HooksStubBackend) Enable(id string) error            { return ErrUnsupported }
func (HooksStubBackend) Disable(id string) error           { return ErrUnsupported }

type MCPStubBackend struct{}

func (MCPStubBackend) Kind() Kind                        { return KindMCP }
func (MCPStubBackend) List() ([]Extension, error)        { return nil, nil }
func (MCPStubBackend) Get(id string) (*Extension, error) { return nil, ErrNotFound }
func (MCPStubBackend) Enable(id string) error            { return ErrUnsupported }
func (MCPStubBackend) Disable(id string) error           { return ErrUnsupported }
