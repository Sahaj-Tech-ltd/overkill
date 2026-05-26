// Package tui — mouse event handler for clickable copy chips.
//
// Bubble Tea's mouse events arrive as tea.MouseMsg with X/Y in cell
// coordinates over the program output. We consume them only to drive
// the code-block copy chips; everything else is left for the terminal's
// native handling so users keep their muscle memory.
//
// Mouse capture mode (tea.WithMouseCellMotion) takes over native
// click-and-drag selection. Modern terminals (iTerm2, kitty, wezterm,
// alacritty, gnome-terminal) preserve native selection while in this
// mode if the user holds Option/Alt — we surface this once via toast
// the first time the user accidentally clicks empty space.
package tui

import (
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/chat"
)

// altSelectHintShown tracks whether the "Alt+drag to select text" tip
// has already been emitted. Atomic because mouse events run on the
// Bubble Tea Update goroutine but the hint can also fire from an init
// path in the future. One-shot — once true, never resets within a
// session.
var altSelectHintShown atomic.Bool

// handleMouse dispatches a Bubble Tea mouse event. Returns the tea.Cmd
// (typically a toast) and a bool indicating whether the event was
// consumed; the caller can ignore unconsumed events.
//
// Three event types matter:
//   - tea.MouseLeft + Press: click on a chip → copy that block
//   - tea.MouseMotion: update hover state so the chip renders
//     highlighted on the next frame
//   - everything else: pass through, the renderer ignores it
func (m *appModel) handleMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	switch msg.Type {
	case tea.MouseMotion:
		// Hover-tracking is decoupled from click so the chip preview
		// works even on terminals that send motion without click. We
		// only re-render if the hovered zone changed.
		newID := chat.HoveredIDForPoint(msg.X, msg.Y)
		if newID != chat.HoveredID() {
			chat.SetHoveredID(newID)
			// Force a re-render. Returning a no-op cmd that emits a
			// nil message would be wrong (cmd must not return nil msg);
			// instead a tick is the standard re-render trigger.
			return nil, true
		}
		return nil, false
	case tea.MouseLeft:
		if msg.Action != tea.MouseActionPress {
			return nil, false
		}
		zone := chat.HitTest(msg.X, msg.Y)
		if zone == nil {
			// Click on empty space while mouse capture is on. First
			// time this happens, surface the Alt+drag tip so the user
			// learns how to do native selection. Subsequent clicks
			// are silent (no spam).
			if !altSelectHintShown.Swap(true) {
				return m.toastCmd("tip: hold Alt/Option and drag to select text natively (mouse capture is on for copy chips)", "info"), true
			}
			return nil, false
		}
		// OSC52-push the code body to the clipboard. The writeOSC52
		// helper lives in tui.go and works for terminals that support
		// the sequence (iTerm2, kitty, wezterm, alacritty, tmux with
		// set-clipboard, screen with osc52 enabled).
		writeOSC52(zone.Body)
		lang := zone.Lang
		if lang == "" {
			lang = "code"
		}
		preview := previewFirstLine(zone.Body)
		return m.toastCmd("copy: "+lang+" — "+preview, "success"), true
	}
	return nil, false
}

// previewFirstLine returns at most 40 characters of the first line of
// body, with an ellipsis when truncated. Mirrors the format used by
// the slash-command path so toast wording is consistent.
func previewFirstLine(body string) string {
	end := len(body)
	for i, c := range body {
		if c == '\n' {
			end = i
			break
		}
	}
	first := body[:end]
	if len(first) > 40 {
		first = first[:37] + "..."
	}
	return first
}
