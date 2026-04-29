package theme

import "github.com/charmbracelet/lipgloss"

type Catppuccin struct{}

func (c *Catppuccin) Background() lipgloss.Color              { return "#1e1e2e" }
func (c *Catppuccin) Foreground() lipgloss.Color              { return "#cdd6f4" }
func (c *Catppuccin) Text() lipgloss.Color                    { return "#cdd6f4" }
func (c *Catppuccin) TextMuted() lipgloss.Color               { return "#6c7086" }
func (c *Catppuccin) TextBold() lipgloss.Color                { return "#cdd6f4" }
func (c *Catppuccin) Primary() lipgloss.Color                 { return "#89b4fa" }
func (c *Catppuccin) Secondary() lipgloss.Color               { return "#a6adc8" }
func (c *Catppuccin) Accent() lipgloss.Color                  { return "#cba6f7" }
func (c *Catppuccin) Success() lipgloss.Color                 { return "#a6e3a1" }
func (c *Catppuccin) Warning() lipgloss.Color                 { return "#f9e2af" }
func (c *Catppuccin) Error() lipgloss.Color                   { return "#f38ba8" }
func (c *Catppuccin) Border() lipgloss.Color                  { return "#45475a" }
func (c *Catppuccin) BorderFocused() lipgloss.Color           { return "#89b4fa" }
func (c *Catppuccin) BorderUnfocused() lipgloss.Color         { return "#45475a" }
func (c *Catppuccin) PanelBackground() lipgloss.Color         { return "#181825" }
func (c *Catppuccin) PanelBorder() lipgloss.Color             { return "#45475a" }
func (c *Catppuccin) PanelActive() lipgloss.Color             { return "#89b4fa" }
func (c *Catppuccin) PanelInactive() lipgloss.Color           { return "#313244" }
func (c *Catppuccin) EditorBackground() lipgloss.Color        { return "#1e1e2e" }
func (c *Catppuccin) EditorBorder() lipgloss.Color            { return "#45475a" }
func (c *Catppuccin) EditorCursor() lipgloss.Color            { return "#f5e0dc" }
func (c *Catppuccin) EditorPlaceholder() lipgloss.Color       { return "#6c7086" }
func (c *Catppuccin) StatusBarBackground() lipgloss.Color     { return "#181825" }
func (c *Catppuccin) StatusBarText() lipgloss.Color           { return "#cdd6f4" }
func (c *Catppuccin) StatusBarBorder() lipgloss.Color         { return "#45475a" }
func (c *Catppuccin) DialogBackground() lipgloss.Color        { return "#1e1e2e" }
func (c *Catppuccin) DialogBorder() lipgloss.Color            { return "#89b4fa" }
func (c *Catppuccin) DialogText() lipgloss.Color              { return "#cdd6f4" }
func (c *Catppuccin) DialogAccent() lipgloss.Color            { return "#cba6f7" }
func (c *Catppuccin) DialogHighlight() lipgloss.Color         { return "#f9e2af" }
func (c *Catppuccin) MessageUserBackground() lipgloss.Color   { return "#313244" }
func (c *Catppuccin) MessageUserText() lipgloss.Color         { return "#cdd6f4" }
func (c *Catppuccin) MessageAssistantBackground() lipgloss.Color { return "#1e1e2e" }
func (c *Catppuccin) MessageAssistantText() lipgloss.Color    { return "#cdd6f4" }
func (c *Catppuccin) MessageToolBackground() lipgloss.Color   { return "#181825" }
func (c *Catppuccin) MessageToolText() lipgloss.Color         { return "#a6adc8" }
func (c *Catppuccin) MessageErrorText() lipgloss.Color        { return "#f38ba8" }
func (c *Catppuccin) SidebarBackground() lipgloss.Color       { return "#181825" }
func (c *Catppuccin) SidebarBorder() lipgloss.Color           { return "#45475a" }
func (c *Catppuccin) SidebarActiveTab() lipgloss.Color        { return "#89b4fa" }
func (c *Catppuccin) SidebarInactiveTab() lipgloss.Color      { return "#6c7086" }
