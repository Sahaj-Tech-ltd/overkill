package gateway

// isPrePairingCommand returns true for slash commands that are safe to
// execute before pairing approval. These commands neither execute code
// nor mutate agent state — they only operate on the session router
// (bind, follow, unfollow) or return static help text.
//
// The pairing flow itself depends on /new (create session) and /help
// (display the challenge code to the user), so these MUST be available
// before the pairing gate.
func (d *Dispatcher) isPrePairingCommand(cmd string) bool {
	switch cmd {
	case "/help", "/new", "/attach", "/follow", "/unfollow", "/end":
		return true
	default:
		return false
	}
}
