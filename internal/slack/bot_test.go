package slack

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// fakeAPI records every call so tests can assert on the sequence of
// chat.postMessage / chat.update / reactions calls the bot made.
type fakeAPI struct {
	mu        sync.Mutex
	posts     []string // text of each chat.postMessage
	updates   []string // text of each chat.update (in order)
	reactions []string // "add:<name>" or "remove:<name>"
}

func (f *fakeAPI) PostMessage(ctx context.Context, channel, threadTS, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, text)
	return "ts-1", nil
}

func (f *fakeAPI) UpdateMessage(ctx context.Context, channel, ts, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, text)
	return nil
}

func (f *fakeAPI) AddReaction(ctx context.Context, channel, ts, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactions = append(f.reactions, "add:"+name)
	return nil
}

func (f *fakeAPI) RemoveReaction(ctx context.Context, channel, ts, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactions = append(f.reactions, "remove:"+name)
	return nil
}

func (f *fakeAPI) snapshot() ([]string, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...), append([]string(nil), f.updates...), append([]string(nil), f.reactions...)
}

// fakeAgent emits a fixed sequence of stream events so we can assert the
// bot translates them into the right Slack calls.
type fakeAgent struct {
	mu     sync.Mutex
	events []agent.StreamEvent
	sid    string
	got    string
}

func (f *fakeAgent) SetSessionID(id string) { f.sid = id }
func (f *fakeAgent) SessionID() string      { return f.sid }
func (f *fakeAgent) Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error) {
	f.mu.Lock()
	f.got = in
	evs := f.events
	f.mu.Unlock()
	ch := make(chan agent.StreamEvent, len(evs))
	go func() {
		defer close(ch)
		for _, ev := range evs {
			ch <- ev
		}
	}()
	return ch, nil
}

func TestBot_AppMention_RunsThroughStream(t *testing.T) {
	api := &fakeAPI{}
	ag := &fakeAgent{events: []agent.StreamEvent{
		{Type: agent.EventToken, Content: "Hello"},
		{Type: agent.EventToken, Content: " there"},
		{Type: agent.EventToolStart, ToolCall: &providers.ToolCall{Name: "shell", Arguments: `{"cmd":"ls"}`}},
		{Type: agent.EventToolOutput, ToolCall: &providers.ToolCall{Name: "shell", Arguments: `{"cmd":"ls"}`}},
		{Type: agent.EventToken, Content: "\nDone."},
		{Type: agent.EventDone},
	}}

	src := make(chan *SocketEnvelope, 1)
	src <- &SocketEnvelope{
		Type:       "events_api",
		EnvelopeID: "env-1",
		Payload: EventsAPIPayload{
			Type: "event_callback",
			Event: Event{
				Type:        "app_mention",
				User:        "U1",
				Text:        "<@UBOT> hello",
				TS:          "100.000",
				Channel:     "C1",
				ChannelType: "channel",
			},
		},
	}
	close(src)

	sm, _ := NewSessionMap("")
	bot := New(api, ag, sm, "xapp-test", nil)
	bot.UpdateEvery = 10 * time.Millisecond
	bot.EnvelopeSource = src
	bot.AckSink = func(string) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = bot.Run(ctx)

	// Wait for the streaming goroutine to finish settling.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, updates, reactions := api.snapshot()
		if len(updates) > 0 && containsAll(reactions, "add:hourglass", "add:white_check_mark", "remove:hourglass") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	posts, updates, reactions := api.snapshot()
	if len(posts) != 1 {
		t.Fatalf("expected 1 initial post, got %d: %v", len(posts), posts)
	}
	if posts[0] != ":hourglass: thinking…" {
		t.Errorf("unexpected initial post: %q", posts[0])
	}
	if len(updates) == 0 {
		t.Fatalf("expected at least one chat.update")
	}
	final := updates[len(updates)-1]
	if !contains(final, "Hello there") || !contains(final, "Done.") {
		t.Errorf("final update missing tokens: %q", final)
	}
	if !contains(final, ":wrench:") || !contains(final, "shell") {
		t.Errorf("final update missing tool block: %q", final)
	}
	if !contains(joinAll(reactions), "add:hourglass") || !contains(joinAll(reactions), "add:white_check_mark") || !contains(joinAll(reactions), "remove:hourglass") {
		t.Errorf("missing expected reactions: %v", reactions)
	}
	if ag.got != "hello" {
		t.Errorf("agent got %q, want %q", ag.got, "hello")
	}
	if ag.sid == "" {
		t.Errorf("expected session id to be set")
	}
}

func TestBot_DM_OnlyChannelTypeIM(t *testing.T) {
	api := &fakeAPI{}
	ag := &fakeAgent{events: []agent.StreamEvent{{Type: agent.EventToken, Content: "hi"}, {Type: agent.EventDone}}}
	src := make(chan *SocketEnvelope, 1)
	// channel-type message with no mention: should be ignored.
	src <- &SocketEnvelope{Type: "events_api", EnvelopeID: "x", Payload: EventsAPIPayload{Event: Event{
		Type: "message", Channel: "C", User: "U", Text: "hello", TS: "1", ChannelType: "channel",
	}}}
	close(src)

	sm, _ := NewSessionMap("")
	bot := New(api, ag, sm, "xapp-test", nil)
	bot.EnvelopeSource = src
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = bot.Run(ctx)

	posts, updates, _ := api.snapshot()
	if len(posts) != 0 || len(updates) != 0 {
		t.Fatalf("expected no API calls for non-DM message, got posts=%v updates=%v", posts, updates)
	}
}

func TestBot_AllowList(t *testing.T) {
	api := &fakeAPI{}
	ag := &fakeAgent{events: []agent.StreamEvent{{Type: agent.EventDone}}}
	src := make(chan *SocketEnvelope, 1)
	src <- &SocketEnvelope{Type: "events_api", EnvelopeID: "x", Payload: EventsAPIPayload{Event: Event{
		Type: "app_mention", Channel: "C-blocked", User: "U", Text: "<@UBOT> hi", TS: "1",
	}}}
	close(src)

	sm, _ := NewSessionMap("")
	bot := New(api, ag, sm, "xapp-test", []string{"C-allowed"})
	bot.EnvelopeSource = src
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = bot.Run(ctx)

	posts, _, _ := api.snapshot()
	if len(posts) != 0 {
		t.Fatalf("expected blocked channel to skip, got %v", posts)
	}
}

func TestStripMention(t *testing.T) {
	cases := map[string]string{
		"<@UBOT> hello world": "hello world",
		"   <@UBOT>   hi":     "hi",
		"no mention":          "no mention",
		"<@UBOT>":             "",
	}
	for in, want := range cases {
		if got := stripMention(in); got != want {
			t.Errorf("stripMention(%q) = %q want %q", in, got, want)
		}
	}
}

// helpers
func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}
func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
func joinAll(s []string) string {
	out := ""
	for _, x := range s {
		out += x + "\n"
	}
	return out
}
func containsAll(haystack []string, needles ...string) bool {
	set := map[string]bool{}
	for _, h := range haystack {
		set[h] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}
