package chat

import (
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/styles"
)

var (
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#89b4fa")).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a6e3a1")).
			Bold(true)

	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6c7086"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f38ba8")).
			Bold(true)

	contentStyle = lipgloss.NewStyle()

	renderCache = struct {
		sync.RWMutex
		entries map[string]string
		order   []string
	}{
		entries: make(map[string]string),
	}
)

const maxCacheEntries = 100

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

func (m Message) View(width int) string {
	if width <= 0 {
		width = 80
	}

	key := fmt.Sprintf("%s-%d", m.ID, width)

	renderCache.RLock()
	if cached, ok := renderCache.entries[key]; ok {
		renderCache.RUnlock()
		return cached
	}
	renderCache.RUnlock()

	var label, body string

	switch m.Role {
	case "user":
		label = userStyle.Render("[You]")
		body = m.Content
	case "assistant":
		label = assistantStyle.Render("[Ethos]")
		body = styles.RenderMarkdown(m.Content, width)
	case "tool":
		toolLabel := fmt.Sprintf("[Tool: %s]", m.ToolName)
		label = toolStyle.Render(toolLabel)
		body = m.Content
	case "error":
		label = errorStyle.Render("[Error]")
		body = m.Content
	default:
		label = fmt.Sprintf("[%s]", m.Role)
		body = m.Content
	}

	if body == "" {
		placeholder := fmt.Sprintf("(%s)", m.Role)
		result := fmt.Sprintf("%s %s", label, contentStyle.Render(placeholder))
		cachePut(key, result)
		return result
	}

	plainLabelLen := ansi.StringWidth(label) + 1
	contentWidth := width - plainLabelLen
	if contentWidth < 10 {
		contentWidth = 10
	}

	rendered := contentStyle.Render(ansi.Truncate(body, contentWidth, "…"))
	result := fmt.Sprintf("%s %s", label, rendered)

	cachePut(key, result)
	return result
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
