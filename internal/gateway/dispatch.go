package gateway

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
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
	Agent  AgentSender
	Router *SessionRouter
	Vision vision.Describer // optional; when set, attached images are
	// captioned and inlined into the agent prompt
	Logger      *log.Logger
	UpdateEvery time.Duration // batch reply edits; default 750ms

	// Bookmark, when set, handles the /bm <label> slash command from
	// any gateway. Nil leaves the command unwired; users see a
	// helpful error rather than a silent no-op.
	Bookmark BookmarkFn

	// lockStripes is a fixed-size striped mutex pool. The old
	// `locks map[string]*sync.Mutex` grew O(distinct session ids)
	// without bound — a bot serving thousands of WhatsApp/Telegram
	// chats accumulated thousands of stale mutex entries. The pool
	// gives the same serialise-per-session semantics with bounded
	// memory at the cost of rare contention between unrelated
	// sessions whose IDs collide on hash.
	lockStripes [64]sync.Mutex
}

// NewDispatcher returns a Dispatcher with sensible defaults filled in.
func NewDispatcher(ag AgentSender, r *SessionRouter) *Dispatcher {
	return &Dispatcher{
		Agent:       ag,
		Router:      r,
		Logger:      log.New(io.Discard, "", 0),
		UpdateEvery: 750 * time.Millisecond,
	}
}

// BookmarkFn is the callback invoked when a user runs /bm <label>
// in any gateway. The dispatcher resolves the active session for the
// chat, then asks this function to persist a bookmark with that
// label against that session. Return non-nil error to signal failure
// — the reply to the user surfaces it verbatim.
//
// Defined as a field rather than a constructor argument so the
// caller can wire it after construction (some bookmark backends
// need the dispatcher to exist first).
type BookmarkFn func(ctx context.Context, sessionID, label string) error

// Handle is called by a Channel for each Inbound it receives. Reply is
// the channel's own transport. Errors are logged and surfaced via
// reply.Error rather than returned, because gateway loops shouldn't die
// over one bad message.
func (d *Dispatcher) Handle(ctx context.Context, in Inbound, reply Reply) {
	text := strings.TrimSpace(in.Text)
	if text == "" && len(in.Images) == 0 {
		return
	}
	// Slash commands: only route as commands when there's no image. An
	// image with /caption as its caption is just a labeled image.
	if text != "" && strings.HasPrefix(text, "/") && len(in.Images) == 0 {
		d.handleCommand(ctx, in, reply, text)
		return
	}

	prompt := d.buildPrompt(ctx, in, text)
	if prompt == "" {
		return
	}
	sid := d.resolveSession(in)
	d.serialize(sid, func() { d.runTurn(ctx, in, reply, sid, prompt) })
}

// buildPrompt prepends "[image: <caption>]" lines for any attached
// images so the text-only main agent has a verbal handle on them. If
// no Vision describer is wired we fall back to a blunt placeholder so
// the user knows their image was dropped.
func (d *Dispatcher) buildPrompt(ctx context.Context, in Inbound, text string) string {
	if len(in.Images) == 0 {
		return text
	}
	var b strings.Builder
	for i, img := range in.Images {
		if d.Vision == nil {
			fmt.Fprintf(&b, "[image %d attached — vision describer not configured; ignoring]\n", i+1)
			continue
		}
		visionImg := []vision.Image{{Bytes: img.Bytes, Mime: img.Mime}}
		descCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		desc, err := d.Vision.Describe(descCtx, visionImg, "")
		cancel()
		if err != nil {
			d.Logger.Printf("dispatch: vision describe: %v", err)
			fmt.Fprintf(&b, "[image %d: vision failed: %s]\n", i+1, err.Error())
			continue
		}
		fmt.Fprintf(&b, "[image %d attached by user — vision model says: %s]\n", i+1, desc)
	}
	if text != "" {
		b.WriteString("\n")
		b.WriteString(text)
	}
	return strings.TrimSpace(b.String())
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
// Uses a striped lock pool — see Dispatcher.lockStripes for the
// bounded-memory rationale.
func (d *Dispatcher) serialize(sessionID string, fn func()) {
	mu := d.stripeFor(sessionID)
	mu.Lock()
	defer mu.Unlock()
	fn()
}

func (d *Dispatcher) stripeFor(sessionID string) *sync.Mutex {
	h := uint32(2166136261) // FNV-1a basis
	for i := 0; i < len(sessionID); i++ {
		h ^= uint32(sessionID[i])
		h *= 16777619
	}
	return &d.lockStripes[h%uint32(len(d.lockStripes))]
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
					// Behind mu so a future reader in another select arm
					// (e.g. ctx.Done logging) doesn't introduce a race.
					// The current <-done read is happens-before safe via
					// channel close, but the lock makes the invariant
					// "streamErr is accessed under mu" hold globally.
					mu.Lock()
					streamErr = ev.Error
					mu.Unlock()
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
			mu.Lock()
			final := buf.String()
			err := streamErr
			mu.Unlock()
			if err != nil {
				_ = reply.Error(ctx, handle, err)
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
	case "/bm", "/bookmark":
		// §7.4 message bookmarking. Tag the active session with a
		// label so a future "dive into that session" can recall it.
		// The label is everything after /bm — quoted or bare, both
		// fine; we don't try to parse a structure.
		if d.Bookmark == nil {
			d.respond(ctx, in, reply, "bookmark backend not wired on this build")
			return
		}
		label := strings.TrimSpace(arg)
		if label == "" {
			d.respond(ctx, in, reply, "usage: /bm <label> — describe what we were talking about")
			return
		}
		sid := d.resolveSession(in)
		// 10s budget: bookmark stores are typically Badger writes
		// (sub-ms), but allowing more headroom keeps us robust to
		// network-backed stores users might plug in later.
		bmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := d.Bookmark(bmCtx, sid, label)
		cancel()
		if err != nil {
			d.respond(ctx, in, reply, "bookmark failed: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, "bookmarked: "+label)
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
		"overkill remote — commands:",
		"  /sessions         list recent sessions",
		"  /attach <id>      bind this chat to a session",
		"  /follow tui       mirror whatever session the TUI is using",
		"  /follow <id>      pin to a specific session",
		"  /unfollow         clear follow mode",
		"  /new              start a fresh session for this chat",
		"  /end              clear follow but keep the binding",
		"  /bm <label>       bookmark the active session with a label",
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
