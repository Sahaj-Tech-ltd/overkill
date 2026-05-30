package gateway

import "context"

// PairingGate checks whether a sender is approved to interact with the
// agent. Implementations wrap a pairing store (e.g. security.PairingStore)
// and return true when the sender is on the allow-from list or pairing
// is disabled for the channel. False means the sender should receive a
// challenge code and their message should be discarded.
type PairingGate interface {
	IsApproved(channel, senderID string) bool
	IssueChallenge(channel, senderID string) (string, error)
}

// InputHistoryStore persists incoming messages for context-aware
// reply history. Implementations wrap a Postgres-backed ring buffer
// (see InputHistory in input_history.go).
type InputHistoryStore interface {
	Append(ctx context.Context, chatKey, text string) error
}
