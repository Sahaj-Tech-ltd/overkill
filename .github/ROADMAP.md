# Roadmap

> **Last updated:** June 1, 2026
> **Current version:** v0.10 (beta)
>
> Overkill moves fast. This document tracks what's next. Everything with a date is committed. Priorities shift with user feedback — the [CHANGELOG](CHANGELOG.md) is the source of truth for what shipped.

---

## v0.10 — Beta Launch 🚀 *(June 2026)*

The line in the sand. Public beta, polished onboarding, real docs.

| What | Target |
|---|---|
| **Onboarding wizard v2** — one provider, one key, one model, go | June 7 |
| **docs.overkill.my** — searchable, every command documented | June 10 |
| **Task dashboard** — web UI showing active sessions, cron jobs, sub-agent status, cost breakdown | June 14 |
| **Bug bash** — close all HIGH severity items | June 10 |
| **Windows native support** — PowerShell installer, pre-built `.exe`, TUI on Windows Terminal | June 12 |
| **Homebrew tap + npm global install** — reach non-Go users | June 14 |
| **Beta release cut** — `v0.10.0-beta` tag, signed binaries, release notes | June 16 |

**Beta success criteria:** 100 active installs in first week, <5 critical bugs, one community-contributed skill.

---

## v0.11 — Mobile + Multi-Device *(July 2026)*

Take Overkill out of the terminal.

| What | Target | Notes |
|---|---|---|
| **Overkill mobile app** — iOS + Android via React Native | July 14 | Connect to your running daemon. Chat, approve permissions, check task status from your phone. |
| **Push notifications** — cron job complete, sub-agent done, alert fired | July 14 | APNs + FCM, configurable per notification type |
| **Session sync v2** — seamless handoff between TUI, mobile, and messaging channels | July 21 | Zero-config cross-device context |
| **Mobile-specific UX** — native permission dialogs, camera roll attachments, voice-to-prompt | July 21 | |
| **`overkill connect`** — pair mobile app to daemon via QR code | July 14 | |

---

## v0.12 — Collaboration + Scale *(August 2026)*

| What | Notes |
|---|---|
| **Shared sessions** — invite another dev into your session, agent sees both contexts | |
| **Team configs** — `.overkill/` in repo root, shared skills and hooks per-project | |
| **Usage analytics dashboard** — per-project cost breakdown, model routing efficiency, compaction savings | |
| **Plugin marketplace v1** — browse and install community plugins from TUI | |
| **Post-quantum encryption** — lattice-based key exchange (CRYSTALS-Kyber) + hybrid TLS for all gateway channels, agent-to-agent communication, and config-at-rest | |

---

## Phase 5 — Research & R&D *(Backlog)*

Aspirational. No dates. Shipped when the research is solid and the implementation is clean.

| What | Source |
|---|---|
| **Cartridge-style KV compaction** (50x ratio) | Eyuboglu 2025 |
| **Neural garbage collection** for context | Li 2026 |
| **Cross-session task graph** — "What did we ship 3 days ago?" | — |
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

Overkill is built in the open:

- **Bug reports:** Issues tab. Template included.
- **Skills:** Write a `SKILL.md`, open a PR. Community skills ship into `optional-skills/`.
- **Core:** [CONTRIBUTING.md](CONTRIBUTING.md) has the full process. Conventional Commits. One concern per PR.
- **Ideas:** Discord or Discussions. The roadmap is dictated by what users actually need.
