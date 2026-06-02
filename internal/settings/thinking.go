package settings

// ThinkingSettings defines the UI-editable thinking level configuration.
// Registered with the settings registry so the TUI settings pane can
// display it without hardcoding field names.
type ThinkingSettings struct {
	// Level controls how much internal reasoning the model exposes.
	// Supported values: off, minimal, low, medium, high, x-high.
	Level string `setting:"level" default:"off" desc:"Extended thinking budget for the model"`
}

func init() {
	Register("thinking", &ThinkingSettings{})
}
