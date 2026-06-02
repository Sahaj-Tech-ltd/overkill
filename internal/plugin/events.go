package plugin

// Event names the plugin runtime understands. Keep this list in lockstep
// with the wire-protocol documentation — these are the only strings plugins
// can put in manifest.permissions.events.
const (
	EventToolCall      = "tool_call"
	EventChatMessage   = "chat_message"
	EventCompact       = "compact"
	EventSessionSwitch = "session_switch"
	EventError         = "error"
)

// KnownEvents is exported so the discovery / doctor surfaces can validate
// permissions at install time.
var KnownEvents = []string{
	EventToolCall,
	EventChatMessage,
	EventCompact,
	EventSessionSwitch,
	EventError,
}
