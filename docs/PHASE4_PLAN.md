# Phase 4 Execution Plan

**Status when planning started:** Phase 0/1/2/3 ✅, Phase 4 ⚠️ partial.

This doc is the spec + dispatch order for Phase 4. Each batch has:
- **Goal** — one-sentence outcome
- **Surfaces** — files/packages it lives in
- **Tests** — what proves it's done
- **Depends on** — earlier batch IDs
- **Estimated size** — S (≤200 LOC), M (200–600), L (600+), XL (1000+)

## Guiding principles

1. **Daemon-first**: every async / scheduled feature needs the `overkill daemon` process. Build it first so downstream batches inherit a real runtime, not a stub.
2. **Smallest viable for each gateway**: ship Telegram + Discord before WhatsApp. Baileys is a port-or-wrap project worth its own week.
3. **No subagent delegation for the lift**: prior session burns showed subagents miscall orphans + invent function names. Do the core work in main thread with TaskCreate progress tracking. Reserve subagents for clearly-scoped audits (e.g. "find every shell call that bypasses the scanner").
4. **Tests up front**: every batch starts with the test file. Implementation lands when tests fail in the expected shape.

---

## Execution Order

### Batch A — Daemon foundation  (M)

**Goal:** `overkill daemon start` runs a persistent process that owns
the alarm + cron + task scheduler. Survives crashes via BadgerDB state.

**Surfaces:**
- `cmd/overkill/daemon.go` — Cobra command (`start`, `stop`, `status`)
- `internal/daemon/runtime.go` — lifecycle manager
- `internal/daemon/socket.go` — UNIX socket for the TUI to send work
- `~/.overkill/daemon.sock` — well-known socket path
- `~/.overkill/daemon.pid` — pidfile for start/stop semantics

**Tests:**
- Start → status reports running, pidfile exists
- Start twice → second invocation rejects with clear "already running"
- SIGTERM → clean shutdown, pidfile removed
- TUI can dial socket and send a ping

**Depends on:** none

**Open question for user:** systemd unit file generation? Or document
the systemd snippet in README and let user create their own? Default
plan: ship a `~/.overkill/systemd-unit.template` and `overkill daemon
install` that prints copy-paste-able systemd content.

---

### Batch B — Background Task Ledger sweeper  (S)

**Goal:** the existing Task struct gets a 60-second reconciler that
marks runtime-missing tasks as `lost` after a 5-min grace.

**Surfaces:**
- `internal/automation/ledger.go` — sweeper goroutine, lifecycle gating
- `internal/automation/ledger_test.go`

**Tests:**
- Task `running` with last-heartbeat > 5min + no live runtime → `lost`
- Task `running` with last-heartbeat < 5min → unchanged
- Task `succeeded`/`failed` → never touched
- Sweeper stops when ctx canceled

**Depends on:** Batch A (ledger lives in daemon)

---

### Batch C — Alarm Clock dispatch  (M)

**Goal:** `agent.SetAlarm(when, prompt)` schedules a one-shot timer.
On fire, a cheap sub-agent processes the prompt; if there's real work
it runs, otherwise it exits without burning a turn on the main model.

**Surfaces:**
- `internal/automation/alarms.go` — persistence + fire logic (timer
  loop in the daemon, NOT in-process timers — those die with the TUI)
- `internal/tools/alarm.go` — `alarm_set`, `alarm_list`, `alarm_cancel`
- `internal/agent/alarm_dispatch.go` — sub-agent factory for fired alarms

**Tests:**
- Set alarm, advance clock past target → sub-agent fires once
- Set alarm + cancel before fire → never fires
- Crash daemon, restart → pending alarms reload from BadgerDB
- Sub-agent quick-exits when prompt has no actionable verb

**Depends on:** Batch A (daemon owns the timer wheel), Batch B (ledger
records each alarm execution)

---

### Batch D — Task Flow (durable multi-step resume)  (L)

