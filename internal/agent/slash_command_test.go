package agent

import (
	"os"
	"strings"
	"testing"
)

// TestSlashCommands_AllRecognized verifies every user-facing slash command
// is registered in knownCommands. BUG #162, #163.
func TestSlashCommands_AllRecognized(t *testing.T) {
	required := []string{
		"plan", "build", "think", "safe", "yolo", "auto",
		"compact", // BUG #163: missing — FIXED
	}

	for _, cmd := range required {
		if _, ok := knownCommands[cmd]; !ok {
			t.Errorf("%q not in knownCommands — /%s silently becomes plain text", cmd, cmd)
		}
	}
}

// TestCompact_HandlerExists verifies /compact has a handler.
// BUG #163: handleSlashCommand has no "compact" case.
func TestCompact_HandlerExists(t *testing.T) {
	if _, ok := knownCommands["compact"]; !ok {
		t.Fatal("'compact' not in knownCommands — cannot test handler")
	}
	a := &Agent{}
	msg, handled := a.handleSlashCommand(&SlashCommand{Command: "compact"})
	if !handled {
		t.Error("/compact handler returns handled=false — command silently ignored")
	}
	if msg == "" {
		t.Error("/compact handler returns empty message")
	}
}

// TestStream_ParseSlashCommandsInSource verifies ParseSlashCommand is
// callable from the Stream path. BUG #162.
// Reads stream.go to check if ParseSlashCommand appears in StreamWithAttachments.
func TestStream_ParseSlashCommandsInSource(t *testing.T) {
	data, err := os.ReadFile("stream.go")
	if err != nil {
		t.Skipf("cannot read stream.go: %v", err)
	}
	content := string(data)

	// StreamWithAttachments should parse slash commands.
	// Find the function body and check.
	idx := strings.Index(content, "func (a *Agent) StreamWithAttachments")
	if idx < 0 {
		t.Fatal("StreamWithAttachments not found in stream.go")
	}
	// Look for ParseSlashCommand in the function body (next ~200 lines)
	body := content[idx : idx+4000]
	if !strings.Contains(body, "ParseSlashCommand") {
		t.Error("BUG #162 CONFIRMED: ParseSlashCommand not called in StreamWithAttachments")
		t.Error("All slash commands from TUI/gateways silently become plain text")
	} else {
		t.Log("ParseSlashCommand found in StreamWithAttachments — fix applied")
	}
}
