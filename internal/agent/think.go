package agent

// ThinkConfig governs preamble streaming before tool calls.
// When Enabled is true, the agent emits a brief natural-language
// preamble via the event callback before every tool execution,
// mimicking Codex's "thinking" messages.
type ThinkConfig struct {
	Enabled bool
	Verbose bool // reserved for future verbosity levels
}

// SetThinkConfig toggles preamble streaming. Safe to call from any
// goroutine; takes the agent mutex. Pass cfg with Enabled=false to
// disable.
func (a *Agent) SetThinkConfig(cfg ThinkConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinkConfig = cfg
}

// ThinkConfig returns the current think configuration. Safe for
// concurrent reads.
func (a *Agent) ThinkConfig() ThinkConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.thinkConfig
}

// thinkEnabled returns true when preamble streaming is active.
func (a *Agent) thinkEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.thinkConfig.Enabled
}

// generatePreamble maps tool names to short natural-language phrases
// suitable for streaming as "thinking" messages before tool execution.
// Kept under 10 words — brief and non-repetitive, like Codex does.
func generatePreamble(toolName string) string {
	switch toolName {
	case "bash", "shell":
		return "Running a shell command..."
	case "read_file":
		return "Reading a file..."
	case "write_file", "fs_write", "edit_file":
		return "Writing to a file..."
	case "patch":
		return "Applying an edit..."
	case "plan_set", "plan_create":
		return "Setting up the plan..."
	case "grep", "search_files", "search_content", "glob":
		return "Searching..."
	case "web_fetch", "http_get":
		return "Fetching a web page..."
	case "memory_search", "memory_query":
		return "Searching memory..."
	case "git", "git_diff", "git_status":
		return "Checking git..."
	case "browser_open", "browser_navigate":
		return "Opening a browser..."
	case "browser_screenshot":
		return "Taking a screenshot..."
	case "task", "subagent", "delegate":
		return "Delegating a subtask..."
	case "todo_write", "todo":
		return "Updating the task list..."
	case "ask_user", "question", "ask":
		return "Asking a question..."
	default:
		return "Working on it..."
	}
}
