// Package theme — TOML-loadable user themes from ~/.overkill/themes.
//
// Users can ship a theme without recompiling by dropping a .toml file
// in ~/.overkill/themes/. The filename (without extension) becomes the
// theme name shown in the /theme picker. Missing keys inherit from a
// base theme via the `extends = "<name>"` field, so a minimal file can
// just override the accent color and inherit everything else.
//
// Format (all keys optional except `extends`):
//
//	extends = "tokyo-night"   # or "catppuccin" — base for unset keys
//
//	[colors]
//	accent      = "#ff79c6"   # 40 slot names from the Theme interface
//	primary     = "#bd93f9"
//	...
//
// Keys are snake_case versions of the Theme method names:
//
//	Accent()      -> accent
//	BorderFocused() -> border_focused
//	MessageUserBackground() -> message_user_background
package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	toml "github.com/pelletier/go-toml/v2"
)

// FileTheme is a Theme backed by a TOML color map plus a fallback to
// a base theme for any unset keys. Embedding the base by interface
// rather than value means future Theme additions automatically inherit
// without re-shipping every TOML file.
type FileTheme struct {
	name   string
	base   Theme
	colors map[string]lipgloss.Color
}

// Name returns the registry name (the TOML filename minus extension).
func (f *FileTheme) Name() string { return f.name }

// color returns the override color if present, else delegates to base.
// The slot name is the snake_case form of the Theme method name.
func (f *FileTheme) color(slot string, fallback lipgloss.Color) lipgloss.Color {
	if c, ok := f.colors[slot]; ok {
		return c
	}
	return fallback
}

func (f *FileTheme) Background() lipgloss.Color { return f.color("background", f.base.Background()) }
func (f *FileTheme) Foreground() lipgloss.Color { return f.color("foreground", f.base.Foreground()) }
func (f *FileTheme) Text() lipgloss.Color       { return f.color("text", f.base.Text()) }
func (f *FileTheme) TextMuted() lipgloss.Color  { return f.color("text_muted", f.base.TextMuted()) }
func (f *FileTheme) TextBold() lipgloss.Color   { return f.color("text_bold", f.base.TextBold()) }
func (f *FileTheme) Primary() lipgloss.Color    { return f.color("primary", f.base.Primary()) }
func (f *FileTheme) Secondary() lipgloss.Color  { return f.color("secondary", f.base.Secondary()) }
func (f *FileTheme) Accent() lipgloss.Color     { return f.color("accent", f.base.Accent()) }
func (f *FileTheme) Success() lipgloss.Color    { return f.color("success", f.base.Success()) }
func (f *FileTheme) Warning() lipgloss.Color    { return f.color("warning", f.base.Warning()) }
func (f *FileTheme) Error() lipgloss.Color      { return f.color("error", f.base.Error()) }
func (f *FileTheme) Border() lipgloss.Color     { return f.color("border", f.base.Border()) }
func (f *FileTheme) BorderFocused() lipgloss.Color {
	return f.color("border_focused", f.base.BorderFocused())
}
func (f *FileTheme) BorderUnfocused() lipgloss.Color {
	return f.color("border_unfocused", f.base.BorderUnfocused())
}
func (f *FileTheme) PanelBackground() lipgloss.Color {
	return f.color("panel_background", f.base.PanelBackground())
}
func (f *FileTheme) PanelBorder() lipgloss.Color {
	return f.color("panel_border", f.base.PanelBorder())
}
func (f *FileTheme) PanelActive() lipgloss.Color {
	return f.color("panel_active", f.base.PanelActive())
}
func (f *FileTheme) PanelInactive() lipgloss.Color {
	return f.color("panel_inactive", f.base.PanelInactive())
}
func (f *FileTheme) EditorBackground() lipgloss.Color {
	return f.color("editor_background", f.base.EditorBackground())
}
func (f *FileTheme) EditorBorder() lipgloss.Color {
	return f.color("editor_border", f.base.EditorBorder())
}
func (f *FileTheme) EditorCursor() lipgloss.Color {
	return f.color("editor_cursor", f.base.EditorCursor())
}
func (f *FileTheme) EditorPlaceholder() lipgloss.Color {
	return f.color("editor_placeholder", f.base.EditorPlaceholder())
}
func (f *FileTheme) StatusBarBackground() lipgloss.Color {
	return f.color("status_bar_background", f.base.StatusBarBackground())
}
func (f *FileTheme) StatusBarText() lipgloss.Color {
	return f.color("status_bar_text", f.base.StatusBarText())
}
func (f *FileTheme) StatusBarBorder() lipgloss.Color {
	return f.color("status_bar_border", f.base.StatusBarBorder())
}
func (f *FileTheme) DialogBackground() lipgloss.Color {
	return f.color("dialog_background", f.base.DialogBackground())
}
func (f *FileTheme) DialogBorder() lipgloss.Color {
	return f.color("dialog_border", f.base.DialogBorder())
}
func (f *FileTheme) DialogText() lipgloss.Color {
	return f.color("dialog_text", f.base.DialogText())
}
func (f *FileTheme) DialogAccent() lipgloss.Color {
	return f.color("dialog_accent", f.base.DialogAccent())
}
func (f *FileTheme) DialogHighlight() lipgloss.Color {
	return f.color("dialog_highlight", f.base.DialogHighlight())
}
func (f *FileTheme) MessageUserBackground() lipgloss.Color {
	return f.color("message_user_background", f.base.MessageUserBackground())
}
func (f *FileTheme) MessageUserText() lipgloss.Color {
	return f.color("message_user_text", f.base.MessageUserText())
}
func (f *FileTheme) MessageAssistantBackground() lipgloss.Color {
	return f.color("message_assistant_background", f.base.MessageAssistantBackground())
}
func (f *FileTheme) MessageAssistantText() lipgloss.Color {
	return f.color("message_assistant_text", f.base.MessageAssistantText())
}
func (f *FileTheme) MessageToolBackground() lipgloss.Color {
	return f.color("message_tool_background", f.base.MessageToolBackground())
}
func (f *FileTheme) MessageToolText() lipgloss.Color {
	return f.color("message_tool_text", f.base.MessageToolText())
}
func (f *FileTheme) MessageErrorText() lipgloss.Color {
	return f.color("message_error_text", f.base.MessageErrorText())
}
func (f *FileTheme) SidebarBackground() lipgloss.Color {
	return f.color("sidebar_background", f.base.SidebarBackground())
}
func (f *FileTheme) SidebarBorder() lipgloss.Color {
	return f.color("sidebar_border", f.base.SidebarBorder())
}
func (f *FileTheme) SidebarActiveTab() lipgloss.Color {
	return f.color("sidebar_active_tab", f.base.SidebarActiveTab())
}
func (f *FileTheme) SidebarInactiveTab() lipgloss.Color {
	return f.color("sidebar_inactive_tab", f.base.SidebarInactiveTab())
}

