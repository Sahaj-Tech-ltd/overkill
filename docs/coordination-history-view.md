# Readable saved coordination history

Desktop saved transcripts recognize the application's bounded coordination
message envelope and show its payload in a coordination card. A card identifies
the message kind, task and sender/recipient route. Names come from the current
chat's authorized peer inventory; unavailable names use abbreviated IDs. The
card labels itself a saved snapshot and does not turn its recorded started
state into a live progress claim. Inspect current task uses the existing
session-bound API and its participant checks.

The exact original stored text remains in a collapsed disclosure. Structured
payloads remain readable JSON; delivery excerpts retain a truncation notice.
Malformed, unrelated, oversized and unknown-kind messages retain ordinary text
rendering. Envelope formatting is presentation, not authenticated provenance or
permission to execute peer input. All payloads, task labels and original text
render as text nodes, including strings containing HTML. Provider inputs,
persisted messages, sender authority and worker execution are unchanged.

Protocol-generated chat titles display an agent-workspace title derived from
the saved envelope's first task, with a generic fallback before history loads.
Ordinary chat titles are kept. The original title/messages are not rewritten.

The native fixture opens the backend's real committed transcript after its
cross-worktree task, checks the named route and payload, opens the original
stored message, and inspects current task state. Pure rendering tests cover
malformed fields, prototype-like kind names, structured/truncated payloads,
HTML-as-text, and title fallback. See `.cache/desktop-coordination-history.png`.
