# Terminal agent coordination

Open the sidebar with Ctrl+B, then Tab to Agents. This panel uses authenticated
production `coordination.*` RPCs scoped to the selected persisted chat. The
existing per-turn `agent.subagents` list is a separate runtime concept; it is not
used as evidence that a durable peer is online.

The panel discovers registered peers in the canonical workspace. `g` opens a
name/role registration form with a review step. Registration binds the identity
to the saved chat; it is not a temporary display name. Runtime presence is supplied by application-owned database leases, refreshed every five seconds and expiring after twenty. It distinguishes a loaded idle runtime, working, waiting for input and finishing an operation. Unloaded/expired runtimes are unknown; registration and provider/delivery health are separate. Multiple observers are explicitly shown rather than inferred to be exclusive. Local expiry continues without a successful transport refresh. See `docs/coordination-presence.md`.

`n` opens a request composer with recipient, optional task, token limit, USD
limit, optional prerequisite request UUIDs, optional required contract pins, and message. Prerequisites accept comma or whitespace separated UUIDs (maximum 32, no duplicates); invalid input cannot be submitted. The server verifies existing request identity, workspace and sender participation. Prerequisites are immutable after acceptance. Contract pins use comma or whitespace separated `UUID@revision` entries (maximum 32 distinct contract IDs, positive safe integer revisions), and are also immutable. Contract agreement gates wait for acknowledgements; a changed or deleted pinned contract fails the request. Enter advances through fields to review; only Enter on the
review step sends. Left/right selects a field; Ctrl+U clears; Escape discards.
Zero limits mean unlimited. Payloads are text requests; no raw RPC, sender,
project, credentials, or arbitrary lifecycle transition controls are exposed.

Correspondence shows persisted states: accepted is labeled queued, started is
working, and blocked/completed/failed/cancelled retain their backend meaning.
An accepted send receipt does not indicate completion. `j/k` selects a message,
Enter inspects, and `o` loads older correspondence with the server cursor.
`r` refreshes. The list refreshes every four seconds, with one operation at a
time. Inspection and draft editing pause automatic refresh.

Inspection has overview, usage, activity, dependency, and agreements/artifacts views (`v` cycles). Overview shows
task/conversation identity, revision, a bounded message excerpt and the last loaded event/payload. If more activity is available this is explicitly a partial snapshot; load subsequent Activity pages to review later events. Usage shows
the actual scope, measured token totals, estimated USD, limits, unknown usage or
price counts, pending/unconfirmed executions, and review requirements. Activity
uses persisted transition revisions, with `j/k` scrolling the loaded events and
`a` loading another activity page. The dependency view loads the authenticated graph at a 100-node bound, shows each request's combined dependency/contract readiness, pinned contract revisions, and distinct request and agreement blocker reasons (budget and runtime admission gates still apply), and supports `j/k` scrolling. Hidden prerequisites and graph truncation are explicit; `r` refreshes inspection, usage and graph together. Acknowledgements and completed prerequisites satisfy scheduler agreement/readiness, not proof of tests or artifact correctness. The agreements/artifacts view reads published workspace contracts through the fixed authenticated list operation. It shows the selected message’s pin alongside the published current revision, endpoint/schema version/excerpt, workspace/worktree, owned files, acceptance criteria, artifact metadata, dependency pins and compatibility changes. `n/p` selects a contract and `j/k` scrolls every declared field; `r` refreshes. These are declarations, never proof files exist or tests passed; the inspector does not open/execute artifacts or change acknowledgements. Backend list bounds fail explicitly instead of presenting an incomplete contract inventory. Escape returns to correspondence.

`x` requests cancellation using the selected message's current revision. Only
outgoing queued/working/blocked requests qualify. Conflicts are shown without
inventing a local successful state. Cancellation does not undo existing effects.
`y` copies an outgoing blocked/failed/cancelled text request to a review draft;
it never sends automatically. Recipient, conversation, task and the original
token/USD limits, prerequisites and contract pins are locked to retain the original usage scope and accumulated
usage. Earlier work may already have effects, and blocked work can require
review. Discard and start a new request to intentionally create a new scope.
An ambiguous send failure retains the exact draft and idempotency key; editing
the text creates a new key. Structured model requests cannot be copied into this
text-only composer.

Changing chats and unmounting abort transports and discard stale responses,
inspection, and drafts. Auth headers remain in the backend client. The panel
never accepts a sender identity from text or a request payload.

Validation is `npm run typecheck`, `npm run build`, then `npm run test:build`
inside `tui` with Node 20 or later. Coordination tests exercise the compiled
client over a local HTTP RPC fixture and drive real Ink keyboard input, scope
preservation, prerequisite and contract pin validation, graph transport and rendering, conflicts, ambiguous sends, pagination and late-session results.
They do not establish paid-provider behavior. The separate controlled native coding acceptance in `docs/local-interactive-coding-acceptance.md` exercises autonomous frontend/backend tool calls and actual artifacts. Contract authoring and full transcript browsing remain outside this terminal panel; Desktop has separate contract authoring. Pending model questions use the app’s explicit clarification dialog with immutable question identity and cancel; runtime waiting-for-input presence is separate from registration. See `docs/coordination-contract-terminal-audit.md` for clause-by-clause receipts and limitations.

Repository worktrees are an explicit registration option. On the registration
review screen, press `s` to switch from this checkout to repository worktrees,
then Enter to submit. The saved chat must point at a Git checkout root. Each
participant opts in independently; an existing identity cannot change scope.
Messages and agreement gates share the repository project while the receiving
agent continues to use its own checkout folder.
