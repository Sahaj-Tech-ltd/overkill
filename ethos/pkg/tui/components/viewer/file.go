// Package viewer provides side-pane viewers (file, image, etc.) used by the
// TUI's split-view mode. Currently only a scrollable file viewer with
// chroma-based syntax highlighting.
package viewer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/internal/highlight"
)

// FileView is a scrollable, syntax-highlighted file viewer.
type FileView struct {
	path    string
	lines   []string
	width   int
	height  int
	scroll  int
	focused bool
	err     error
}

// NewFileView opens path and returns a viewer. If path is empty, an empty
// viewer is returned (use Open later).
func NewFileView(path string) *FileView {
	v := &FileView{}
	if path != "" {
		_ = v.Open(path)
	}
	return v
}

// Open loads (or reloads) the given file into the viewer. Highlights based
// on the file extension.
func (v *FileView) Open(path string) error {
	v.path = path
	v.scroll = 0
	data, err := os.ReadFile(path)
	if err != nil {
		v.err = err
		v.lines = nil
		return err
	}
	v.err = nil
	lang := langFromExt(filepath.Ext(path))
	rendered := highlight.Highlight(string(data), lang, highlight.DefaultTheme)
	v.lines = strings.Split(rendered, "\n")
	return nil
}

// SetSize updates the viewport dimensions.
func (v *FileView) SetSize(w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	v.width = w
	v.height = h
	v.clampScroll()
}

// SetFocus marks the viewer focused (for key handling).
func (v *FileView) SetFocus(b bool) { v.focused = b }

// Focused reports focus state.
func (v *FileView) Focused() bool { return v.focused }

// Path returns the currently loaded path.
func (v *FileView) Path() string { return v.path }

// ScrollDown advances the viewport.
func (v *FileView) ScrollDown(n int) {
	v.scroll += n
	v.clampScroll()
}

// ScrollUp moves the viewport back.
func (v *FileView) ScrollUp(n int) {
	v.scroll -= n
	if v.scroll < 0 {
		v.scroll = 0
	}
}

// PageDown advances by viewport height.
func (v *FileView) PageDown() { v.ScrollDown(v.bodyHeight()) }

// PageUp moves back by viewport height.
func (v *FileView) PageUp() { v.ScrollUp(v.bodyHeight()) }

func (v *FileView) bodyHeight() int {
	// 2 lines reserved for header + footer
	h := v.height - 2
	if h < 1 {
		return 1
	}
	return h
}

func (v *FileView) clampScroll() {
	maxScroll := len(v.lines) - v.bodyHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if v.scroll > maxScroll {
		v.scroll = maxScroll
	}
	if v.scroll < 0 {
		v.scroll = 0
	}
}

// View renders the file viewer (header, body, footer). Falls back to a plain
// "no file" message when nothing is loaded.
func (v *FileView) View() string {
	if v.path == "" {
		return lipgloss.NewStyle().Faint(true).Render("(no file open)")
	}
	header := fmt.Sprintf(" %s", v.path)
	if v.focused {
		header = lipgloss.NewStyle().Bold(true).Render("▸ " + v.path)
	}

	var body strings.Builder
	body.WriteString(header + "\n")
	if v.err != nil {
		body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("error: "+v.err.Error()) + "\n")
		return body.String()
	}

	end := v.scroll + v.bodyHeight()
	if end > len(v.lines) {
		end = len(v.lines)
	}
	for i := v.scroll; i < end; i++ {
		body.WriteString(truncate(v.lines[i], v.width) + "\n")
	}

	footer := fmt.Sprintf(" %d/%d  j/k pgup/pgdn", min(v.scroll+v.bodyHeight(), len(v.lines)), len(v.lines))
	body.WriteString(lipgloss.NewStyle().Faint(true).Render(footer))
	return body.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncate(s string, w int) string {
	if w <= 0 {
		return s
	}
	// Simple display-width truncation that respects basic rune width. ANSI
	// escapes from chroma may be wider than visible; we trade strict
	// truncation for simplicity (chroma escapes still render OK over-length).
	if len(s) <= w {
		return s
	}
	return s[:w]
}

// langFromExt picks a chroma lexer name from a filename extension.
func langFromExt(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "go":
		return "go"
	case "py":
		return "python"
	case "js", "mjs":
		return "javascript"
	case "ts":
		return "typescript"
	case "tsx":
		return "tsx"
	case "rs":
		return "rust"
	case "rb":
		return "ruby"
	case "sh", "bash":
		return "bash"
	case "yaml", "yml":
		return "yaml"
	case "toml":
		return "toml"
	case "json":
		return "json"
	case "md":
		return "markdown"
	case "html":
		return "html"
	case "css":
		return "css"
	case "c":
		return "c"
	case "cpp", "cc", "cxx", "h", "hpp":
		return "cpp"
	case "java":
		return "java"
	case "kt":
		return "kotlin"
	case "swift":
		return "swift"
	default:
		return ""
	}
}
