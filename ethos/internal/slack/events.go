package slack

// Slack event envelopes. Only the fields we actually consume are typed;
// everything else is intentionally dropped. Slack sends a LOT of fields.

// SocketEnvelope wraps every payload Slack sends over Socket Mode.
// envelope_id must be echoed back as the ack so Slack stops retrying.
type SocketEnvelope struct {
	Type                   string           `json:"type"`        // "hello" | "events_api" | "disconnect" | "interactive" | ...
	EnvelopeID             string           `json:"envelope_id"` // omitted for "hello"
	AcceptsResponsePayload bool             `json:"accepts_response_payload"`
	Payload                EventsAPIPayload `json:"payload"`
	Reason                 string           `json:"reason"` // for "disconnect"
}

// EventsAPIPayload is the inner envelope for the "events_api" type.
type EventsAPIPayload struct {
	Type     string `json:"type"` // "event_callback"
	TeamID   string `json:"team_id"`
	APIAppID string `json:"api_app_id"`
	Event    Event  `json:"event"`
	EventID  string `json:"event_id"`
}

// Event covers app_mention and message events. Slack uses a single shape
// with a discriminator field; we type the union and let downstream code
// switch on Type/ChannelType.
type Event struct {
	Type        string `json:"type"`    // "app_mention" | "message" | "assistant_thread_started"
	Subtype     string `json:"subtype"` // "bot_message" etc — we ignore non-empty subtypes
	User        string `json:"user"`
	BotID       string `json:"bot_id"` // present on bot-authored messages — skip those
	Text        string `json:"text"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"` // empty for top-level posts
	Channel     string `json:"channel"`
	ChannelType string `json:"channel_type"` // "im" for DMs, "channel"/"group" otherwise
}

// IsFromBot reports whether the event was authored by a bot (including us).
// Used to drop our own posts so we don't loop on @mentions of ourselves.
func (e Event) IsFromBot() bool {
	return e.BotID != "" || e.Subtype == "bot_message"
}

// ThreadAnchor returns the ts to use as the "session anchor" for a thread.
// Slack semantics: a top-level message has thread_ts == "" and ts ==
// message-ts; replies have thread_ts == parent-ts. We always anchor on the
// parent so all replies in a thread share the same session.
func (e Event) ThreadAnchor() string {
	if e.ThreadTS != "" {
		return e.ThreadTS
	}
	return e.TS
}

// connectionsOpenResponse is the response shape for apps.connections.open.
type connectionsOpenResponse struct {
	OK    bool   `json:"ok"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

// postMessageResponse is the response shape for chat.postMessage.
type postMessageResponse struct {
	OK      bool   `json:"ok"`
	Channel string `json:"channel"`
	TS      string `json:"ts"`
	Error   string `json:"error"`
}
