package chat

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/styles"
	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
	"github.com/Sahaj-Tech-ltd/overkill/internal/highlight"
)

// highlightFencedBlocks finds ```lang ... ``` blocks and replaces their body
// with ANSI-highlighted output. Used during streaming where Glamour isn't
// run on every chunk for performance.
func highlightFencedBlocks(content string) string {
	lines := strings.Split(content, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			lang := highlight.LangFromFence(line)
			out = append(out, line)
			i++
			start := i
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				i++
			}
			body := strings.Join(lines[start:i], "\n")
			if body != "" {
				body = highlight.Highlight(body, lang, highlight.DefaultTheme)
			}
			out = append(out, body)
			if i < len(lines) {
				out = append(out, lines[i])
				i++
			}
			continue
		}
		out = append(out, line)
		i++
	}
	return strings.Join(out, "\n")
}

var (
	renderCache = struct {
		sync.RWMutex
		entries map[string]string
		order   []string
	}{
		entries: make(map[string]string),
	}
)

const (
	maxCacheEntries  = 100
	toolPreviewLines = 3
)

func ClearCache() {
	renderCache.Lock()
	defer renderCache.Unlock()
	renderCache.entries = make(map[string]string)
	renderCache.order = nil
}

type Message struct {
	ID        string
	Role      string
	Content   string
	ToolName  string
	Timestamp time.Time
	Width     int
	// Streaming is true while content is still being appended. While true,
	// assistant messages render as plain text — Glamour markdown re-renders
	// per chunk are too expensive over SSH and cause perceived lag.
	Streaming bool
}

func NewMessage(role, content string) Message {
	return Message{
		ID:        uuid.New().String(),
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	}
}

func (m Message) CacheKey() string {
	return fmt.Sprintf("%s-%d", m.ID, m.Width)
}

// View renders the message in opencode-style: minimal labels, no boxes.
func (m Message) View(width int) string {
	if width <= 0 {
		width = 80
	}
	// Include content length + streaming flag so streaming updates to the
	// same message ID don't return a stale earlier render and so the final
	// non-streaming render is cached separately from the in-flight one.
	key := fmt.Sprintf("%s-%d-%d-%t", m.ID, width, len(m.Content), m.Streaming)

	renderCache.RLock()
	if cached, ok := renderCache.entries[key]; ok {
		renderCache.RUnlock()
		return cached
	}
	renderCache.RUnlock()

	t := theme.CurrentTheme()
	var rendered string

	switch m.Role {
	case "user":
		rendered = renderUser(t, m.Content, width)
	case "assistant":
		rendered = renderAssistant(t, m.Content, width, m.Streaming)
	case "tool":
		rendered = renderTool(t, m.ToolName, m.Content, width)
	case "error":
		rendered = renderError(t, m.Content, width)
	default:
		rendered = renderGeneric(t, m.Role, m.Content, width)
	}

	cachePut(key, rendered)
	// Copy-chip footer is appended AFTER the cache lookup so the
	// expensive markdown render stays cached but the footer reflects
	// the latest hover state without invalidating the cache on every
	// mouse-motion event. Streaming and non-assistant messages get no
	// footer (block boundaries aren't stable mid-stream).
	return appendCopyFooter(rendered, m, width)
}

// renderedWithFooter wraps the post-cache step of appending the copy
// footer. Pulled out so the View method stays readable and so the
// scrollback cache only stores the heavy body, not the hover-dependent
// footer.
func appendCopyFooter(rendered string, m Message, width int) string {
	if m.Role != "assistant" || m.Streaming {
		return rendered
	}
	blocks := ExtractCodeBlocks(m.Content)
	if len(blocks) == 0 {
		return rendered
	}
	t := theme.CurrentTheme()
	footer, _ := buildCopyFooter(t, blocks, HoveredID())
	return rendered + "\n" + footer
}

func renderUser(t theme.Theme, content string, width int) string {
	label := lipgloss.NewStyle().
		Foreground(t.Primary()).
		Bold(true).
		Render("you")
	body := lipgloss.NewStyle().
		Foreground(t.Text()).
		Width(width).
		Align(lipgloss.Right).
		Render(content)
	labelLine := lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Right).
		Render(label)
	return labelLine + "\n" + body
}

func renderAssistant(t theme.Theme, content string, width int, streaming bool) string {
	label := lipgloss.NewStyle().
		Foreground(t.Accent()).
		Bold(true).
		Render("overkill")
	var body string
	if streaming {
		// Plain text while streaming — markdown parsing on every token
		// chunk burns CPU and floods SSH with rewrites. We still apply a
		// lightweight syntax-highlight pass over any complete fenced blocks
		// so even mid-stream code reads cleanly.
		highlighted := highlightFencedBlocks(content)
		body = lipgloss.NewStyle().
			Foreground(t.Text()).
			Width(width).
			Render(highlighted)
	} else {
		body = styles.RenderMarkdown(content, width)
		body = strings.TrimRight(body, "\n")
	}
	return label + "\n" + body
}

func renderTool(t theme.Theme, name, content string, width int) string {
	header := lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Render("→ " + name)

	if strings.TrimSpace(content) == "" {
		return header
	}

	indent := "  "
	lines := strings.Split(content, "\n")
	hidden := 0
	if len(lines) > toolPreviewLines {
		hidden = len(lines) - toolPreviewLines
		lines = lines[:toolPreviewLines]
	}

	bodyStyle := lipgloss.NewStyle().Foreground(t.TextMuted())
	var rendered []string
	for _, line := range lines {
		rendered = append(rendered, bodyStyle.Render(indent+line))
	}
	if hidden > 0 {
		rendered = append(rendered, bodyStyle.Italic(true).Render(
			fmt.Sprintf("%s(%d more lines)", indent, hidden),
		))
	}
	return header + "\n" + strings.Join(rendered, "\n")
}

func renderError(t theme.Theme, content string, width int) string {
	style := lipgloss.NewStyle().Foreground(t.Error()).Bold(true)
	return style.Render("✗ " + content)
}

func renderGeneric(t theme.Theme, role, content string, width int) string {
	label := lipgloss.NewStyle().Foreground(t.TextMuted()).Render(role)
	if content == "" {
		return label
	}
	return label + "\n" + content
}

func cachePut(key, value string) {
	renderCache.Lock()
	defer renderCache.Unlock()

	if _, exists := renderCache.entries[key]; exists {
		return
	}

	if len(renderCache.entries) >= maxCacheEntries {
		oldest := renderCache.order[0]
		delete(renderCache.entries, oldest)
		renderCache.order = renderCache.order[1:]
	}

	renderCache.entries[key] = value
	renderCache.order = append(renderCache.order, key)
}
