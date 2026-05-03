package gateway

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
)

// Dispatcher is the shared "message arrives → reply goes out" engine
// every Channel hands its inbound messages to. It owns:
//   - session resolution via SessionRouter (incl. follow mode)
//   - the small set of slash commands a remote user needs to manage
//     their session from a phone (/sessions, /attach, /follow, /new,
//     /end, /help)
//   - per-session serialization so two phones blasting the same chat
//     don't interleave turns inside the agent loop
//
// The Channel keeps owning its Reply transport; Dispatcher just calls
// PostInitial / Update / Final / Error against it.
type Dispatcher struct {
	Agent       AgentSender
	Router      *SessionRouter
	Logger      *log.Logger
	UpdateEvery time.Duration // batch reply edits; default 750ms

	mu    sync.Mutex
	locks map[string]*sync.Mutex // session id → serializer
}

// NewDispatcher returns a Dispatcher with sensible defaults filled in.
func NewDispatcher(ag AgentSender, r *SessionRouter) *Dispatcher {
	return &Dispatcher{
		Agent:       ag,
		Router:      r,
		Logger:      log.New(io.Discard, "", 0),
		UpdateEvery: 750 * time.Millisecond,
		locks:       map[string]*sync.Mutex{},
	}
}

// Handle is called by a Channel for each Inbound it receives. Reply is
// the channel's own transport. Errors are logged and surfaced via
// reply.Error rather than returned, because gateway loops shouldn't die
// over one bad message.
func (d *Dispatcher) Handle(ctx context.Context, in Inbound, reply Reply) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "/") {
		d.handleCommand(ctx, in, reply, text)
		return
	}

	sid := d.resolveSession(in)
	d.serialize(sid, func() { d.runTurn(ctx, in, reply, sid, text) })
}

// resolveSession picks the session id this turn writes into. If the
// chat has no binding yet we mint one and persist it so future turns
// land in the same place.
func (d *Dispatcher) resolveSession(in Inbound) string {
	live := ""
	if d.Agent != nil {
		live = d.Agent.SessionID()
	}
	if d.Router != nil {
		if sid, _ := d.Router.Resolve(in.Channel, in.ChatKey, in.Thread, live); sid != "" {
			return sid
		}
	}
	sid := NewSessionID(in.Channel)
	if d.Router != nil {
		if err := d.Router.Bind(in.Channel, in.ChatKey, in.Thread, sid); err != nil {
			d.Logger.Printf("dispatch: bind: %v", err)
		}
	}
	return sid
}

// serialize ensures one in-flight turn per session id at a time. The
// agent's internal state (history, tool calls) isn't safe to scribble
// into concurrently, and Slack already learned this the hard way.
func (d *Dispatcher) serialize(sessionID string, fn func()) {
	d.mu.Lock()
	mu, ok := d.locks[sessionID]
	if !ok {
		mu = &sync.Mutex{}
		d.locks[sessionID] = mu
	}
	d.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	fn()
}

// runTurn is the meat: post a placeholder, swap session, stream the
// agent reply into the placeholder, finalize.
func (d *Dispatcher) runTurn(ctx context.Context, in Inbound, reply Reply, sessionID, text string) {
	postCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	handle, err := reply.PostInitial(postCtx, in, "⏳ thinking…")
	cancel()
	if err != nil {
		d.Logger.Printf("dispatch: post initial: %v", err)
		return
	}

	if d.Agent == nil {
		_ = reply.Error(ctx, handle, fmt.Errorf("no agent configured"))
		return
	}
	d.Agent.SetSessionID(sessionID)
	stream, err := d.Agent.Stream(ctx, text)
	if err != nil {
		_ = reply.Error(ctx, handle, err)
		return
	}
	d.streamInto(ctx, reply, handle, stream)
	if d.Router != nil {
		d.Router.Touch(in.Channel, in.ChatKey, in.Thread)
	}
}