**Goal:** a task that hits `max_steps` mid-execution saves state and
flips to `timed_out`. A follow-up alarm wakes a sub-agent that
re-loads state and continues from the last completed step.

**Surfaces:**
- `internal/agent/flow.go` — checkpoint state machine
- `internal/agent/flow_test.go`
- Hook into `stream.go` exit path when `step == maxSteps`

**Tests:**
- Multi-step task hits limit → state saved, status `timed_out`
- Resume → sub-agent picks up at step N+1, completes
- State corrupt → graceful abort with clear error, no retry loop
- Two alarms fire concurrently for same flow → mutex-protected, only
  one wins

**Depends on:** Batch B + C

---

### Batch E — Emergency controls  (S)

**Goal:** `overkill estop` halts every running task immediately;
tool receipts give an audit chain.

**Surfaces:**
- `cmd/overkill/estop.go` — CLI command
- `internal/automation/estop.go` — broadcast halt via daemon socket
- `internal/agent/receipts.go` — cryptographic per-tool-call ledger

**Tests:**
- `estop` while a task is running → task aborts within 1s
- Receipt chain verifies under tampering (hash mismatch → fail)
- `estop` with no running tasks → no-op, exit 0

**Depends on:** Batch A

---

### Batch F — Auto-update  (M)

**Goal:** `overkill update` fetches the latest release binary, verifies
checksum + signature, atomically swaps. Non-blocking check on launch.

**Surfaces:**
- `cmd/overkill/update.go`
- `internal/update/checker.go` — version check + GitHub Releases query
- `internal/update/installer.go` — atomic swap with rollback

**Tests:**
- Newer version available → toast + offer to update
- Bad checksum → reject + keep current binary
- Update interrupted mid-swap → rollback to original (atomic rename)
- Already on latest → silent no-op

**Depends on:** none (independent of automation work)

---

### Batch G — Telegram gateway  (M)

**Goal:** users can chat with Overkill through Telegram. Cross-channel
session continuity for the same user.

**Surfaces:**
- `internal/gateway/telegram/` — bot polling (long poll, no webhook
  exposure required)
- `internal/gateway/router.go` — already partially built for Slack

**Tests:**
- Bot token → polling loop receives message
- Message → spawns/resumes session for that telegram-user-id
- Image upload → routes to vision-capable model (uses existing
  attachment pipeline)
- Bookmark via `/bm` slash command → session bookmark stored

**Depends on:** Batch A (daemon hosts the polling goroutine)

---

### Batch H — Discord gateway  (M)

**Goal:** same as Telegram, for Discord.

**Surfaces:**
- `internal/gateway/discord/` — WebSocket gateway via discordgo
- Reuse `internal/gateway/router.go`

**Tests:** same shape as Telegram

**Depends on:** Batch A

---

### Batch I — Understand-anything  (M)

**Goal:** PDF/DOCX/audio/video → routed to the right tool, surfaced
as text + key metadata into the agent context. Never "I can't handle
this file."

**Surfaces:**
- `internal/multimodal/detect.go` — MIME + magic-byte routing
- `internal/multimodal/pdf.go` — pdftotext shell-out (text extraction)
- `internal/multimodal/audio.go` — Whisper-cpp shell-out (transcription)
- `internal/multimodal/router.go` — picks tool by content type
- `internal/tools/understand.go` — new tool exposed to the agent

**Tests:**
- Drop a PDF → tool returns text + page count
- Drop a WAV → tool returns transcript
- Unknown binary → tool returns "unable to extract" not crash
- Tool registered with vision/transcription capability flags

**Depends on:** none (independent layer)

---

### Batch J — dev-browser  (L)

**Goal:** sandboxed AI-safe browser. QuickJS WASM, persistent named
pages, `snapshotForAI()` returning structured page content.

**Surfaces:**
- `internal/browser/devbrowser/` — port of SawyerHood's dev-browser
- Tools: `browser_open`, `browser_snapshot`, `browser_click`,
  `browser_type`

