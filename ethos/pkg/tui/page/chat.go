package page

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/bgpulse"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/chat"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/logo"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"

	tuitypes "github.com/Sahaj-Tech-ltd/overkill/pkg/tui/types"
)

const promptMaxWidth = 75

var welcomeSuggestions = []string{
	"explain this codebase",
	"find tests for the gateway",
	"refactor the agent loop",
}

type ChatPage struct {
	agent       *agent.Agent
	messages    chat.MessageListModel
	editor      chat.EditorModel
	width       int
	height      int
	busy        bool
	streaming   bool
	streamBuf   string
	streamCh    <-chan agent.StreamEvent
	pulse       bgpulse.Model
	shimmerLogo logo.LogoModel
	// streamCancel cancels the in-flight LLM stream context. Set when a stream
	// starts, cleared when it completes or is cancelled. Lets the parent TUI
	// abort an in-flight request on Ctrl+C / Esc so tokens stop burning.
	streamCancel context.CancelFunc
}

// CancelStream aborts the in-flight LLM stream (if any) by cancelling its
// context. Safe to call when no stream is active. Returns true when a stream
// was actually cancelled. The provider's Stream goroutine observes ctx.Done()
// and closes its event channel; pump() then reports Done back into the TUI.
func (c *ChatPage) CancelStream() bool {
	if c.streamCancel == nil {
		return false
	}
	c.streamCancel()
	c.streamCancel = nil
	return true
}

// PulseFrame returns the bgpulse frame for tests / external rendering.
func (c ChatPage) PulseFrame() int { return c.pulse.Frame() }

// PulseActive reports whether the background pulse is currently animating.
func (c ChatPage) PulseActive() bool { return c.pulse.Active() }

// streamReadyMsg arrives after Stream() returns, carrying the channel we'll
// drain via pump().
type streamReadyMsg struct {
	ch <-chan agent.StreamEvent
}

// pump returns a tea.Cmd that reads one event from ch and translates it into
// AgentStreamMsg. Re-armed by the parent Update loop while streaming.
func (c *ChatPage) pump(ch <-chan agent.StreamEvent) tea.Cmd {
	c.streamCh = ch
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return tuitypes.AgentStreamMsg{Done: true}
		}
		switch ev.Type {
		case agent.EventToken:
			return tuitypes.AgentStreamMsg{Chunk: ev.Content}
		case agent.EventToolStart:
			name := ""
			if ev.ToolCall != nil {
				name = ev.ToolCall.Name
			}
			return tuitypes.AgentStreamMsg{ToolName: name}
		case agent.EventToolOutput:
			return tuitypes.AgentStreamMsg{Chunk: ""}
		case agent.EventError:
			return tuitypes.AgentStreamMsg{Err: ev.Error, Done: true}
		case agent.EventDone:
			return tuitypes.AgentStreamMsg{Done: true}
		}
		return tuitypes.AgentStreamMsg{}
	}
}

// IsBusy reports whether a stream is in flight; the status bar uses this.
func (c ChatPage) IsBusy() bool { return c.busy }

// AppendRaw is a low-level helper used by parents (e.g. fork) that need to
// rehydrate the message list without re-running the agent.
func (c *ChatPage) AppendRaw(role, content string) {
	c.messages.Append(chat.NewMessage(role, content))
}

func NewChatPage(a *agent.Agent) ChatPage {
	cp := ChatPage{
		agent:       a,
		messages:    chat.NewMessageList(),
		editor:      chat.NewEditor(),
		shimmerLogo: logo.NewLogoModel(),
	}
	cp.editor.Focus()
	return cp
}

// SetAgent swaps the underlying agent — used after in-TUI reconfigure.
func (c *ChatPage) SetAgent(a *agent.Agent) {
	c.agent = a
}

// HasMessages reports whether any chat history exists. Used by the parent to
// decide whether to render the welcome screen.
func (c ChatPage) HasMessages() bool {
	return c.messages.Len() > 0
}

// ClearHistory wipes message list and underlying agent history.
func (c *ChatPage) ClearHistory() {
	c.messages = chat.NewMessageList()
	c.messages.SetSize(c.width, max(1, c.height-3))
	if c.agent != nil {
		c.agent.ClearHistory()
	}
}

// Editor exposes the inner editor for parent routing decisions.
func (c *ChatPage) Editor() *chat.EditorModel {
	return &c.editor
}

func (c ChatPage) Init() tea.Cmd {
	return tea.Batch(c.messages.Init(), c.editor.Focus())
}

// startShimmerIfWelcome arms the logo shimmer when we're in welcome state
// (no messages, agent absent or no history). Returns nil otherwise so we
// don't double-tick when chat is active.
func (c *ChatPage) startShimmerIfWelcome() tea.Cmd {
	if c.HasMessages() {
		return nil
	}
	c.shimmerLogo.SetWidth(c.width)
	return c.shimmerLogo.Start()
}

