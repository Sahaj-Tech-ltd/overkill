# Clarification and turn cancellation

The desktop now displays the selected chat’s pending `ask_user` question with
choices or free text, plus an explicit cancel action. The actual model catalogue
advertises its question/choices schema. Input sizes and choice counts are bounded;
malformed input is rejected before the bridge. Public prompts contain no private
transport credential.

Each API question has a fresh opaque UUID. `clarify.poll` with `session_id`
returns that question’s identity, prompt and choices without consuming it.
`agent.answer` requires the same `session_id`, `question_id`, `text`, `index`
and optional explicit `cancel`. A chosen index must match its choice; `-1`
means free text. Empty free text is rejected. Cancellation uses empty text,
index `-1` and `cancel:true`. The answer consumes that generation once;
duplicate, stale and cross-session answers fail. A concurrent second question
cannot overwrite the first. Cancelling its execution context removes the wait.

The desktop uses fixed typed IPC operations, safe text DOM and per-chat/question
drafts. Polls do not reset an open draft; stale asynchronous results cannot
answer another question. The terminal captures the polled identity in its
callback and sends exactly one cancellation. Older API clients must include
`question_id`; the bundled terminal has been updated.

Clarification does not hold a project ownership transaction while waiting for
a human. Another peer can publish its independent ownership contract during
that wait. This is a human question, not a tool-permission authorization or
proof that pending dependencies have passed acceptance.

## Stop this response

`agent.abort` cancels the current admitted session operation through an
operation-owned context. RPC, streaming and coordination execution bind their
own cancellation generation. Delayed cleanup cannot remove a later operation’s
binding. Persistence remains bounded and keeps admission until the commit
completes; cancellation cannot undo an earlier tool effect.

The original handler permanently called `Agent.EStop()`, so the next turn of
that chat could not run. The new native coding fixture reproduced that failure
(`.cache/interactive-stop-before.log`). Real provider RPC and streaming
regressions prove cancellation releases the current wait and permits the next
turn (`.cache/interaction-abort-focused.log`). A session with no current
operation reports idle. This per-turn operation does not stop other agents or
prevent a later queued request from starting.

## Stop all agents in this app

The desktop’s separate stop-all control synchronously refuses new work, drains
its owned backend/streams/dispatcher and database, and writes a private stop
marker. It verifies that the owned PostgreSQL postmaster is gone before claiming
success. Persistence/drain failures remain explicit, and work is still drained
when writing the marker fails. The marker keeps automatic startup stopped even
after closing and reopening the app. Only an explicit successful Provider
settings reconnect clears it; queued requests may then resume. External servers,
other applications and earlier side effects remain outside this operation.

The controlled native coding scenario covers answer, stale ID rejection,
explicit cancel, stop→next-turn, two active agents plus queued work during
stop-all, owned database shutdown, whole-app relaunch while stopped and
intentional reconnect. These are local controlled-provider fixtures; real
Keychain, paid-provider and full-platform release acceptance remain separate.