**Tests:**
- Open page → snapshot returns title + readable text
- Click selector → page state changes, next snapshot reflects it
- Sandboxed: no filesystem access, no network beyond the target page
- Named pages persist across tool calls within a session

**Depends on:** none (Playwright is the comparison; dev-browser is the
"safe for auto-approve" alternative)

---

### Batch K — Stagehand  **DROPPED**

Skipped per 2026-05-13 decision. Stagehand wraps Browserbase, a paid
SaaS for cloud-hosted browsers. Capability overlaps Playwright +
dev-browser. Revisit if a user explicitly asks.

---

### Batch L — WhatsApp (whatsmeow + Cloud API)  (XL)

**Goal:** WhatsApp gateway with two backends behind one router.

**Surfaces:**
- `internal/gateway/whatsapp/router.go` — backend-agnostic entry point
- `internal/gateway/whatsapp/whatsmeow/` — Go-native unofficial client
  (go.mau.fi/whatsmeow). QR-pairing on first run; SQLite session store.
- `internal/gateway/whatsapp/cloud/` — Meta Business Cloud API client.
  Webhook receiver + send-message HTTP client.
- Config switch: `gateway.whatsapp.backend = "whatsmeow"|"cloud"`

**Tests:**
- whatsmeow: QR code pairing flow → device added
- whatsmeow: receive message → route to session
- cloud: webhook signature verification (HMAC-SHA256 with app secret)
- cloud: send message → 200 OK + message_id captured for delivery ack
- cloud: 24h window enforcement — outside window, falls back to
  approved template or surfaces clear error
- Image attachments: both backends encode + dispatch
- Backend swap via config → no code changes, only router lookup flips

**Depends on:** Batch A

**Risk notes:**
- whatsmeow: same TOS posture as Baileys (occasional bans), personal
  use only. NOT for productizing.
- Cloud API: requires Meta Business verification + phone number
  approval (1-2 weeks). Document the onboarding in
  `docs/gateways-whatsapp.md`.

---

## Recommended sequencing

```
A ─┬─ B ─┬─ C ─┬─ D
   │     │     │
   └─ E  │     │
         │     │
F (parallel — no automation deps)
I (parallel — no automation deps)
G,H (need A done)
J,K (parallel — independent)
L (after A, last because Baileys is the biggest risk)
```

**Suggested cut points for review:**

- **After A+B+C** — alarm clocks work end-to-end. Killer demo: "wake me
  when the build finishes."
- **After D+E** — daemon story is complete. Estop + task resume cover
  the failure modes.
- **After F** — `overkill update` ships, no-deps work done.
- **After G+H+I** — Telegram + Discord + understand-anything. Most of
  the user-visible Phase 4 value.
- **After J+K** — browser story is complete (Playwright + dev-browser
  + Stagehand).
- **After L** — Phase 4 done.

## Decisions (locked in 2026-05-13)

1. **Daemon UX:** print-and-copy. We ship `~/.overkill/systemd-unit.template`
   and `overkill daemon install` prints the unit content; user wires it.
2. **WhatsApp:** ship two backends, user picks at config:
   - **`whatsmeow`** (go.mau.fi/whatsmeow) — Go-native, no Node sidecar.
     Strict upgrade over Baileys-via-Node: same TOS posture, less
     operational complexity. Personal use.
   - **WhatsApp Business Cloud API** (Meta official) — production path.
     24h messaging window, template messages for outside that window,
     no ban risk, paid per conversation at scale.
   - Batch L scope changes: replace single Baileys impl with both
     backends behind a `gateway.whatsapp.backend = "whatsmeow"|"cloud"`
     config switch.
3. **Stagehand:** **skip** — overlaps Playwright + dev-browser and
   forces a Browserbase SaaS dependency. Revisit if users ask.
   Batch K is dropped.
4. **Order:** A→B→C→D→E in series. Confirmed.