func (c ChatPage) Update(msg tea.Msg) (ChatPage, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		msgHeight := msg.Height - 5
		if msgHeight < 1 {
			msgHeight = 1
		}
		c.messages.SetSize(msg.Width, msgHeight)
		c.editor.SetSize(min(msg.Width, promptMaxWidth+4), 3)
		c.shimmerLogo.SetWidth(msg.Width)
		// Kick off the shimmer the first time we know the width — only if
		// we're showing the welcome screen (no messages yet).
		if !c.HasMessages() {
			return c, c.shimmerLogo.Start()
		}
		return c, nil

	case logo.ShimmerTickMsg:
		// Stop ticking once chat has messages so we don't burn frames behind
		// the conversation view.
		if c.HasMessages() {
			c.shimmerLogo.Stop()
			return c, nil
		}
		var lcmd tea.Cmd
		c.shimmerLogo, lcmd = c.shimmerLogo.Update(msg)
		return c, lcmd

	case tuitypes.SendMsg:
		if c.busy || c.agent == nil || msg.Text == "" {
			return c, nil
		}
		c.busy = true
		c.messages.Append(chat.NewMessage("user", msg.Text))
		// Append a placeholder assistant message we'll mutate in-place.
		c.messages.Append(chat.NewMessage("assistant", ""))
		c.streaming = true
		c.streamBuf = ""
		c.pulse.SetWidth(c.width)
		pulseCmd := c.pulse.Start()

		input := msg.Text
		agt := c.agent
		// Cancellable context: parent TUI calls CancelStream() on Ctrl+C/Esc
		// to stop in-flight tokens. The agent's Stream loop selects on
		// ctx.Done() between chunks, so cancellation propagates promptly.
		ctx, cancel := context.WithCancel(context.Background())
		c.streamCancel = cancel
		streamStart := func() tea.Msg {
			ch, err := agt.Stream(ctx, input)
			if err != nil {
				return tuitypes.AgentResponseMsg{Err: err, Done: true}
			}
			return streamReadyMsg{ch: ch}
		}
		return c, tea.Batch(pulseCmd, streamStart)

	case bgpulse.TickMsg:
		var pcmd tea.Cmd
		c.pulse, pcmd = c.pulse.Update(msg)
		return c, pcmd

	case streamReadyMsg:
		return c, c.pump(msg.ch)

	case tuitypes.AgentStreamMsg:
		if msg.Err != nil {
			c.busy = false
			c.streaming = false
			c.pulse.Stop()
			if c.streamCancel != nil {
				c.streamCancel()
				c.streamCancel = nil
			}
			c.messages.Append(chat.NewMessage("error", msg.Err.Error()))
			return c, nil
		}
		if msg.Done {
			c.busy = false
			c.streaming = false
			c.pulse.Stop()
			if c.streamCancel != nil {
				c.streamCancel()
				c.streamCancel = nil
			}
			c.messages.FinalizeLastAssistant()
			return c, nil
		}
		if msg.Chunk != "" {
			c.streamBuf += msg.Chunk
			c.messages.UpdateLastAssistant(c.streamBuf)
		}
		// Continue draining
		if c.streamCh != nil {
			return c, c.pump(c.streamCh)
		}
		return c, nil

	case tuitypes.AgentResponseMsg:
		c.busy = false
		if msg.Err != nil {
			c.messages.Append(chat.NewMessage("error", msg.Err.Error()))
		} else if msg.Content != "" {
			c.messages.Append(chat.NewMessage("assistant", msg.Content))
		}
		return c, nil

	case tea.KeyMsg:
		c.messages, cmd = c.messages.Update(msg)
		var editorCmd tea.Cmd
		c.editor, editorCmd = c.editor.Update(msg)
		return c, tea.Batch(cmd, editorCmd)
	}

	c.editor, cmd = c.editor.Update(msg)
	return c, cmd
}

func (c ChatPage) View() string {
	if c.width <= 0 {
		return "Loading..."
	}

	if !c.HasMessages() {
		return c.viewWelcome()
	}
	return c.viewSession()
}

func (c ChatPage) viewSession() string {
	t := theme.CurrentTheme()
	sepStyle := lipgloss.NewStyle().Foreground(t.Border())
	separator := sepStyle.Render(strings.Repeat("─", max(c.width, 1)))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		c.messages.View(),
		separator,
		c.editor.View(),
	)
}

func (c ChatPage) viewWelcome() string {
	t := theme.CurrentTheme()

	// Use the animated logo when active; LogoModel.View() falls back to the
	// static logo when animations are gated off (small terminals, anim=false).
	logoBlock := c.shimmerLogo.View()
	if logoBlock == "" {
		logoBlock = logo.Render(t)
	}

	subtitleStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Italic(true)
	subtitle := subtitleStyle.Render(logo.Subtitle)

	bubbleStyle := lipgloss.NewStyle().
		Foreground(t.Text()).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Border()).
		Padding(0, 2)

	var bubbles []string
	for _, s := range welcomeSuggestions {
		bubbles = append(bubbles, bubbleStyle.Render(s))
	}
	suggestions := lipgloss.JoinVertical(lipgloss.Left, bubbles...)

	editorWidth := min(c.width-2, promptMaxWidth)
	if editorWidth < 20 {
		editorWidth = 20
	}
	editor := lipgloss.NewStyle().Width(editorWidth).Render(c.editor.View())

	body := lipgloss.JoinVertical(
		lipgloss.Center,
		logoBlock,
		"",
		subtitle,
		"",
		"",
		editor,
		"",
		suggestions,
	)

	return lipgloss.Place(
		c.width, max(c.height, 10),
		lipgloss.Center, lipgloss.Center,
		body,
	)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
