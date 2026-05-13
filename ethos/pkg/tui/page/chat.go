package page

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
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

	// pendingQueue holds user messages typed while a previous stream is
	// still running. Drained FIFO when the in-flight stream finishes
	// naturally. The user can interrupt via double-Esc — on interrupt the
	// LAST queued message is popped back into the editor for editing,
	// earlier queued entries are discarded (their intent is stale).
	//
	// Semantics chosen because: (1) the latest message is the most
	// current expression of user intent — that's the one they want to
	// edit; (2) the user explicitly said "msg thats in queue comes back
	// to the tui field" (singular).
	pendingQueue []string
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

// QueueDepth returns the number of user messages waiting behind the
// current in-flight stream. Drains FIFO when the stream finishes.
func (c *ChatPage) QueueDepth() int {
	return len(c.pendingQueue)
}

// InterruptStream cancels the in-flight stream AND drains the pending
// queue. Returns the LAST queued text (the user's most-recent intent) so
// the TUI can drop it back into the editor for editing. Earlier queued
// entries are discarded — they were superseded by the user's later
// thinking. Returns ("", false) when nothing was running.
//
// Spec: user hits Esc twice → stream stops, latest queued message comes
// back to the editor field, user can edit and resend.
func (c *ChatPage) InterruptStream() (string, bool) {
	if !c.busy && len(c.pendingQueue) == 0 {
		return "", false
	}
	c.CancelStream()
	var restore string
	if n := len(c.pendingQueue); n > 0 {
		restore = c.pendingQueue[n-1]
		c.pendingQueue = nil
	}
	return restore, true
}

// extractShellMetadata reads the tool name + output JSON off a
// StreamEvent and, when the tool is shell-family, returns the formatted
// per-command metadata line. Returns "" for non-shell tools or unparseable
// payloads — silent skip is the right behaviour because the agent loop
// keeps moving and metadata is decoration.
func extractShellMetadata(ev agent.StreamEvent) string {
	if ev.ToolCall == nil {
		return ""
	}
	switch ev.ToolCall.Name {
	case "shell", "pty_shell", "execute_command":
	default:
		return ""
	}
	raw, ok := ev.Metadata["output"].(string)
	if !ok || raw == "" {
		return ""
	}
	var out tools.ShellOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return ""
	}
	return formatShellMetadataLine(out)
}

// directShellResultMsg carries the output of a $hell direct-exec back into
// the Update loop so we can append the tool message in the main reducer
// instead of mutating state from a goroutine.
type directShellResultMsg struct {
	Command string
	Output  string
	Err     error
}

// startDirectShell runs the $hell-prefixed command literally, bypassing
// the agent. The user-typed line is appended as the user message; the
// shell output (including metadata) lands under it as a tool message.
// Goes through internal/tools.ShellTool so we inherit the §6 marker,
// timeout policy, ANSI stripping, and the new exit/ms/cwd capture.
func (c *ChatPage) startDirectShell(line string) (ChatPage, tea.Cmd) {
	command := strings.TrimSpace(strings.TrimPrefix(line, "$"))
	if command == "" {
		return *c, nil
	}
	c.messages.Append(chat.NewMessage("user", line))

	cmd := func() tea.Msg {
		shell := tools.NewShellTool()
		in, _ := json.Marshal(tools.ShellInput{Command: command})
		raw, err := shell.Execute(context.Background(), in)
		if err != nil {
			return directShellResultMsg{Command: command, Err: err}
		}
		var out tools.ShellOutput
		if uerr := json.Unmarshal(raw, &out); uerr != nil {
			return directShellResultMsg{Command: command, Err: uerr}
		}
		return directShellResultMsg{
			Command: command,
			Output:  formatDirectShellOutput(out),
		}
	}
	return *c, cmd
}

