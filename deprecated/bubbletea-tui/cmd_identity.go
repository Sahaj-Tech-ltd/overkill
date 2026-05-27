// Package tui — /identity slash command.
//
// Surfaces the agent's baseline self-model (master plan §4.16). The
// identity loads on every boot — even on LevelOff — and is what
// makes the agent feel like a defined character instead of a
// ChatGPT-clone form-filler. Power users who want to fork the voice
// can override via ~/.overkill/identity.toml; /identity is the
// "what am I forking" pre-check.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
)

// runIdentity prints the loaded identity into the chat as an
// assistant-style message. Uses the chat surface (not a toast)
// because the content is multi-line prose — toasts truncate and
// auto-dismiss, neither of which fits a "here's who I am" reveal.
func (m *appModel) runIdentity() tea.Cmd {
	if m.person == nil {
		return m.toastCmd("identity: personality engine not wired", "warning")
	}
	id := m.person.Identity()
	if id == nil {
		// Fall back to a fresh load — defensive against future code
		// paths that construct Personality without going through New.
		loaded, _ := personality.LoadIdentity()
		id = loaded
	}
	if id == nil {
		return m.toastCmd("identity: no identity loaded", "warning")
	}

	display := id.Display()
	// Render as an assistant message in the chat scrollback so it
	// participates in the existing render pipeline. We use the chat
	// surface (not a toast) because the content is multi-line prose
	// — toasts truncate and auto-dismiss, neither fits a /identity
	// reveal where the user is reading 5 short paragraphs.
	m.chatPage.AppendRaw("assistant", display)
	return nil
}

// _ unused fmt import guard — left here so a future addition that
// wants Sprintf doesn't have to re-add the import.
var _ = fmt.Sprintf