// streamInto drains the agent stream, batching token deltas into
// reply.Update calls at most once per UpdateEvery, then calls Final.
func (d *Dispatcher) streamInto(ctx context.Context, reply Reply, handle string, stream <-chan agent.StreamEvent) {
	var (
		mu        sync.Mutex
		buf       strings.Builder
		lastSent  string
		dirty     bool
		streamErr error
	)
	flush := func(force bool) {
		mu.Lock()
		current := buf.String()
		need := dirty && current != lastSent
		mu.Unlock()
		if !need && !force {
			return
		}
		fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := reply.Update(fctx, handle, current)
		cancel()
		if err != nil {
			d.Logger.Printf("dispatch: update: %v", err)
			return
		}
		mu.Lock()
		lastSent = current
		dirty = false
		mu.Unlock()
	}

	tick := time.NewTicker(d.UpdateEvery)
	defer tick.Stop()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for ev := range stream {
			switch ev.Type {
			case agent.EventToken:
				mu.Lock()
				buf.WriteString(ev.Content)
				dirty = true
				mu.Unlock()
			case agent.EventToolStart:
				if ev.ToolCall != nil {
					mu.Lock()
					if buf.Len() > 0 {
						buf.WriteString("\n\n")
					}
					fmt.Fprintf(&buf, "▸ %s", ev.ToolCall.Name)
					dirty = true
					mu.Unlock()
				}
			case agent.EventError:
				if ev.Error != nil {
					streamErr = ev.Error
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			flush(false)
		case <-done:
			flush(true)
			final := buf.String()
			if streamErr != nil {
				_ = reply.Error(ctx, handle, streamErr)
				return
			}
			if final == "" {
				final = "(no output)"
			}
			fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = reply.Final(fctx, handle, final)
			cancel()
			return
		}
	}
}

// handleCommand implements the small slash-command surface remote
// users get. Anything not recognized falls through to the agent so
// people can still ask it to "do /something".
func (d *Dispatcher) handleCommand(ctx context.Context, in Inbound, reply Reply, raw string) {
	cmd, arg := splitCommand(raw)
	switch cmd {
	case "/help":
		d.respond(ctx, in, reply, helpText())
	case "/sessions":
		d.respond(ctx, in, reply, d.renderSessions())
	case "/attach":
		if arg == "" {
			d.respond(ctx, in, reply, "usage: /attach <session-id>")
			return
		}
		if err := d.Router.Bind(in.Channel, in.ChatKey, in.Thread, arg); err != nil {
			d.respond(ctx, in, reply, "attach failed: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, "attached to session "+arg)
	case "/follow":
		target := arg
		if target == "" {
			target = "tui"
		}
		if err := d.Router.Follow(in.ChatKey, target); err != nil {
			d.respond(ctx, in, reply, "follow failed: "+err.Error())
			return
		}
		if target == "tui" {
			d.respond(ctx, in, reply, "following the TUI's active session — your messages will land wherever the terminal is.")
		} else {
			d.respond(ctx, in, reply, "pinned to session "+target)
		}
	case "/unfollow":
		_ = d.Router.Follow(in.ChatKey, "")
		d.respond(ctx, in, reply, "follow cleared")
	case "/new":
		sid := NewSessionID(in.Channel)
		_ = d.Router.Bind(in.Channel, in.ChatKey, in.Thread, sid)
		_ = d.Router.Follow(in.ChatKey, "")
		d.respond(ctx, in, reply, "new session: "+sid)
	case "/end":
		_ = d.Router.Follow(in.ChatKey, "")
		d.respond(ctx, in, reply, "follow cleared. binding kept; reply here to keep using this session.")
	default:
		// Unknown slash → treat as agent input.
		sid := d.resolveSession(in)
		d.serialize(sid, func() { d.runTurn(ctx, in, reply, sid, raw) })
	}
}

// respond posts a one-shot text reply for slash commands. We piggyback
// on the same Reply surface so each channel formats consistently.
func (d *Dispatcher) respond(ctx context.Context, in Inbound, reply Reply, text string) {
	postCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	handle, err := reply.PostInitial(postCtx, in, text)
	cancel()
	if err != nil {
		d.Logger.Printf("dispatch: respond: %v", err)
		return
	}
	_ = reply.Final(ctx, handle, text)
}

func (d *Dispatcher) renderSessions() string {
	if d.Router == nil {
		return "no router configured"
	}
	rows := d.Router.Recent(10)
	if len(rows) == 0 {
		return "no recent sessions yet — send a message to start one."
	}
	var b strings.Builder
	b.WriteString("recent sessions (newest first):\n")
	for i, r := range rows {
		fmt.Fprintf(&b, "%d. %s  [%s]  %s ago\n", i+1, r.SessionID, r.Channel, humanAgo(r.Updated))
	}
	b.WriteString("\nuse /attach <session-id> to bind this chat, or /follow tui to mirror the terminal.")
	return b.String()
}

func helpText() string {
	return strings.Join([]string{
		"ethos remote — commands:",
		"  /sessions         list recent sessions",
		"  /attach <id>      bind this chat to a session",
		"  /follow tui       mirror whatever session the TUI is using",
		"  /follow <id>      pin to a specific session",
		"  /unfollow         clear follow mode",
		"  /new              start a fresh session for this chat",
		"  /end              clear follow but keep the binding",
		"  /help             this message",
		"anything else is sent to the agent.",
	}, "\n")
}

func splitCommand(raw string) (cmd, arg string) {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, ' '); i > 0 {
		return strings.ToLower(raw[:i]), strings.TrimSpace(raw[i+1:])
	}
	return strings.ToLower(raw), ""
}

func humanAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}
