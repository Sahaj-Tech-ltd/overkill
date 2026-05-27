// Package tui — /compose handler. Opens $EDITOR with the current editor
// contents so the user can draft long prompts in vi/nano/whatever without
// fighting the inline editor's reflow. On editor exit, the saved file
// content replaces the editor value.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	tuitypes "github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/types"
)

// composeFinishedMsg carries the result of an external-editor compose
// session back into the Bubble Tea Update loop.
type composeFinishedMsg struct {
	content string
	err     error
}

// resolveEditor picks $VISUAL → $EDITOR → vi, in that order. Matches the
// convention git/less/most-CLI-tools use, so users with established
// preferences don't get surprised.
func resolveEditor() string {
	if v := strings.TrimSpace(os.Getenv("VISUAL")); v != "" {
		return v
	}
	if e := strings.TrimSpace(os.Getenv("EDITOR")); e != "" {
		return e
	}
	return "vi"
}

// runCompose opens $EDITOR with the current editor value as scratch
// content. When the editor exits cleanly the saved text replaces the
// in-app editor's value. Errors surface as toasts; the original editor
// value is left untouched on failure.
func (m *appModel) runCompose() tea.Cmd {
	editor := m.chatPage.Editor()
	if editor == nil {
		return m.toastCmd("compose: editor not ready", "warning")
	}
	seed := editor.Value()

	tmp, err := os.CreateTemp("", fmt.Sprintf("overkill-compose-%d-*.md", time.Now().Unix()))
	if err != nil {
		return m.toastCmd("compose: tmpfile: "+err.Error(), "error")
	}
	path := tmp.Name()
	if seed != "" {
		if _, err := tmp.WriteString(seed); err != nil {
			_ = tmp.Close()
			_ = os.Remove(path)
			return m.toastCmd("compose: write: "+err.Error(), "error")
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return m.toastCmd("compose: close: "+err.Error(), "error")
	}

	// Split $EDITOR on whitespace so values like `code --wait` work.
	editorCmd := resolveEditor()
	parts := strings.Fields(editorCmd)
	if len(parts) == 0 {
		_ = os.Remove(path)
		return m.toastCmd("compose: empty $EDITOR", "warning")
	}
	args := append(parts[1:], path)
	c := exec.Command(parts[0], args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return tea.ExecProcess(c, func(runErr error) tea.Msg {
		// Always try to read back. Editors like vim sometimes exit
		// non-zero on :cquit or signals but still leave saved content.
		data, readErr := os.ReadFile(path)
		_ = os.Remove(filepath.Clean(path))
		if runErr != nil && len(data) == 0 {
			return composeFinishedMsg{err: runErr}
		}
		if readErr != nil {
			return composeFinishedMsg{err: readErr}
		}
		// Trim a single trailing newline editors add on save so we don't
		// re-submit with a phantom blank line.
		content := strings.TrimRight(string(data), "\n")
		return composeFinishedMsg{content: content}
	})
}

// applyCompose handles the editor-exit message: replaces the editor's
// value with the composed text and emits a confirmation toast. Kept in
// this file so the message type, the runner, and the Update-side handler
// stay co-located.
func (m *appModel) applyCompose(msg composeFinishedMsg) tea.Cmd {
	if msg.err != nil {
		return m.toastCmd("compose: "+msg.err.Error(), "error")
	}
	editor := m.chatPage.Editor()
	if editor == nil {
		return nil
	}
	editor.SetValue(msg.content)
	if msg.content == "" {
		return m.toastCmd("compose: editor empty (no content saved)", "info")
	}
	return func() tea.Msg {
		return tuitypes.ToastMsg{Text: "compose: loaded from $EDITOR", Kind: "info"}
	}
}
