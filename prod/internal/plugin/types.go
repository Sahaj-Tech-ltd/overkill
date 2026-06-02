package plugin

import "encoding/json"

// ToolDecl is the schema a plugin sends via host.register_tool.
type ToolDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	ArgsSchema  json.RawMessage `json:"args_schema,omitempty"`
	RiskLevel   string          `json:"risk_level,omitempty"`
}

// CommandDecl is what host.register_command receives.
type CommandDecl struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// SessionInfo is the response shape for host.session_get. Kept small on
// purpose — plugins shouldn't snoop the full conversation.
type SessionInfo struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	MessageCount int    `json:"message_count"`
}

// ContextSnippet is one item returned by a plugin's context.provide. Title
// is rendered as a short header; Content is the body that gets prepended to
// the system prompt.
type ContextSnippet struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// ContextResponse is what context.provide returns.
type ContextResponse struct {
	Snippets []ContextSnippet `json:"snippets"`
}

// ToolWithPlugin pairs a registered tool with its owning plugin name. The
// agent's tool registry uses this to namespace tools as plugin:<name>:<tool>.
type ToolWithPlugin struct {
	Plugin string
	Tool   ToolDecl
}

// CommandWithPlugin is the same idea for slash commands.
type CommandWithPlugin struct {
	Plugin  string
	Command CommandDecl
}

// Status is the snapshot the /plugins dialog reads.
type Status struct {
	Name      string
	Version   string
	Running   bool
	LastError string
	Tools     int
	Commands  int
	Restarts  int
	Disabled  bool
}

// HostBridge is the abstract surface a plugin Client uses to satisfy
// host.* RPCs. The Manager implements it. Pulling this out of the Client
// keeps the wire layer testable in isolation.
type HostBridge interface {
	// SessionInfo returns the current session for host.session_get.
	SessionInfo() SessionInfo
	// ConfigValue returns a config value the plugin asked for. Returning
	// (nil, nil) is treated as "key exists but no value" — the host should
	// only return ErrCodePermissionDenied through the bridge by surfacing
	// the lookup as a permission failure on the manifest layer.
	ConfigValue(key string) (any, bool)
	// Toast displays a toast in the host UI. Best-effort.
	Toast(kind, text string)
}
