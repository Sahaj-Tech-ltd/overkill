package theme

import (
	"sync"

	"github.com/charmbracelet/lipgloss"
)

type Theme interface {
	Background() lipgloss.Color
	Foreground() lipgloss.Color
	Text() lipgloss.Color
	TextMuted() lipgloss.Color
	TextBold() lipgloss.Color
	Primary() lipgloss.Color
	Secondary() lipgloss.Color
	Accent() lipgloss.Color
	Success() lipgloss.Color
	Warning() lipgloss.Color
	Error() lipgloss.Color
	Border() lipgloss.Color
	BorderFocused() lipgloss.Color
	BorderUnfocused() lipgloss.Color
	PanelBackground() lipgloss.Color
	PanelBorder() lipgloss.Color
	PanelActive() lipgloss.Color
	PanelInactive() lipgloss.Color
	EditorBackground() lipgloss.Color
	EditorBorder() lipgloss.Color
	EditorCursor() lipgloss.Color
	EditorPlaceholder() lipgloss.Color
	StatusBarBackground() lipgloss.Color
	StatusBarText() lipgloss.Color
	StatusBarBorder() lipgloss.Color
	DialogBackground() lipgloss.Color
	DialogBorder() lipgloss.Color
	DialogText() lipgloss.Color
	DialogAccent() lipgloss.Color
	DialogHighlight() lipgloss.Color
	MessageUserBackground() lipgloss.Color
	MessageUserText() lipgloss.Color
	MessageAssistantBackground() lipgloss.Color
	MessageAssistantText() lipgloss.Color
	MessageToolBackground() lipgloss.Color
	MessageToolText() lipgloss.Color
	MessageErrorText() lipgloss.Color
	SidebarBackground() lipgloss.Color
	SidebarBorder() lipgloss.Color
	SidebarActiveTab() lipgloss.Color
	SidebarInactiveTab() lipgloss.Color
}

var (
	currentTheme Theme
	themeOnce    sync.Once
	themeMu      sync.RWMutex
)

func init() {
	currentTheme = &Catppuccin{}
}

func CurrentTheme() Theme {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return currentTheme
}

func SetTheme(t Theme) {
	themeMu.Lock()
	defer themeMu.Unlock()
	currentTheme = t
}
