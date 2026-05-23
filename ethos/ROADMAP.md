# Roadmap

> **Last updated:** May 14, 2026
> **Current version:** 0.9.0 (pre-alpha)
> **Active installs:** ~340 and climbing
>
> Overkill moves fast. This document tracks what's shipped, what's in flight, and what's next.
> Everything with a date is committed. Everything else is planned — priorities shift with user feedback.

---

## Now — v0.9 (pre-alpha) ✅

Shipped and stable. The core agent is real.

| What | Status |
|---|---|
| **ReAct agent loop** with dual dispatch (Complete + Stream) | ✅ |
| **Bubble Tea TUI** — split panes, markdown, themes, keybindings | ✅ |
| **13 LLM providers** — OpenAI, Anthropic, Gemini, DeepSeek, z.ai, Ollama, OpenRouter + compat layer | ✅ |
| **LCM compaction** — three-level escalation, context stays lean | ✅ |
| **Security plane** — injection detection, command scanning, path traversal blocking, permission escalation dialogs | ✅ |
| **Model routing** — complexity classifier, pricing-aware fallback, family-aware selection | ✅ |
| **Personality engine** — advisor framing, relationship arc, two-layer style model, frustration detection, proactive transparency | ✅ |
| **Sub-agent system** — parallel workstreams, delegation ledger, cross-agent fault attribution | ✅ |
| **BadgerDB everywhere** — sessions, memory, cron, automation, journal. Embedded, pure Go, no CGo | ✅ |
| **Python bridge** — gRPC embeddings, reranking, memory, compaction | ✅ |
| **Journal + flight recorder** — append-only JSONL, LLM summarizer, structured observations, alerts | ✅ |
| **Cron scheduler** — timezone-aware, 4 execution styles, BadgerDB persistence | ✅ |
| **Gateway daemon** — Telegram, Discord, WhatsApp (cloud + whatsmeow), cross-channel sessions | ✅ |
| **Agentic browser** — Playwright tool + dev-browser sandbox with snapshotForAI() | ✅ |
| **Multimodal** — PDF, DOCX, audio, images through to vision models | ✅ |
| **3 Walls quality gates** — adversarial review, architecture context, behavioral regression bank | ✅ |
| **Self-update** — GitHub releases, atomic rename, SHA-256 verification, rollback | ✅ |

---

## v0.10 — Beta Launch 🚀 *(June 2026)*

The line in the sand. Public beta, polished onboarding, real docs.

| What | Target | Owner |
|---|---|---|
| **Onboarding wizard v2** — one provider, one key, one model, go | June 7 | @harsh |
| **docs.overkill.dev** — mdBook, searchable, every command documented | June 10 | docs team |
| **Task dashboard** — web UI at `~/.overkill/dashboard/` showing active sessions, cron jobs, sub-agent status, cost breakdown | June 14 | @harsh |
| **Bug bash** — close all HIGH severity items from `butterbugs.md` | June 10 | core |
| **Homebrew tap + npm global install** — reach non-Go users | June 14 | infra |
| **Beta release cut** — `v0.10.0-beta` tag, signed binaries, release notes | June 16 | release |
| **Community Discord** — #help, #showcase, #skills channels | June 16 | community |

**Beta success criteria:** 100 active installs in first week, <5 critical bugs, one community-contributed skill.

---

## v0.11 — Mobile + Multi-Device *(July 2026)*

Take Overkill out of the terminal.

| What | Target | Notes |
|---|---|---|
| **Overkill mobile app** — iOS + Android via React Native | July 14 | Connect to your running daemon. Chat, approve permissions, check task status from your phone. Same session context as TUI. |
| **Push notifications** — cron job complete, sub-agent done, alert fired | July 14 | APNs + FCM, configurable per notification type |
| **Session sync v2** — seamless handoff between TUI, mobile, and messaging channels | July 21 | Already have cross-channel sessions via gateways. This makes it zero-config. |
| **Mobile-specific UX** — permission approvals as native dialogs, file attachment from camera roll, voice-to-prompt | July 21 | |
| **`overkill connect`** — pair mobile app to daemon via QR code | July 14 | |

---

## v0.12 — Collaboration + Scale *(August 2026)*

| What | Notes |
|---|---|
| **Shared sessions** — invite another dev into your session, agent sees both | |
| **Team configs** — `.overkill/` in repo root, shared skills and hooks per-project | |
| **Usage analytics dashboard** — per-project cost breakdown, model routing efficiency, compaction savings | |
| **Plugin marketplace v1** — browse and install community plugins from TUI | |

---

## Phase 5 — Research & R&D *(Backlog)*

Aspirational. No dates. Shipped when the research is solid and the implementation is clean.

| What | Paper / Source |
|---|---|
| **Cartridge-style KV compaction** (50x ratio) | Eyuboglu 2025 |
| **Neural garbage collection** for context | Li 2026 |
| **Cross-session task graph** | "What did we ship 3 days ago?" |
| **LATS tree search** for multi-path code exploration | Zhou 2024 |
| **RL-based self-improvement** (credit assignment across 100+ turn trajectories) | Zhang 2026, Shinn 2023 |

---

## How We Prioritize

1. **User pain.** If 10 people file the same issue, it jumps the queue.
2. **Token economics.** Features that save API budget ship before features that burn it.
3. **Completion over novelty.** Polish what exists before adding what doesn't.
4. **The plan is a bet, not a promise.** Dates shift. Priorities shift. The [CHANGELOG](CHANGELOG.md) is the source of truth.

---

## Contribute

Overkill is built in the open, mostly by vibe-coding agents and a human who reviews their diffs. If you want in:

- **Bug reports:** Issues tab. Template included.
- **Skills:** Write a `SKILL.md`, open a PR. We ship community skills into `optional-skills/`.
- **Core:** [CONTRIBUTING.md](CONTRIBUTING.md) has the full process. Conventional Commits. One concern per PR.
- **Ideas:** Discord or Discussions. The roadmap above is dictated by what users actually need.

---

*340 installs and counting. This thing ships fast because the feedback loop is tight. Tell us what sucks.*