// formatDirectShellOutput renders the ShellTool result into a single
// string we can drop into the messages list. Combines the §8 metadata
// line with the actual stdout/stderr beneath it.
func formatDirectShellOutput(out tools.ShellOutput) string {
	var b strings.Builder
	b.WriteString(formatShellMetadataLine(out))
	b.WriteString("\n")
	if out.Stdout != "" {
		b.WriteString(out.Stdout)
	}
	if out.Stderr != "" {
		b.WriteString(out.Stderr)
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatShellMetadataLine produces just the inline metadata block —
// exit code, elapsed time, cwd — for Phase 1.5 #8. Exported in
// lowercase form (package-internal) because it's reused by both the
// $hell direct-exec path and the agent-driven pump.
func formatShellMetadataLine(out tools.ShellOutput) string {
	var b strings.Builder
	mark := "✓"
	if out.ExitCode != 0 {
		mark = "✗"
	}
	if out.TimedOut {
		mark = "⏱"
	}
	fmt.Fprintf(&b, "%s exit %d", mark, out.ExitCode)
	if out.ElapsedMs > 0 {
		if out.ElapsedMs >= 1000 {
			fmt.Fprintf(&b, " · %.1fs", float64(out.ElapsedMs)/1000)
		} else {
			fmt.Fprintf(&b, " · %dms", out.ElapsedMs)
		}
	}
	if out.Cwd != "" {
		fmt.Fprintf(&b, " · %s", out.Cwd)
	}
	return b.String()
}

// startStream constructs the tea.Cmd that begins streaming `input` to
// the agent. Extracted from the SendMsg branch so it can also be
// invoked when the queue drains after a prior stream finishes.
func (c *ChatPage) startStream(input string) tea.Cmd {
	c.busy = true
	c.streaming = true
	c.streamBuf = ""
	c.messages.Append(chat.NewMessage("user", input))
	// Placeholder assistant message — mutated in-place by chunks.
	c.messages.Append(chat.NewMessage("assistant", ""))
	c.pulse.SetWidth(c.width)
	pulseCmd := c.pulse.Start()

	agt := c.agent
	// Cancellable ctx so CancelStream / InterruptStream can stop the
	// in-flight provider call between chunks.
	ctx, cancel := context.WithCancel(context.Background())
	c.streamCancel = cancel
	streamStart := func() tea.Msg {
		ch, err := agt.Stream(ctx, input)
		if err != nil {
			return tuitypes.AgentResponseMsg{Err: err, Done: true}
		}
		return streamReadyMsg{ch: ch}
	}
	return tea.Batch(pulseCmd, streamStart)
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
			// Phase 1.5 #8: surface per-command metadata for
			// shell-family tools so the user sees exit/ms/cwd
			// inline under each shell call the agent made. Skips
			// silently for other tools and for malformed payloads.
			if line := extractShellMetadata(ev); line != "" {
				return tuitypes.AgentStreamMsg{MetadataLine: line}
			}
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
		if msg.Text == "" {
			return c, nil
		}
		// Phase 1.5 — $hell shortcut: lines starting with `$` bypass the
		// agent entirely and execute literally. Zero tokens, zero
		// ambiguity. Output renders as a tool message under the user's
		// typed line. Works even when the agent is busy because there's
		// no provider call to compete with.
		if strings.HasPrefix(msg.Text, "$") {
			return c.startDirectShell(msg.Text)
		}
		if c.agent == nil {
			return c, nil
		}
		// While an existing stream runs we queue rather than refusing or
		// starting in parallel. The queue drains FIFO on natural Done.
		// User can interrupt via double-Esc (see InterruptStream).
		if c.busy {
			c.pendingQueue = append(c.pendingQueue, msg.Text)
			return c, nil
		}
		return c, c.startStream(msg.Text)

	case bgpulse.TickMsg:
		var pcmd tea.Cmd
		c.pulse, pcmd = c.pulse.Update(msg)
		return c, pcmd

	case streamReadyMsg:
		return c, c.pump(msg.ch)

	case directShellResultMsg:
		// $hell completion: append the result as a tool message.
		// Failure on the Execute call itself (vs non-zero exit, which
		// is a successful run of a failing script) renders as error.
		if msg.Err != nil {
			c.messages.Append(chat.NewMessage("error", msg.Err.Error()))
		} else {
			c.messages.Append(chat.NewMessage("tool", msg.Output))
		}
		return c, nil

	case tuitypes.AgentStreamMsg:
		// Phase 1.5 #8: render the per-command metadata block under
		// shell tool calls. Comes through as a standalone event from
		// pump — append, then keep draining the stream.
		if msg.MetadataLine != "" {
			c.messages.Append(chat.NewMessage("tool", msg.MetadataLine))
			if c.streamCh != nil {
				return c, c.pump(c.streamCh)
			}
			return c, nil
		}
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
			// Drain queue head — FIFO. The user's earliest still-pending
			// intent runs next. If they wanted to skip it, they'd have
			// hit Esc twice (InterruptStream discards the queue).
			if len(c.pendingQueue) > 0 {
				next := c.pendingQueue[0]
				c.pendingQueue = c.pendingQueue[1:]
				return c, c.startStream(next)
			}
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
