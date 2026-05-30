package gateway

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

const (
	// maxTextLen caps the text per inbound message to prevent OOM from
	// huge pastes.
	maxTextLen = 100_000
	// maxImageBytes caps total image bytes per message.
	maxImageBytes = 20_000_000
	// maxReqsPerMinute is the rate limit per sender per minute.
	maxReqsPerMinute = 10
)

// Dispatcher is the shared "message arrives → reply goes out" engine
// every Channel hands its inbound messages to. It owns:
//   - session resolution via SessionRouter (incl. follow mode)
//   - the small set of slash commands a remote user needs to manage
//     their session from a phone (/sessions, /attach, /follow, /new,
//     /end, /help)
//   - per-session serialization so two phones blasting the same chat
//     don't interleave turns inside the agent loop
//   - sender pairing gate: unapproved senders are challenged before
//     their messages are processed
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

	// SessionTitler, when set, is called once per session when the
	// first user message arrives, so the session Title is auto-set.
	// Nil disables auto-titling. Called synchronously before the
	// agent turn — should be fast (fire-and-forget is fine).
	SessionTitler func(sessionID, firstMsg string)

	// Pairing, when set, gates inbound messages from unknown senders.
	// Unapproved senders receive a challenge code; their messages are
	// not processed or persisted until the owner approves them.
	Pairing PairingGate

	// InputHistory, when set, records incoming messages to PostgreSQL
	// for context-aware reply history. Recording happens only after
	// the pairing gate passes — unapproved messages are never stored.
	InputHistory InputHistoryStore

	// MsgDelivery, when set, tracks message delivery status in Postgres.
	// Messages are recorded as 'pending' before the provider call.
	// On provider failure they're marked 'failed' — on the next Handle()
	// call for the same session, failed messages are auto-retried first.
	// On success they're marked 'delivered'. No message is ever lost.
	MsgDelivery *MessageDelivery

	// OnActivity, when set, is called every time a non-command user
	// message is accepted for processing (after rate-limit and pairing
	// gates). Cron systems wire their ActivityTracker.Record here so
	// cron output delivery can defer until the user is idle.
	OnActivity func()

	// CostTracker, when set, powers the /usage slash command and the
	// session.usage RPC endpoint. Nil disables usage tracking from
	// gateways (the command returns a helpful error message).
	CostTracker cost.Tracker

	// lockStripes is a fixed-size striped mutex pool. The old
	// `locks map[string]*sync.Mutex` grew O(distinct session ids)
	// without bound — a bot serving thousands of WhatsApp/Telegram
	// chats accumulated thousands of stale mutex entries. The pool
	// gives the same serialise-per-session semantics with bounded
	// memory at the cost of rare contention between unrelated
	// sessions whose IDs collide on hash.
	lockStripes [64]sync.Mutex

	// visionSem limits concurrent vision.Describe calls to prevent
	// unbounded goroutine spawn when processing multi-image messages.
	visionSem chan struct{}

	// rateLimits tracks per-sender request timestamps for rate limiting.
	rateLimits   map[string][]time.Time
	rateLimitsMu sync.Mutex

	// active tracks which sessions currently have a running turn.
	// When a new message arrives for an active session, it's queued
	// and the user gets an acknowledgement — the running turn is
	// NOT interrupted. Queued messages auto-fire FIFO when the
	// turn completes.
	active   map[string]bool
	activeMu sync.Mutex

	// pending holds message queues for sessions with active turns.
	// When a user sends while the agent is busy, their message is
	// appended. Messages are dequeued FIFO — each turn processes
	// the oldest first. If more messages remain after a turn
	// completes, the next one auto-fires.
	pending   map[string][]string
	pendingMu sync.Mutex
}

