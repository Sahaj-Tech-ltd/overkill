package agent

import "github.com/Sahaj-Tech-ltd/overkill/internal/settings"

// AgentSettings are runtime-tunable agent parameters surfaced via the
// settings registry (~/.overkill/settings.toml). Each field has a TOML
// key, default value, and validation bounds declared via struct tags.
type AgentSettings struct {
	// MaxToolOutputChars is the universal safety-net truncation limit
	// applied to every tool output before it enters history. See the
	// Agent Config's MaxToolOutputChars field for runtime wiring.
	// Default 8000; set to 0 to disable universal truncation.
	MaxToolOutputChars int `setting:"max_tool_output_chars" default:"8000" min:"0" max:"50000" desc:"Maximum characters kept from each tool output (0 = unlimited)"`
}

func init() {
	settings.Register("agent", &AgentSettings{})
}
