package theme

import (
	"sort"

	"github.com/charmbracelet/lipgloss"
)

// TokyoNight is the Tokyo Night Storm palette.
type TokyoNight struct{}

func (t *TokyoNight) Background() lipgloss.Color                 { return "#24283b" }
func (t *TokyoNight) Foreground() lipgloss.Color                 { return "#c0caf5" }
func (t *TokyoNight) Text() lipgloss.Color                       { return "#c0caf5" }
func (t *TokyoNight) TextMuted() lipgloss.Color                  { return "#565f89" }
func (t *TokyoNight) TextBold() lipgloss.Color                   { return "#c0caf5" }
func (t *TokyoNight) Primary() lipgloss.Color                    { return "#7aa2f7" }
func (t *TokyoNight) Secondary() lipgloss.Color                  { return "#9aa5ce" }
func (t *TokyoNight) Accent() lipgloss.Color                     { return "#bb9af7" }
func (t *TokyoNight) Success() lipgloss.Color                    { return "#9ece6a" }
func (t *TokyoNight) Warning() lipgloss.Color                    { return "#e0af68" }
func (t *TokyoNight) Error() lipgloss.Color                      { return "#f7768e" }
func (t *TokyoNight) Border() lipgloss.Color                     { return "#3b4261" }
func (t *TokyoNight) BorderFocused() lipgloss.Color              { return "#7aa2f7" }
func (t *TokyoNight) BorderUnfocused() lipgloss.Color            { return "#3b4261" }
func (t *TokyoNight) PanelBackground() lipgloss.Color            { return "#1f2335" }
func (t *TokyoNight) PanelBorder() lipgloss.Color                { return "#3b4261" }
func (t *TokyoNight) PanelActive() lipgloss.Color                { return "#7aa2f7" }
func (t *TokyoNight) PanelInactive() lipgloss.Color              { return "#292e42" }
func (t *TokyoNight) EditorBackground() lipgloss.Color           { return "#24283b" }
func (t *TokyoNight) EditorBorder() lipgloss.Color               { return "#3b4261" }
func (t *TokyoNight) EditorCursor() lipgloss.Color               { return "#c0caf5" }
func (t *TokyoNight) EditorPlaceholder() lipgloss.Color          { return "#565f89" }
func (t *TokyoNight) StatusBarBackground() lipgloss.Color        { return "#1f2335" }
func (t *TokyoNight) StatusBarText() lipgloss.Color              { return "#c0caf5" }
func (t *TokyoNight) StatusBarBorder() lipgloss.Color            { return "#3b4261" }
func (t *TokyoNight) DialogBackground() lipgloss.Color           { return "#1f2335" }
func (t *TokyoNight) DialogBorder() lipgloss.Color               { return "#7aa2f7" }
func (t *TokyoNight) DialogText() lipgloss.Color                 { return "#c0caf5" }
func (t *TokyoNight) DialogAccent() lipgloss.Color               { return "#bb9af7" }
func (t *TokyoNight) DialogHighlight() lipgloss.Color            { return "#e0af68" }
func (t *TokyoNight) MessageUserBackground() lipgloss.Color      { return "#292e42" }
func (t *TokyoNight) MessageUserText() lipgloss.Color            { return "#c0caf5" }
func (t *TokyoNight) MessageAssistantBackground() lipgloss.Color { return "#24283b" }
func (t *TokyoNight) MessageAssistantText() lipgloss.Color       { return "#c0caf5" }
func (t *TokyoNight) MessageToolBackground() lipgloss.Color      { return "#1f2335" }
func (t *TokyoNight) MessageToolText() lipgloss.Color            { return "#9aa5ce" }
func (t *TokyoNight) MessageErrorText() lipgloss.Color           { return "#f7768e" }
func (t *TokyoNight) SidebarBackground() lipgloss.Color          { return "#1f2335" }
func (t *TokyoNight) SidebarBorder() lipgloss.Color              { return "#3b4261" }
func (t *TokyoNight) SidebarActiveTab() lipgloss.Color           { return "#7aa2f7" }
func (t *TokyoNight) SidebarInactiveTab() lipgloss.Color         { return "#565f89" }

// Registry returns the available themes — built-ins plus any TOML
// themes loaded via LoadFromDir. Built-ins always take precedence on
// name collision (LoadFromDir rejects conflicting names at load time,
// but this is the second line of defense).
func Registry() map[string]Theme {
	r := map[string]Theme{
		"catppuccin":  &Catppuccin{},
		"tokyo-night": &TokyoNight{},
	}
	for k, v := range FileThemes() {
		if _, exists := r[k]; exists {
			continue
		}
		r[k] = v
	}
	return r
}

// Names returns the registered theme names in display order: built-ins
// first (catppuccin, tokyo-night) then user themes alphabetized. The
// stable ordering matters because the picker dialog uses positional
// indexes for keyboard navigation.
func Names() []string {
	out := []string{"catppuccin", "tokyo-night"}
	file := FileThemes()
	userNames := make([]string, 0, len(file))
	for name := range file {
		userNames = append(userNames, name)
	}
	sort.Strings(userNames)
	return append(out, userNames...)
}

// ByName returns a theme by name (case-sensitive). Returns nil if not
// found.
func ByName(name string) Theme {
	if t, ok := Registry()[name]; ok {
		return t
	}
	return nil
}