// NewDispatcher returns a Dispatcher with sensible defaults filled in.
func NewDispatcher(ag AgentSender, r *SessionRouter) *Dispatcher {
	return &Dispatcher{
		Agent:       ag,
		Router:      r,
		Logger:      log.New(io.Discard, "", 0),
		UpdateEvery: 750 * time.Millisecond,
		active:      make(map[string]bool),
		pending:     make(map[string][]string),
		visionSem:   make(chan struct{}, 4), // max 4 concurrent vision calls
		rateLimits:  make(map[string][]time.Time),
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

	// Cron channel messages: deliver output directly without agent processing.
	// The delivery target (in.ChatKey) is filtered at the call site — by the
	// time Handle() is invoked, the Reply is already bound to the right channel.
	if in.Channel == "cron" {
		d.respond(ctx, in, reply, text)
		return
	}

	// H-17: Rate limiting — at most maxReqsPerMinute per sender.
	if in.From != "" && !d.checkRateLimit(in.From) {
		d.Logger.Printf("dispatch: rate limit exceeded for %s", in.From)
		return
	}

	// H-17: Message size validation.
	if len(text) > maxTextLen {
		text = text[:maxTextLen]
	}
	var totalImageBytes int64
	for _, img := range in.Images {
		totalImageBytes += int64(len(img.Bytes))
	}
	if totalImageBytes > maxImageBytes {
		d.Logger.Printf("dispatch: image size %d exceeds limit %d", totalImageBytes, maxImageBytes)
		return
	}

	// Slash commands: always route immediately — they're fast.
	if text != "" && strings.HasPrefix(text, "/") && len(in.Images) == 0 {
		d.handleCommand(ctx, in, reply, text)
		return
	}

	// H-13: Pairing gate — check sender approval before processing any
	// non-command message, including image-only messages.
	if d.Pairing != nil && in.From != "" && !d.Pairing.IsApproved(in.Channel, in.From) {
		d.Logger.Printf("dispatch: unapproved sender %q — rejecting", in.From)
		return
	}

	// Record activity for idle tracking (cron output buffering).
	if d.OnActivity != nil {
		d.OnActivity()
	}

	prompt := d.buildPrompt(ctx, in, text)
	if prompt == "" {
		return
	}
	sid := d.resolveSession(in)

	// If the session is busy (agent mid-turn), queue the message
	// and acknowledge. Do NOT interrupt — the running turn finishes
	// naturally, then the queue auto-fires FIFO.
	d.activeMu.Lock()
	if d.active[sid] {
		d.pendingMu.Lock()
		d.pending[sid] = append(d.pending[sid], prompt)
		queueLen := len(d.pending[sid])
		d.pendingMu.Unlock()
		d.activeMu.Unlock()
		// Acknowledge without interrupting the agent.
		if queueLen == 1 {
			d.respond(ctx, in, reply, "📥 You're next in line — agent is working on your last request.")
		} else {
			d.respond(ctx, in, reply, fmt.Sprintf("📥 You're %s in line — agent is still working on turn %d.", ordinal(queueLen), queueLen))
		}
		return
	}
	d.active[sid] = true
	d.activeMu.Unlock()

	defer func() {
		d.activeMu.Lock()
		delete(d.active, sid)
		d.activeMu.Unlock()
	}()

	// Auto-retry any undelivered messages from previous failed turns.
	// These are messages the user sent that the provider rejected (rate
	// limit, invalid API key, etc.). They're retried BEFORE the new
	// message so the user sees their original request fulfilled first.
	if d.MsgDelivery != nil {
		undelivered, _ := d.MsgDelivery.GetUndelivered(ctx, sid)
		if len(undelivered) > 0 {
			// Mark them as pending for retry.
			d.MsgDelivery.RetryUndelivered(ctx, sid)
			for _, um := range undelivered {
				// Create a fresh delivery record for this retry attempt.
				retryID, _ := d.MsgDelivery.RecordPending(ctx, sid, in.ChatKey, um.Message+" [auto-retry]")
				go d.runTurn(context.Background(), in, reply, sid, um.Message, retryID)
			}
		}
	}

	// Record this message as pending so it survives provider failure.
	var deliveryID int64
	if d.MsgDelivery != nil {
		deliveryID, _ = d.MsgDelivery.RecordPending(ctx, sid, in.ChatKey, prompt)
	}

	// Check for queued messages from previous interrupts (FIFO).
	d.pendingMu.Lock()
	queue := d.pending[sid]
	var queued string
	if len(queue) > 0 {
		queued = queue[0]
		d.pending[sid] = queue[1:]
	}
	if len(d.pending[sid]) == 0 {
		delete(d.pending, sid)
	}
	d.pendingMu.Unlock()

	if queued != "" {
		prompt = queued + "\n\n[new message]\n" + prompt
	}

	d.runTurn(ctx, in, reply, sid, prompt, deliveryID)

	// If more messages are queued, auto-fire the next turn.
	d.pendingMu.Lock()
	if len(d.pending[sid]) > 0 {
		nextPrompt := d.pending[sid][0]
		d.pending[sid] = d.pending[sid][1:]
		if len(d.pending[sid]) == 0 {
			delete(d.pending, sid)
		}
		d.pendingMu.Unlock()
		// H-15: Use background context so the queued auto-fire isn't
		// killed by cancellation of the original HTTP/poll request.
		go d.runTurn(context.Background(), in, reply, sid, nextPrompt, 0)
	} else {
		d.pendingMu.Unlock()
	}
}

// checkRateLimit returns true if the sender is within the rate limit.
func (d *Dispatcher) checkRateLimit(from string) bool {
	d.rateLimitsMu.Lock()
	defer d.rateLimitsMu.Unlock()

	now := time.Now()
	window := now.Add(-time.Minute)

	// Prune old entries.
	timestamps := d.rateLimits[from]
	cut := 0
	for cut < len(timestamps) && timestamps[cut].Before(window) {
		cut++
	}
	timestamps = timestamps[cut:]

	if len(timestamps) >= maxReqsPerMinute {
		d.rateLimits[from] = timestamps
		return false
	}

	timestamps = append(timestamps, now)
	d.rateLimits[from] = timestamps
	return true
}

// buildPrompt prepends "[image: <caption>]" lines for any attached
// images so the text-only main agent has a verbal handle on them. If
// no Vision describer is wired we fall back to a blunt placeholder so
// the user knows their image was dropped.
// H-14: Vision calls run concurrently with a semaphore to avoid
// blocking for up to 30s × N images sequentially.
func (d *Dispatcher) buildPrompt(ctx context.Context, in Inbound, text string) string {
	if len(in.Images) == 0 {
		return text
	}
	var (
		b   strings.Builder
		mu  sync.Mutex
		wg  sync.WaitGroup
	)
	for i, img := range in.Images {
		if d.Vision == nil {
			fmt.Fprintf(&b, "[image %d attached — vision describer not configured; ignoring]\n", i+1)
			continue
		}
		wg.Add(1)
		go func(idx int, img InboundImage) {
			defer wg.Done()
			// Acquire semaphore slot to limit concurrent vision calls.
			d.visionSem <- struct{}{}
			defer func() { <-d.visionSem }()

			visionImg := []vision.Image{{Bytes: img.Bytes, Mime: img.Mime}}
			descCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			desc, err := d.Vision.Describe(descCtx, visionImg, "")
			cancel()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				d.Logger.Printf("dispatch: vision describe: %v", err)
				fmt.Fprintf(&b, "[image %d: vision failed: %s]\n", idx+1, err.Error())
				return
			}
			fmt.Fprintf(&b, "[image %d attached by user — vision model says: %s]\n", idx+1, desc)
		}(i, img)
	}
	wg.Wait()
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
func (d *Dispatcher) runTurn(ctx context.Context, in Inbound, reply Reply, sessionID, text string, deliveryID int64) {
	postCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	handle, err := reply.PostInitial(postCtx, in, "⏳ thinking…")
	cancel()
	if err != nil {
		d.Logger.Printf("dispatch: post initial: %v", err)
		return
	}

	if d.Agent == nil {
		_ = reply.Error(ctx, handle, fmt.Errorf("no agent configured"))
		if deliveryID > 0 && d.MsgDelivery != nil {
			d.MsgDelivery.MarkFailed(ctx, deliveryID, "no agent configured")
		}
		return
	}

	// Auto-title the session from the first user message.
	if d.SessionTitler != nil {
		d.SessionTitler(sessionID, text)
	}

	d.serialize(sessionID, func() {
		d.Agent.SetSessionID(sessionID)
	})
	stream, err := d.Agent.Stream(ctx, text)
	if err != nil {
		_ = reply.Error(ctx, handle, err)
		if deliveryID > 0 && d.MsgDelivery != nil {
			d.MsgDelivery.MarkFailed(ctx, deliveryID, err.Error())
		}
		return
	}
	d.streamInto(ctx, reply, handle, stream)
	if d.Router != nil {
		d.Router.Touch(in.Channel, in.ChatKey, in.Thread)
	}
	// Mark delivered — the provider accepted and streamed a response.
	if deliveryID > 0 && d.MsgDelivery != nil {
		d.MsgDelivery.MarkDelivered(ctx, deliveryID)
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
	stop := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-stream:
				if !ok {
					return
				}
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
						mu.Lock()
						streamErr = ev.Error
						mu.Unlock()
					}
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			close(stop)
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
		if d.Router == nil {
			d.respond(ctx, in, reply, "router not available")
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
		if d.Router == nil {
			d.respond(ctx, in, reply, "router not available")
			return
		}
		if err := d.Router.Follow(in.Channel, in.ChatKey, target); err != nil {
			d.respond(ctx, in, reply, "follow failed: "+err.Error())
			return
		}
		if target == "tui" {
			d.respond(ctx, in, reply, "following the TUI's active session — your messages will land wherever the terminal is.")
		} else {
			d.respond(ctx, in, reply, "pinned to session "+target)
		}
	case "/unfollow":
		if d.Router == nil {
			d.respond(ctx, in, reply, "router not available")
			return
		}
		if err := d.Router.Follow(in.Channel, in.ChatKey, ""); err != nil {
			d.Logger.Printf("dispatch: /unfollow: %v", err)
		}
		d.respond(ctx, in, reply, "follow cleared")
	case "/new":
		sid := NewSessionID(in.Channel)
		if d.Router == nil {
			d.respond(ctx, in, reply, "router not available")
			return
		}
		if err := d.Router.Bind(in.Channel, in.ChatKey, in.Thread, sid); err != nil {
			d.Logger.Printf("dispatch: /new bind: %v", err)
		}
		if err := d.Router.Follow(in.Channel, in.ChatKey, ""); err != nil {
			d.Logger.Printf("dispatch: /new follow: %v", err)
		}
		d.respond(ctx, in, reply, "new session: "+sid)
	case "/end":
		if d.Router == nil {
			d.respond(ctx, in, reply, "router not available")
			return
		}
		if err := d.Router.Follow(in.Channel, in.ChatKey, ""); err != nil {
			d.Logger.Printf("dispatch: /end unfollow: %v", err)
		}
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
		// 10s budget: bookmark stores are typically DB writes
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
	case "/estop":
		if d.Agent == nil {
			d.respond(ctx, in, reply, "agent not available")
			return
		}
		d.Agent.EStop()
		d.respond(ctx, in, reply, "🛑 emergency stop triggered. all running agent loops halted.")
	case "/stop":
		sid := d.resolveSession(in)
		d.pendingMu.Lock()
		queueLen := len(d.pending[sid])
		d.pendingMu.Unlock()
		if d.Agent == nil {
			d.respond(ctx, in, reply, "agent not available")
			return
		}
		d.Agent.Interrupt()
		if queueLen > 0 {
			d.respond(ctx, in, reply, fmt.Sprintf("⏸️ stopped. %d message(s) queued.", queueLen))
		} else {
			d.respond(ctx, in, reply, "⏸️ stopped. nothing queued.")
		}
	case "/undo":
		if d.Agent == nil {
			d.respond(ctx, in, reply, "agent not available")
			return
		}
		result, err := d.Agent.Undo()
		if err != nil {
			d.respond(ctx, in, reply, "undo failed: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, result)
	case "/retry":
		if d.Agent == nil {
			d.respond(ctx, in, reply, "agent not available")
			return
		}
		// Retry is async — fire and post placeholder.
		d.respond(ctx, in, reply, "🔄 retrying last message...")
		// Run retry in background since it may take a while.
		go func() {
			res, err := d.Agent.Retry()
			if err != nil {
				d.respond(context.Background(), in, reply, "retry failed: "+err.Error())
				return
			}
			d.respond(context.Background(), in, reply, res)
		}()
	case "/steer":
		if d.busyGuard(ctx, in, reply, "steer") {
			return
		}
		if arg == "" {
			d.respond(ctx, in, reply, "usage: /steer <guidance message> — inject mid-run guidance into the agent loop")
			return
		}
		if d.Agent == nil {
			d.respond(ctx, in, reply, "agent not available")
			return
		}
		result := d.Agent.Steer(arg)
		d.respond(ctx, in, reply, result)
	case "/fork":
		if d.busyGuard(ctx, in, reply, "fork") {
			return
		}
		name := arg
		if name == "" {
			name = fmt.Sprintf("fork-%s", time.Now().Format("20060102-150405"))
		}
		if d.Agent == nil {
			d.respond(ctx, in, reply, "agent not available")
			return
		}
		newSID, err := d.Agent.Fork(name)
		if err != nil {
			d.respond(ctx, in, reply, "fork failed: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, fmt.Sprintf("🔀 forked to new session: %s (%s)", newSID, name))
	case "/goal":
		if d.busyGuard(ctx, in, reply, "goal") {
			return
		}
		if arg == "" {
			if d.Agent == nil {
				d.respond(ctx, in, reply, "agent not available")
				return
			}
			gctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			current, err := d.Agent.GetGoal(gctx)
			cancel()
			if err != nil {
				d.respond(ctx, in, reply, "goal: "+err.Error())
				return
			}
			if current == "" {
				d.respond(ctx, in, reply, "no goal set. use /goal set <objective> to create one.")
				return
			}
			d.respond(ctx, in, reply, fmt.Sprintf("🎯 current goal: %s", current))
			return
		}
		parts, _ := splitCommand(arg)
		switch parts {
		case "set":
			goalText := strings.TrimSpace(arg[len("set"):])
			if goalText == "" {
				d.respond(ctx, in, reply, "usage: /goal set <objective>")
				return
			}
			if d.Agent == nil {
				d.respond(ctx, in, reply, "agent not available")
				return
			}
			gctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := d.Agent.SetGoal(gctx, goalText)
			cancel()
			if err != nil {
				d.respond(ctx, in, reply, "goal set failed: "+err.Error())
				return
			}
			d.respond(ctx, in, reply, fmt.Sprintf("🎯 goal set: %s", goalText))
		case "pause":
			if d.Agent == nil {
				d.respond(ctx, in, reply, "agent not available")
				return
			}
			gctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := d.Agent.PauseGoal(gctx)
			cancel()
			if err != nil {
				d.respond(ctx, in, reply, "goal pause failed: "+err.Error())
				return
			}
			d.respond(ctx, in, reply, "⏸️ goal paused")
		case "resume":
			if d.Agent == nil {
				d.respond(ctx, in, reply, "agent not available")
				return
			}
			gctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := d.Agent.ResumeGoal(gctx)
			cancel()
			if err != nil {
				d.respond(ctx, in, reply, "goal resume failed: "+err.Error())
				return
			}
			d.respond(ctx, in, reply, "▶️ goal resumed")
		case "clear":
			if d.Agent == nil {
				d.respond(ctx, in, reply, "agent not available")
				return
			}
			gctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := d.Agent.ClearGoal(gctx)
			cancel()
			if err != nil {
				d.respond(ctx, in, reply, "goal clear failed: "+err.Error())
				return
			}
			d.respond(ctx, in, reply, "🗑️ goal cleared")
		default:
			d.respond(ctx, in, reply, "usage: /goal [set|pause|resume|clear] — manage the active session goal")
		}
	case "/snapshot":
		name := arg
		if name == "" {
			name = fmt.Sprintf("snap-%s", time.Now().Format("20060102-150405"))
		}
		if d.Agent == nil {
			d.respond(ctx, in, reply, "agent not available")
			return
		}
		hash, err := d.Agent.Snapshot(name)
		if err != nil {
			d.respond(ctx, in, reply, "snapshot failed: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, fmt.Sprintf("📸 snapshot created: %s (%s)", name, hash))
	case "/rollback":
		n := 0
		if arg != "" {
			fmt.Sscanf(arg, "%d", &n)
		}
		if d.Agent == nil {
			d.respond(ctx, in, reply, "agent not available")
			return
		}
		result, err := d.Agent.Rollback(n)
		if err != nil {
			d.respond(ctx, in, reply, "rollback failed: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, result)
	case "/snapshots":
		if d.Agent == nil {
			d.respond(ctx, in, reply, "agent not available")
			return
		}
		result, err := d.Agent.Snapshots()
		if err != nil {
			d.respond(ctx, in, reply, "snapshots: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, result)
	case "/usage":
		d.handleUsage(ctx, in, reply, arg)
	default:
		// Unknown slash → treat as agent input.
		sid := d.resolveSession(in)
		d.runTurn(ctx, in, reply, sid, raw, 0)
	}
}

// handleUsage responds to the /usage command. With no argument it shows
// session-level usage. With "daily" it shows today's usage across all
// sessions. With "all" it shows total lifetime usage.
func (d *Dispatcher) handleUsage(ctx context.Context, in Inbound, reply Reply, arg string) {
	if d.CostTracker == nil {
		d.respond(ctx, in, reply, "usage tracking not wired — set DATABASE_URL and configure [cost] in your config")
		return
	}
	switch strings.ToLower(arg) {
	case "daily":
		daily, err := d.CostTracker.DailyCost(ctx)
		if err != nil {
			d.respond(ctx, in, reply, "daily usage: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, d.formatCostSummary("📊 Usage today", &daily))
	case "all":
		report, err := d.CostTracker.Usage(ctx, cost.UsageOptions{})
		if err != nil {
			d.respond(ctx, in, reply, "usage: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, d.formatUsageReport("📊 Lifetime usage", report))
	default:
		sid := d.resolveSession(in)
		report, err := d.CostTracker.Usage(ctx, cost.UsageOptions{SessionID: sid})
		if err != nil {
			d.respond(ctx, in, reply, "session usage: "+err.Error())
			return
		}
		d.respond(ctx, in, reply, d.formatUsageReport("📊 Usage this session", report))
	}
}

func (d *Dispatcher) formatCostSummary(title string, s *cost.CostSummary) string {
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	fmt.Fprintf(&b, "  Input:   %s tokens\n", cost.FormatTokens(s.InputTokens))
	fmt.Fprintf(&b, "  Output:  %s tokens\n", cost.FormatTokens(s.OutputTokens))
	if s.CachedTokens > 0 {
		fmt.Fprintf(&b, "  Cached:  %s tokens\n", cost.FormatTokens(s.CachedTokens))
	}
	fmt.Fprintf(&b, "  Cost:    %s\n", cost.FormatUSD(s.TotalUSD))
	fmt.Fprintf(&b, "  Requests: %d\n", s.RequestCount)
	return b.String()
}

func (d *Dispatcher) formatUsageReport(title string, r *cost.UsageReport) string {
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	fmt.Fprintf(&b, "  Input:   %s tokens\n", cost.FormatTokens(r.Summary.InputTokens))
	fmt.Fprintf(&b, "  Output:  %s tokens\n", cost.FormatTokens(r.Summary.OutputTokens))
	if r.Summary.CachedTokens > 0 {
		fmt.Fprintf(&b, "  Cached:  %s tokens\n", cost.FormatTokens(r.Summary.CachedTokens))
	}
	fmt.Fprintf(&b, "  Cost:    %s\n", cost.FormatUSD(r.Summary.TotalUSD))
	fmt.Fprintf(&b, "  Requests: %d\n", r.Summary.RequestCount)

	if len(r.ByModel) > 0 {
		b.WriteString("\n  By model:\n")
		// Sort models by total cost descending for consistent output.
		type modelEntry struct {
			name string
			cs   cost.CostSummary
		}
		models := make([]modelEntry, 0, len(r.ByModel))
		for name, cs := range r.ByModel {
			models = append(models, modelEntry{name, cs})
		}
		sort.Slice(models, func(i, j int) bool {
			return models[i].cs.TotalUSD > models[j].cs.TotalUSD
		})
		for _, m := range models {
			fmt.Fprintf(&b, "    %-20s %s in / %s out / %s\n",
				m.name,
				cost.FormatTokens(m.cs.InputTokens),
				cost.FormatTokens(m.cs.OutputTokens),
				cost.FormatUSD(m.cs.TotalUSD),
			)
		}
	}
	return b.String()
}

// busyGuard checks whether the agent is currently running a turn. When busy it
// replies with a friendly message and returns true (caller should return).
// Returns false when the agent is idle — safe to proceed.
func (d *Dispatcher) busyGuard(ctx context.Context, in Inbound, reply Reply, commandName string) bool {
	if d.Agent == nil {
		return false // no agent to check — let command proceed (will fail on nil check)
	}
	if d.Agent.IsBusy() {
		d.respond(ctx, in, reply, fmt.Sprintf(
			"⚠️ Agent is currently working on a turn. Use /stop to interrupt first, then try /%s again.",
			commandName,
		))
		return true
	}
	return false
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
	if err := reply.Final(ctx, handle, text); err != nil {
		d.Logger.Printf("dispatch: respond final: %v", err)
	}
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
		"  /stop             stop the current turn (queues your next message)",
		"  /estop            emergency stop ALL agent loops",
		"  /undo             remove the last exchange from history",
		"  /retry            replay the last user message",
		"  /steer <msg>      inject mid-run guidance into the agent loop",
		"  /fork [name]      branch current session into a new one",
		"  /goal             show the current objective",
		"  /goal set <obj>   set or update the standing goal",
		"  /goal pause       pause the current goal",
		"  /goal resume      resume a paused goal",
		"  /goal clear       remove the current goal",
		"  /snapshot [name]  create a git-based filesystem checkpoint",
		"  /snapshots        list saved checkpoints",
		"  /rollback [N]     roll back N checkpoints (0=latest)",
		"/bm <label>       bookmark the active session with a label",
		"/usage [daily|all] show token usage and cost for this session",
		"/help             this message",
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

// ordinal returns the English ordinal for n (1st, 2nd, 3rd, 4th, ...).
func ordinal(n int) string {
	switch n % 100 {
	case 11, 12, 13:
		return fmt.Sprintf("%dth", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("%dst", n)
	case 2:
		return fmt.Sprintf("%dnd", n)
	case 3:
		return fmt.Sprintf("%drd", n)
	}
	return fmt.Sprintf("%dth", n)
}