// fileThemeDoc mirrors the on-disk TOML shape. We accept colors under
// either a `[colors]` table or at the top level so a minimal file is
// just `extends = "..." \n accent = "#fff"`.
type fileThemeDoc struct {
	Extends string            `toml:"extends"`
	Colors  map[string]string `toml:"colors"`
}

// ParseFileTheme builds a FileTheme from raw TOML bytes. The name is
// caller-provided (typically the filename stem) so this is reusable
// for tests + tooling that doesn't load from disk.
func ParseFileTheme(name string, data []byte) (*FileTheme, error) {
	var doc fileThemeDoc
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("theme %q: parse: %w", name, err)
	}

	// Resolve base. Empty extends → catppuccin (matches the default
	// theme so a missing base feels familiar). Unknown extends is a
	// hard error rather than a silent fallback — otherwise typos in
	// the base name produce confusing partial themes.
	baseName := strings.TrimSpace(doc.Extends)
	if baseName == "" {
		baseName = "catppuccin"
	}
	base := builtinByName(baseName)
	if base == nil {
		return nil, fmt.Errorf("theme %q: extends unknown base %q", name, baseName)
	}

	colors := make(map[string]lipgloss.Color, len(doc.Colors))
	for k, v := range doc.Colors {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		// Lenient: a missing # prefix is auto-added since users
		// frequently paste raw hex.
		if !strings.HasPrefix(v, "#") {
			v = "#" + v
		}
		colors[strings.ToLower(strings.TrimSpace(k))] = lipgloss.Color(v)
	}

	return &FileTheme{
		name:   name,
		base:   base,
		colors: colors,
	}, nil
}

// builtinByName returns one of the compiled-in themes by name. Distinct
// from ByName (which walks the full registry including loaded files)
// because we don't want a user-loaded theme to extend ANOTHER user-
// loaded theme — that creates load-order dependencies and chains can
// silently break.
func builtinByName(name string) Theme {
	switch name {
	case "catppuccin":
		return &Catppuccin{}
	case "tokyo-night":
		return &TokyoNight{}
	}
	return nil
}

var (
	loadedFileThemesMu sync.RWMutex
	loadedFileThemes   = map[string]*FileTheme{}
)

// LoadFromDir scans dir for *.toml files and registers each as a user
// theme keyed by filename stem. Errors loading one file don't abort
// the others — we return the first error encountered so the caller
// can surface it, but the rest of the directory still loads.
//
// Calling LoadFromDir again replaces the cached set (so editing a file
// and rescanning picks up the change without restarting).
func LoadFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Missing directory is fine — user just hasn't created any
		// themes yet. We don't auto-mkdir; that's a setup concern.
		if os.IsNotExist(err) {
			loadedFileThemesMu.Lock()
			loadedFileThemes = map[string]*FileTheme{}
			loadedFileThemesMu.Unlock()
			return nil
		}
		return fmt.Errorf("theme load: read dir: %w", err)
	}

	fresh := map[string]*FileTheme{}
	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".toml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		// Reserved: we never let a file theme shadow a built-in.
		// Otherwise a user's broken file could break the picker for
		// everyone.
		if name == "catppuccin" || name == "tokyo-night" {
			if firstErr == nil {
				firstErr = fmt.Errorf("theme %q: name conflicts with built-in", name)
			}
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("theme %q: read: %w", name, rerr)
			}
			continue
		}
		ft, perr := ParseFileTheme(name, data)
		if perr != nil {
			if firstErr == nil {
				firstErr = perr
			}
			continue
		}
		fresh[name] = ft
	}

	loadedFileThemesMu.Lock()
	loadedFileThemes = fresh
	loadedFileThemesMu.Unlock()
	return firstErr
}

// FileThemes returns a snapshot of the currently-loaded user themes.
// Exposed for the picker dialog + tests; the keys are the registry
// names callers use with SetTheme/ByName.
func FileThemes() map[string]Theme {
	loadedFileThemesMu.RLock()
	defer loadedFileThemesMu.RUnlock()
	out := make(map[string]Theme, len(loadedFileThemes))
	for k, v := range loadedFileThemes {
		out[k] = v
	}
	return out
}
