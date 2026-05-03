# Ethos Master Audit — 2026-05-03

Comparison of the current `internal/`, `cmd/`, `pkg/`, `bridge/`, `skills/` tree
against `~/.local/share/opencode/plans/ethos-master-plan.md` (1607 lines,
Phases 0–5) and the `inspiration/` competitor folder. Supersedes
`docs/AUDIT-2026-05-03.md`, which only covered `internal/` wire-up.

> Verification: file paths and line numbers are repo-relative to
> `/home/harsh/docker/ethos/`. Inspiration references are
> `inspiration/<repo>/<path>`. Master plan sections cited by their own number.

---

## 1. Executive Summary

| Metric | Count |
|---|---:|
| Numbered subsections in master plan (3.1 → 8.6) | ~95 (including narrative) |
| Concrete deliverables tracked below | 78 |
| **SHIPPED** end-to-end | 18 |
| **WIRED-NOT-VERIFIED** (called from real entry points; no spec test) | 14 |
| **SCAFFOLDED** (code exists, never called from live system) | 22 |
| **ORPHAN** (only callers are tests) | 9 |
| **PARTIAL** | 8 |
| **MISSING** | 7 |
| **DEFERRED** (Phase 4/5 explicitly later) | 0 marked complete |

**Top inspiration gems ethos has NOT taken yet:**

1. **claude-mem CLAIM-CONFIRM async observation queue with self-healing crashed-worker recovery** — `inspiration/claude-mem/openclaw/src/index.ts`. Ethos has `internal/journal/summarizer.go` (89 lines) but no async pipeline, no crash recovery, no content-hash dedup. Plan §4.19 calls for this exactly.
2. **dev-browser `snapshotForAI()` + sandboxed QuickJS browser** — `inspiration/dev-browser/cli/src/`, `inspiration/dev-browser/daemon/src/`. Plan §7.3 calls for it. Ethos `internal/browser/` exists but does not wrap dev-browser; no `snapshotForAI` equivalent.
3. **Matt Pocock vertical-slice + diagnose feedback-loop-first skill** — `inspiration/mattpocock-skills/skills/engineering/{diagnose,to-issues,to-prd,grill-with-docs,improve-codebase-architecture,tdd,zoom-out}/`. Ethos has `internal/pipeline/slicer.go` (310 lines) and `internal/diagnostic/analyzer.go` (238 lines) but neither implements the **10-tier feedback-loop escalation** nor the HITL/AFK classification format. The skills are the gem; ethos has skeletons of the engines but no operating playbooks.

Bonus mention: **understand-anything plugin's nine sub-skills + nine specialized agents** (`inspiration/understand-anything/understand-anything-plugin/skills/` & `agents/`) — directly maps to plan §4.18 introspection + §6.4 understand-anything bundled skill. Ethos has both folders empty (`skills/understand-anything/` is `0 bytes`).

---

## 2. Master Plan — Section-by-Section Status

### Phase 0: Foundation (§3)

| Section | Title | Status | Evidence | Gap |
|---|---|---|---|---|
| 3.1 | `.github/` directory | SHIPPED | `.github/{ISSUE_TEMPLATE,workflows,labeler.yml,dependabot.yml,CODEOWNERS,pull_request_template.md}` all present (workflows: `ci.yml lint.yml security.yml codeql.yml docker-publish.yml contributors.yml release.yml labeler.yml`) | `tests.yml` named `ci.yml`; `supply-chain-audit.yml` not present — fold into security.yml or add |
| 3.2 | Root community files | SHIPPED | `CONTRIBUTING.md`, `SECURITY.md`, `AGENTS.md`, `LICENSE-MIT`, `LICENSE-APACHE` exist | none |
| 3.3 | README.md | WIRED-NOT-VERIFIED | `README.md` present | did not verify ASCII art / contributor grid / comparison table; needs visual inspection |
| 3.4 | Inspiration folder (gitignored, shallow) | SHIPPED | 15 subdirs present in `inspiration/` (claude-mem, deep-wiki, dev-browser, dive-into-claude-code, hermes-agent, mattpocock-skills, models.dev, openclaude, openclaw, opencode, opentui, picoclaw, rtk, understand-anything, zeroclaw) | none |
| 3.5 | 47 research papers | claim unverified | not searched for `research/papers/` PDFs in this pass | run `ls research/papers \| wc -l` |

### Phase 1: MVP (§4)

| Section | Title | Status | Evidence | Gap |
|---|---|---|---|---|
| 4.1 | Core agent loop (ReAct) | SHIPPED | `internal/agent/{agent.go,react.go,stream.go,prompt.go}`; `wiring_test.go` covers forethought/budget/recovery/confidence/bus | none for the loop itself |
| 4.1 | Two-Step Forethought | SHIPPED | `internal/agent/forethought.go` invoked at `react.go:104` and `stream.go:187` per prior audit | none |
| 4.1 | Spec-Driven mode | SHIPPED | `internal/agent/specdriver.go` + `specdriver_test.go` | confirm auto-switch trigger; appears wired |
| 4.1 | Command completion marker `__ETHOS_DONE__` | claim unverified | did not grep | check `internal/tools/shell.go` |
| 4.1 | Steering queue | SHIPPED | `internal/agent/steering.go` + tests | none |
| 4.1 | EventBus | SHIPPED | `internal/agent/eventbus.go` + `Agent.Bus()` | none |
| 4.1 | Mentions / `@file` | SHIPPED | `internal/agent/mentions.go` + tests | none |
| 4.2 | Provider interface | SHIPPED | `internal/providers/` 58 callers; protocol-family grouping in catalog | none |
| 4.2 | Provider impls (OpenAI/Anthropic/Gemini/DeepSeek/Ollama/OpenRouter etc.) | WIRED-NOT-VERIFIED | `models/{anthropic,deepseek,google,groq,mistral,ollama,openai,openrouter,together-ai,xai}/` TOML catalog present | did not verify each adapter compiles & passes live; z.ai/GLM, MiniMax not visible |
| 4.2 | Provider selection UI (ZeroClaw style) | PARTIAL | `cmd/ethos/setup.go` exists | did not confirm the "coding plan vs API + custom endpoint" prompt flow |
| 4.2 | Model catalog as TOML (filename-as-ID, extends, family, cap-flags, fine-grained cost) | SHIPPED | `internal/providers/catalog.go:15` (Family field), `catalog.go:339` `ByFamily()`; per-vendor TOML in `models/<vendor>/models/` | claim unverified: `extends` inheritance + tiered (>200K) pricing — needs spot-check |
| 4.2 | Failover chain w/ cooldown | claim unverified | not grepped explicitly | per prior audit assumed wired in providers |
| 4.2 | Retry: 8x exponential, 20% jitter, honor Retry-After | claim unverified | check `internal/providers/` for backoff |
| 4.3 | Security plane / scanner | SHIPPED | `internal/security/scanner.go` wired in `cmd/ethos/tui.go:248` per prior audit; `permission.go` gated through `agent.approvalCheck` |
| 4.3 | 35+ deny patterns | claim unverified | grep `internal/security/` for regex count |
| 4.3 | Permission escalation (allow once / project / global) | WIRED-NOT-VERIFIED | permission dialog exists in TUI; cascade present | confirm the 3-button UX matches plan §4.3 |
| 4.3 | Pre-commit before destructive ops | claim unverified | not grepped |
| 4.3 | Secret scanner before push | claim unverified |  | search `cmd/ethos/`, no `git push` hook seen |
| 4.3 | Privilege separation (paper #21) | MISSING | no separate reader-vs-action agent split found | new layer needed |
| 4.4 | LCM dual-state memory + DAG + 3-level escalation | SCAFFOLDED | `internal/compaction/lcm.go` (171 lines), `compactor.go` (67) | Not consumed: only caller is `config/validate.go` field; agent's compact path bypasses LCM. Wire into `agent.go:472` compact request flow. |
| 4.4 | Tool output compression middleware (RTK pattern) | MISSING | no `internal/tools/compressor*` or per-tool compressor registry | new package needed |
| 4.4 | LLM-based prompt compression (LLMLingua) | SCAFFOLDED | `internal/compaction/prompt_compress.go` (106 lines) + tests | provider middleware never invokes it; no `cfg.Compaction.PromptCompress` consumer |
| 4.4 | 50% trigger / pre-compaction checkpoint / 95% hard | PARTIAL | `internal/agent/budget.go` emits `budget_warning` but does not trigger compaction proactively | wire warn → auto-compact at τ_hard |
| 4.4 | Caveman Mode | MISSING | no caveman mode toggle in `internal/agent/prompt.go` | add prompt mutation when budget > threshold |
| 4.4 | Large file >25K → disk reference | claim unverified |  | check `internal/tools/fs.go` |
| 4.5 | Token counting | SHIPPED | `internal/tokenizer/` 9 callers |
| 4.5 | Cost tracking 4-field (in/out/in-cached/out-cached) | SHIPPED | `internal/cost/` |
| 4.5 | `/usage` command | SHIPPED | `cmd/ethos/usage.go` |
| 4.5 | 5-hour rolling limit | claim unverified | grep for `5h` rolling logic |
| 4.5 | Status bar Context/Cost % w/ >80% warn | WIRED-NOT-VERIFIED | `pkg/tui/components/status/statusbar.go` referenced; visual confirmation needed |
| 4.6 | One session per folder | SHIPPED | `internal/session/` 18 callers |
| 4.6 | `/session` switch | SHIPPED | command id `sessions` registered at `pkg/tui/tui.go:578` |
| 4.6 | Auto-generate session title (cheap model, 80 max tokens) | claim unverified | grep `GenerateTitle` |
| 4.6 | Sub-sessions w/ parent_session_id | PARTIAL | `internal/subagent/` exists but linkage to session model unconfirmed |
| 4.7 | Config TOML, versioning, migration | SHIPPED | `internal/config/` 30 callers; `internal/config/load.go` migration logic |
| 4.7 | Profile-scoped credentials | claim unverified |  |
| 4.7 | Start even with broken config (graceful) | claim unverified |  |
| 4.7 | `doctor` command | WIRED-NOT-VERIFIED | `cmd/ethos/doctor.go:42` `doctor.NewRunner()`; richer checks in `internal/doctor/checks/` exist | confirm `--check-db` BadgerDB integrity check exists (§4.20) |
| 4.8 | Fancy git push preview | MISSING | `internal/diff/` exists (1 caller `gitpreview`) but no ASCII pre-push window in `cmd/` |
| 4.8 | Religious commits per stage | MISSING | no commit-on-stage automation found |
| 4.8 | `git reset --hard` safety valve | MISSING | no /rollback or rollback skill found |
| 4.8 | Filesystem checkpoints before destructive ops | MISSING |  |
| 4.9 | Cobra CLI | SHIPPED | `cmd/ethos/root.go`, all subcommands present |
| 4.9 | Streaming output | SHIPPED | `internal/agent/stream.go` |
| 4.9 | Interrupt/redirect mid-task | WIRED-NOT-VERIFIED | steering queue exists; UI integration unconfirmed |
| 4.10 | Prompt rewriter middleware | SCAFFOLDED | `internal/rewriter/{rewriter.go,middleware.go,llm_rewriter.go,sycophancy.go}` 510 lines + 457-line test | **Zero external callers**. Not in agent loop. Plan flags as MVP. |
| 4.11 | Repo onboarding / GitIngest / PRP | MISSING | no `internal/onboard/` or PRP generator found; `understand-anything` skill folder empty |
| 4.11 | Incremental pipeline (4-stage) | SCAFFOLDED | `internal/pipeline/{pipeline.go,executor.go,prompts.go,slicer.go}` 521 lines | No external callers. No `/slice` or `/pipeline` slash command. |
| 4.11 | Vertical slice + HITL/AFK | SCAFFOLDED | `internal/pipeline/slicer.go` (310 lines) | unwired |
| 4.12 | Independent Test Agent (Spider-Man) | SHIPPED-IN-PKG / PARTIAL | `internal/agent/testagent.go` + test exists | confirm spec-isolation invariant; wired into pipeline? |
| 4.13 | Debugging diagnostic report | SCAFFOLDED | `internal/diagnostic/{analyzer.go,fileanalyzer.go,report.go,diagnostic.go}` 566 lines + 573-line test | No callers outside tests. Plan §4.13 wants 10-tier feedback-loop escalation; only basic likelihood scoring present. |
| 4.14 | Self-aware error recovery | SHIPPED | `internal/agent/recovery.go` wired via `emitRecovery` (per prior audit). Lessons persisted via `JournalEntryWriter` |
| 4.15 | Confidence + honesty | SHIPPED | `internal/agent/confidence.go` attaches `*ConfidenceAssessment` to `RunResult.Confidence` |
| 4.16 | Personality engine (subtle/witty/full/off) | SHIPPED | `internal/personality/personality.go` (234) + tests; loaders in TUI |
| 4.16 | Frame as advisor (Kelley & Riedl) | claim unverified | check `internal/agent/prompt.go` system prompt content |
| 4.16 | Sycophancy reduction (Dubois reframe) | SCAFFOLDED | `internal/rewriter/sycophancy.go` (146) | rewriter middleware unwired (see 4.10) |
| 4.16 | Tone mirroring | claim unverified | `internal/personality/style.go` (250) — short-term/long-term layer present? |
| 4.16 | Two-layer style model (baseline vs short-term) | SHIPPED-IN-PKG | `internal/personality/style.go:` `style_test.go` 213 | confirm consumers in agent prompt construction |
| 4.16 | Frustration detection | PARTIAL | heuristic likely in `style.go`; alert type `AlertFrustration` exists in `journal.go:34` | not surfaced in TUI; AlertStore has zero external readers |
| 4.16 | Self-model file (read on boot) | SHIPPED | `internal/personality/soul.go` (90) wired via boot sequence |
| 4.16 | Model fingerprinting + competence recalibration | SHIPPED-IN-PKG | `internal/personality/fingerprint.go` (163) + 225-line test; plan-aligned | confirm runs on boot; not just tested |
| 4.16 | Proactive transparency (pre-exec failure warnings) | SHIPPED-IN-PKG | `internal/personality/transparency.go` (117) + test | confirm hooked before tool calls |
| 4.16 | Cognitive blind-spot detection | SHIPPED-IN-PKG | `internal/personality/blindspot.go` (68) + 147-line test | likely needs journal `pattern_detected` data; AlertStore unwired so this stays cold |
| 4.16 | Visible memory dashboard | MISSING | no TUI surface for relationship arc / live memory; SSE broadcast not implemented |
| 4.16 | Cold-start protocol | SHIPPED-IN-PKG | `internal/personality/coldstart.go` (221) + test | confirm runs on first session |
| 4.16 | Boot sequence (10 steps) | PARTIAL | `pkg/tui/boot.go` exists; soul/personality wired | "Hey, you're finally awake" pattern, fun fact, journal alert surfacing — confirm step-by-step |
| 4.16 | Fun-fact database | SHIPPED | `internal/personality/funfacts.go` (213) |
| 4.17 | Python bridge (gRPC) | SHIPPED scaffolding / SCAFFOLDED consumers | `bridge/{server.py,client.go,proto/,embeddings/,memory/,reranking/,compaction/}` complete | **No Go consumers** — `bridge.Client` only referenced from doctor's "is bridge running" health check. `internal/memory/orchestrator.go` does NOT call into bridge. |
| 4.18 | Introspection skill (on-demand) | SCAFFOLDED | `internal/introspection/{introspection.go,generators.go}` 200 lines + 330 test | No external callers. No `/introspect` slash command. `~/.ethos/introspection/` files not auto-generated. |
| 4.19 | Journal raw logs (flight recorder) | SHIPPED | `internal/journal/recorder.go` (154); wired in `cmd/ethos/tui.go:330` per prior audit |
| 4.19 | Journal sub-agent (daily summaries) | SCAFFOLDED | `internal/journal/summarizer.go` (89) | no scheduler; no session-exit hook fires it |
| 4.19 | Alerts surfaced in next session opener | SCAFFOLDED | `internal/journal/alerts.go` (`AlertStore`) | **Zero external callers**. `AlertCompactionSkip / TaskDeferred / PatternDetected / Frustration` types defined but never `.Create()`'d outside test. Not loaded by boot. |
| 4.19 | Journal query protocol (3-layer search, hybrid FTS + vector) | PARTIAL | `internal/journal/query.go` (44) + 159-line test | Only Layer 1 metadata search; no FTS; no vector hit (bridge unwired) |
| 4.19 | Structured observation types (typed records) | SHIPPED-IN-PKG | `internal/journal/observation.go` + test | confirm recorder writes typed entries |
| 4.19 | Idempotent storage (content-hash dedup) | claim unverified | grep `SHA256` in `internal/journal/` |
| 4.19 | CLAIM-CONFIRM async queue | MISSING | no async worker; summarizer is synchronous |
| 4.19 | Hook errors never block | claim unverified |  |
| 4.19 | SSE broadcast to dashboard | MISSING |  |
| 4.20 | BadgerDB snapshots (daily, 7 rolling) | MISSING | no `internal/snapshots/`; not in cron either |
| 4.20 | Export ritual (`memory-export.md`) | MISSING |  |
| 4.20 | Graceful degradation on corrupt DB | MISSING | no boot-time integrity check; `doctor --check-db` not present |

### Phase 2: TUI + Routing (§5)

| Section | Title | Status | Evidence | Gap |
|---|---|---|---|---|
| 5.1 | Bubble Tea TUI scaffold | SHIPPED | `pkg/tui/{app.go,tui.go,boot.go,page/,components/,layout/,theme/,styles/,animation/,cellrender/}` |
| 5.1 | Split-pane 70/30/10 layout | WIRED-NOT-VERIFIED | layout exists; visually unverified |
| 5.1 | Model picker Ctrl+O | SHIPPED | `pkg/tui/components/dialog/models.go` + test |
| 5.1 | Status bar (context/cost) | SHIPPED | `pkg/tui/components/status/statusbar.go` |
| 5.1 | Permission dialog | SHIPPED | `pkg/tui/components/dialog/permissions_ledger.go` |
| 5.1 | File completion `@` | claim unverified |  |
| 5.1 | Command palette Ctrl+K | SHIPPED | `pkg/tui/components/dialog/commands.go` (28 commands registered at `tui.go:570`) |
| 5.1 | LSP diagnostics in status bar | WIRED-NOT-VERIFIED | `internal/lsp/` 3 callers |
| 5.1 | Theme system (40 slots, Catppuccin) | WIRED-NOT-VERIFIED | `pkg/tui/theme/` exists |
| 5.1 | Markdown rendering (Glamour) | claim unverified |  |
| 5.1 | Image paste | claim unverified |  |
| 5.1 | Hover-copy | claim unverified |  |
| 5.1 | Viewport culling | claim unverified | check `pkg/tui/components/chat/messagelist.go` |
| 5.1 | Streaming markdown w/ parse state | claim unverified |  |
| 5.1 | Conceal mode for markdown | MISSING | OpenTUI feature; no toggle found |
| 5.1 | Auto dark/light via OSC 10/11 | MISSING |  |
| 5.1 | Line-number gutter w/ signs | claim unverified |  |
| 5.1 | Layered keybind system | PARTIAL | `pkg/tui/keys.go` exists but flat |
| 5.2 | Complexity-based classifier (PicoClaw) | WIRED-NOT-VERIFIED | `internal/routing/` 1 caller (App field) |
| 5.2 | Family-aware routing | SHIPPED-IN-PKG | `internal/providers/catalog.go:339` `ByFamily` | not consumed by routing layer |
| 5.2 | Pricing-aware routing | claim unverified |  |
| 5.2 | TUI tabs for orchestrator/sub-agent/visual | claim unverified |  |
| 5.3 | Sub-agent system (depth-2 limit) | SHIPPED | `internal/subagent/{manager.go,task.go,worker.go,external.go,context.go,cost.go,filestate.go}` + tests; depth check at `manager.go:74` |
| 5.3 | `/subagents` slash command | SHIPPED | registered at `tui.go:595` |
| 5.3 | Cross-agent fault attribution + delegation_failure alert | PARTIAL | `AlertDelegationFailure` mentioned in plan §4.19; not in `internal/journal/journal.go:31-36` enum (verified). Manager has no `Create()` of such alerts |
| 5.3 | Delegated task ledger (BadgerDB) | MISSING |  |

### Phase 3: Memory + Self-Learning + Walls (§6)

| Section | Title | Status | Evidence | Gap |
|---|---|---|---|---|
| 6.1 | Mem0-style persistent memory | SCAFFOLDED | `internal/memory/{badger.go,memory.go,orchestrator.go}` 833 lines | No bridge call (orchestrator stub); no embedding actually computed |
| 6.1 | Embeddings (Python bridge) | SCAFFOLDED | `bridge/embeddings/service.py` exists | Go side never calls `bridge.Client.Embed()` outside its own test |
| 6.1 | Reranking | SCAFFOLDED | `bridge/reranking/service.py` | unwired |
| 6.1 | Cross-session FTS recall | MISSING |  |
| 6.1 | Episodic vs Semantic split | MISSING |  |
| 6.1 | Hot/cold memory paging (MemGPT) | MISSING |  |
| 6.1 | Self-triggered recall `memory_search` tool | MISSING | no such tool in `internal/tools/` |
| 6.2 | Self-learning loop / skill auto-creation (Voyager) | MISSING | no skill-learner sub-agent; `skills/` bundles are empty stubs |
| 6.3 | Hooks system | SHIPPED | `internal/hooks/` 7 callers |
| 6.3 | Hook anti-tampering (PicoClaw) | claim unverified |  |
| 6.3 | Beat detection hooks | MISSING |  |
| 6.4 | Skills loader (`~/.ethos/skills/`) | WIRED-NOT-VERIFIED | `internal/skills/` 4 callers (loader+TUI list) | Skills are loaded but never injected into agent prompts |
| 6.4 | ClawHub registry integration | MISSING |  |
| 6.4 | VirusTotal scanning | MISSING |  |
| 6.4 | Bundled skills (red-team, code-review, humanizer, understand-anything, frontend-design, mutation-test) | MISSING | All six dirs present under `skills/` but **all are empty** (no `SKILL.md`) |
| 6.5 | Wall 1 — Ouroboros / Red Team | SCAFFOLDED | `internal/walls/ouroboros.go` (107) | No callers outside walls package itself |
| 6.5 | Wall 2 — Architecture context | SCAFFOLDED | `internal/walls/architecture.go` (178) | unwired; no `ETHOS_ARCH.md` reader; no domain glossary |
| 6.5 | Wall 3 — Test quality / regression bank | SCAFFOLDED | `internal/walls/testquality.go` (151) | unwired; no behavioral regression bank persistence |
| 6.5 | Walls runner | SCAFFOLDED | `internal/walls/walls.go` (76) + 429-line test | No `/walls` command; no pre-ship trigger |

### Phase 4: Automation + Channels + Browser (§7) — DEFERRED IN PRACTICE

| Section | Title | Status | Evidence | Gap |
|---|---|---|---|---|
| 7.1 | Automation engine (alarm clocks + SOPs + routines) | SCAFFOLDED | `internal/automation/{alarm.go,routines.go,sop.go,store.go,automation.go}` 1185 lines + 544 test | Zero external callers; no `/routine` command; no daemon entry. Phase 2 placeholder. |
| 7.1 | Gateway daemon (`ethos daemon start`) | SHIPPED-IN-CMD / PARTIAL | `cmd/ethos/daemon.go` exists | confirm it actually starts SOP/cron loops |
| 7.1 | Standing orders | MISSING |  |
| 7.1 | Background task ledger (lifecycle) | MISSING |  |
| 7.1 | Task flow / per-task complexity-based timeout | MISSING |  |
| 7.1 | `ethos estop` | MISSING |  |
| 7.2 | Cron timezone-aware | SCAFFOLDED | `internal/cron/{cron.go,parser.go,scheduler.go,store.go}` 633 lines + 478 test | No `/cron` command; no `ethos cron run` entry |
| 7.3 | Agentic browser (Playwright + dev-browser + Stagehand) | PARTIAL | `internal/browser/`, `cmd/ethos/browser_cmd.go` exist; `/browser` command registered | dev-browser sandbox not embedded; `snapshotForAI()` not implemented; Stagehand not present |
| 7.4 | Messaging gateways (WhatsApp/Telegram/Discord) | PARTIAL | `internal/slack/`, `cmd/ethos/slack.go` only; no Baileys, no Telegram, no Discord |
| 7.4 | Cross-channel session continuity | MISSING |  |
| 7.4 | Image-via-channel → vision model | MISSING |  |
| 7.5 | Understand-Anything multimodal (PDF/DOCX/audio) | MISSING |  |
| 7.6 | Auto-update | SHIPPED-IN-CMD | `cmd/ethos/update.go` | confirm pipeline |

### Phase 5: Advanced R&D (§8) — explicitly deferred

| Section | Title | Status | Notes |
|---|---|---|---|
| 8.1 | Cartridge KV / Neural GC / Fast KV compaction | DEFERRED | future research |
| 8.2 | MemAgent / ACE | DEFERRED |  |
| 8.3 | Cross-session task graph | DEFERRED |  |
| 8.4 | Advanced security (MCPSHIELD etc.) | DEFERRED |  |
| 8.5 | Worktrees for parallel agents | PARTIAL | `internal/worktree/` 3 callers; tools exist; LATS/speculative-exec not present |
| 8.6 | RL self-improvement | DEFERRED |  |

---

## 3. Inspiration Folder — Comp Analysis

### inspiration/claude-mem
- One-line: TypeScript Claude Code plugin that auto-captures tool-use observations, async-compresses them via LLM, and injects relevant context into future sessions. Always-on AI flight recorder + pingable query interface.
- Stack: TypeScript / Bun / SQLite + FTS5 + vector
- Star draw: **CLAIM-CONFIRM async queue with self-healing crashed workers** (atomic `UPDATE status='processing' WHERE worker_pid IS alive`); 3-layer progressive disclosure search; tool-blocked observer agent (cannot write files); content-hash dedup; SSE broadcast to web dashboard.
- Already taken: structured observation types (`internal/journal/observation.go`); 3-layer query skeleton (`internal/journal/query.go`).
- Worth taking:
  - **CLAIM-CONFIRM queue** for `internal/journal/summarizer.go` — currently synchronous, fragile.
  - **Hybrid FTS + vector search** in journal queries (currently just metadata).
  - **SSE broadcast** to feed the missing visible memory dashboard (§4.16).
  - **`discovery_tokens` ROI tracking** on memory operations (§4.5 cost extension).
- Specific files to mine:
  - `inspiration/claude-mem/openclaw/src/index.ts` — main capture loop + claim/confirm
  - `inspiration/claude-mem/openclaw/skills/` (`do/`, `make-plan/`) — observation skill prompts
  - `inspiration/claude-mem/PATHFINDER-2026-04-21`, `PATHFINDER-2026-04-22` — design rationale

### inspiration/deep-wiki
- One-line: just an index file pointing to live `deepwiki.com/<org>/<repo>` URLs for every other inspiration repo.
- Stack: markdown only; no code.
- Star draw: convention — "always check DeepWiki first before grepping".
- Already taken: nothing applicable.
- Worth taking: when implementing §4.18 introspection / §6.4 understand-anything, use this index as a meta-table to bootstrap CODEBASE.md generation against any cloned repo.
- Specific files: `inspiration/deep-wiki/README.md` (just the table).

### inspiration/dev-browser
- One-line: Sandboxed AI-safe browser daemon (Rust CLI + Node daemon) with QuickJS WASM execution, persistent named pages, and `snapshotForAI()` for compact LLM-friendly page dumps.
- Stack: Rust (cli) + TypeScript (daemon) + QuickJS WASM
- Star draw: **`snapshotForAI()`** that returns a structured representation of a live DOM, optimized for token cost; CLI-first agent ergonomics; persistent named pages (`getPage("login")` survives across script runs).
- Already taken: nothing — `internal/browser/` and `cmd/ethos/browser_cmd.go` are stubs that don't wrap dev-browser.
- Worth taking:
  - The **CLI contract** (`dev-browser --connect <<'EOF' … EOF`) — Ethos can shell out to dev-browser as one of its three browser tools (plan §7.3). No need to reimplement; it ships as a binary.
  - The **AI usage guide** (`inspiration/dev-browser/cli/llm-guide.txt`) — drop straight into a SKILL.md.
- Specific files to mine:
  - `inspiration/dev-browser/cli/llm-guide.txt:1-60` — agent-facing usage doc
  - `inspiration/dev-browser/daemon/src/` — QuickJS sandbox model
  - `inspiration/dev-browser/skills/dev-browser/` — already-formatted skill we can copy into `skills/dev-browser/SKILL.md`

### inspiration/dive-into-claude-code
- One-line: Academic reverse-engineered architecture analysis of Claude Code v2.1.88, organized as values → principles → implementation.
- Stack: research papers (PDFs + figures)
- Star draw: **1.6% / 98.4% ratio** observation (only 1.6% of code is AI decision logic, the rest is deterministic infrastructure); 5-layer graduated compaction pipeline; SkillTool vs AgentTool distinction; 7 permission modes; 7 safety layers; 93% approval-fatigue empirical finding.
- Already taken: validates ethos's heavy-infra/lean-loop architecture choice.
- Worth taking:
  - **5-layer graduated compaction** (Budget Reduction → Snip → Microcompact → Context Collapse → Auto-Compact). Ethos's LCM is 3-level; extending costs little and reads cleaner.
  - **SkillTool vs AgentTool distinction** — cheap injected instruction vs expensive isolated context (~7x tokens). Plan §5.3 implies but does not codify this.
  - **CLAUDE.md as guidance vs permission as enforcement** distinction — Ethos's system prompt currently mixes both.
- Specific files: `inspiration/dive-into-claude-code/paper/` and `docs/`. PDF only — read for principles, no code to lift.

### inspiration/hermes-agent
- One-line: Python coding agent with 46 modules in `agent/` covering provider adapters, context engine, memory manager, hooks.
- Stack: Python
- Star draw: **`context_compressor.py` + `context_engine.py` + `context_references.py`** trio; `manual_compression_feedback.py`; `error_classifier.py` (parallels PicoClaw's 40-pattern classifier); `models_dev.py` (consumer of models.dev).
- Already taken: error classification idea (plan §4.2 attributes to PicoClaw); models.dev TOML catalog.
- Worth taking:
  - **`manual_compression_feedback.py`** — user can correct what the compaction dropped, feeds future compaction. Plan §4.4 has nothing on this.
  - **`memory_provider.py` + `memory_manager.py`** as a pattern for `internal/memory/orchestrator.go`'s missing bridge calls.
  - **53 bundled skills** (`inspiration/hermes-agent/skills/`) covering apple, github, gaming, mlops, red-teaming etc. — directly minable for `skills/`.
- Specific files:
  - `inspiration/hermes-agent/agent/context_compressor.py`
  - `inspiration/hermes-agent/agent/manual_compression_feedback.py`
  - `inspiration/hermes-agent/agent/models_dev.py`
  - `inspiration/hermes-agent/skills/red-teaming/` — fills empty `skills/red-team/`

### inspiration/mattpocock-skills
- One-line: TypeScript/Claude Code engineering skills repo by Matt Pocock — 8 atomic engineering skills with explicit playbooks.
- Stack: SKILL.md format (markdown + frontmatter)
- Star draw: **diagnose** skill's 10-tier feedback-loop-first escalation ("This is the skill. Everything else is mechanical."); **vertical slice decomposition** (`to-issues` + `to-prd`); **HITL/AFK** classification; **deletion test** for module value; **CONTEXT.md** as canonical glossary; **disable-model-invocation** gating mechanism.
- Already taken: vertical slice decomposition is implemented (plan §4.11) — `internal/pipeline/slicer.go`. Diagnose engine partially implemented (`internal/diagnostic/`).
- Worth taking:
  - **The actual SKILL.md text** for `diagnose/`, `to-issues/`, `to-prd/`, `tdd/`, `grill-with-docs/`, `improve-codebase-architecture/`, `zoom-out/`, `triage/`. These are durable playbooks, not toy prompts. Drop into `skills/` near-verbatim.
  - **`disable-model-invocation: true`** frontmatter — plan §6.4 calls for skill gating; just adopt this convention.
  - **CONTEXT.md** convention for domain glossary (§6.5 Wall 2).
- Specific files to mine (each is one SKILL.md):
  - `inspiration/mattpocock-skills/skills/engineering/diagnose/SKILL.md`
  - `inspiration/mattpocock-skills/skills/engineering/to-prd/SKILL.md`
  - `inspiration/mattpocock-skills/skills/engineering/to-issues/SKILL.md`
  - `inspiration/mattpocock-skills/skills/engineering/tdd/SKILL.md`
  - `inspiration/mattpocock-skills/skills/engineering/improve-codebase-architecture/SKILL.md`
  - `inspiration/mattpocock-skills/skills/engineering/grill-with-docs/SKILL.md`
  - `inspiration/mattpocock-skills/skills/engineering/zoom-out/SKILL.md`
  - `inspiration/mattpocock-skills/skills/engineering/triage/SKILL.md`

### inspiration/models.dev
- One-line: Community-contributed TOML database of AI model specs with public REST API; OpenCode consumes this.
- Stack: TypeScript / Bun
- Star draw: TOML-as-database with filename-as-ID, `extends` inheritance, family taxonomy, capability-flag booleans, fine-grained cost (tier pricing >200K).
- Already taken: ethos has `models/<vendor>/models/*.toml` mirroring this layout (`models/anthropic/`, `models/openai/`, etc.); `internal/providers/catalog.go:15` `Family` field; `ByFamily()` query.
- Worth taking:
  - **`extends` inheritance** for OpenRouter/Groq wrappers — claim unverified in ethos, likely missing.
  - **`model-schema.json`** for IDE autocompletion of model IDs in user config.
  - **CI validation** that every TOML parses against schema.
- Specific files: `inspiration/models.dev/providers/` and `packages/` — copy schema directly.

### inspiration/openclaude
- One-line: TypeScript fork of Claude Code with bonus subsystems (buddy companion sprite, coordinator/worker, memdir, voice mode, Ink UI).
- Stack: TypeScript / Bun / Ink
- Star draw: **`buddy/` companion** (sprite, observer, prompt, useBuddyNotification) — a literal mascot agent that watches the session; **`memdir/`** memory directory pattern with age-aware scanning; **`coordinator/coordinatorMode.ts`** orchestrator pattern.
- Already taken: nothing direct.
- Worth taking:
  - **`memdir/memoryAge.ts` + `findRelevantMemories.ts`** — clean pattern for `internal/memory/orchestrator.go`'s relevance scoring (currently a stub).
  - **`coordinator/workerAgent.ts`** — useful template for `internal/subagent/worker.go`'s contract enforcement.
  - **`buddy/observer.ts`** — Ethos's personality engine could absorb the observer-driven sprite affordance for the journal sub-agent.
- Specific files:
  - `inspiration/openclaude/src/memdir/memoryAge.ts`
  - `inspiration/openclaude/src/memdir/findRelevantMemories.ts`
  - `inspiration/openclaude/src/coordinator/workerAgent.ts`
  - `inspiration/openclaude/src/buddy/observer.ts`

### inspiration/openclaw
- One-line: 365k-star reference TypeScript agent. 53 bundled skills, full enterprise repo template.
- Stack: TypeScript / Bun
- Star draw: skill marketplace, 20+ messaging extensions, standing orders / SOPs, background task ledger, task flow.
- Already taken: GitHub templates patterns (plan §3.1 cites it); skill format.
- Worth taking:
  - **53 SKILL.md files** in `inspiration/openclaw/skills/` — directly minable for `skills/` bundled set + `optional-skills/`.
  - **Standing orders + background task ledger** patterns — none of `internal/automation/` covers these. (Plan §7.1 calls them out.)
- Specific files: `inspiration/openclaw/skills/{1password,apple-notes,github,clawhub,coding-agent,...}/SKILL.md` — pick the 6 plan-mandated bundled skills first.

### inspiration/opencode
- One-line: Go agent with mature Bubble Tea TUI; Ethos's TUI direct ancestor.
- Stack: Go (Bubble Tea + Lip Gloss)
- Star draw: full UX patterns plan §5.1 lists; LSP integration; theme system.
- Already taken: most TUI patterns (commands, model picker, status bar, permission dialog). 28 slash commands registered.
- Worth taking: streaming markdown parse-state, viewport culling, conceal mode, OSC 10/11 dark/light detection — verify each in `pkg/tui/components/`.
- Specific files: `inspiration/opencode/packages/{ui,console,opencode}/`.

### inspiration/opentui
- One-line: Native Zig + TS TUI framework with Yoga flexbox, viewport culling, streaming markdown via tree-sitter.
- Stack: Zig + TypeScript
- Star draw: ground-up framework concepts, not a Bubble Tea drop-in.
- Already taken: none directly (ethos uses Bubble Tea).
- Worth taking: conceptual patterns only — viewport culling, conceal mode, streaming parse state, OSC color queries, `LineNumberRenderable` gutter, layered keymap. Re-implement in Go.
- Specific files: `inspiration/opentui/packages/`.

### inspiration/picoclaw
- One-line: 28.6k-star Go coding agent. Direct architectural ancestor for ethos's Go core.
- Stack: Go
- Star draw: routing classifier; failover + cooldown; error classifier (40 patterns); SecureString + secret filtering; SubTurn concurrency; provider protocol-family grouping; tool TTL / hidden tools / registry clone; 35+ deny patterns; prompt metadata layering; config auto-migration as pipeline.
- Already taken: routing classifier (`internal/routing/`); EventBus; provider catalog; subagent depth limits.
- Worth taking:
  - **Steering queue** (plan §4.1 attributes to PicoClaw) — `internal/agent/steering.go` exists, confirm parity.
  - **Error classifier 40 patterns** — `internal/providers/` likely has fewer.
  - **SecureString reflection-based redactor** — Ethos has a security scanner but the reflection-based output redactor for tool results is missing.
  - **35+ deny patterns** verbatim — confirm `internal/security/` matches.
  - **Hook anti-tampering fingerprint** — `internal/hooks/` likely doesn't enforce this.
- Specific files:
  - `inspiration/picoclaw/pkg/routing/`
  - `inspiration/picoclaw/pkg/providers/`
  - `inspiration/picoclaw/pkg/agent/`
  - `inspiration/picoclaw/pkg/credential/` (SecureString)

### inspiration/rtk
- One-line: Rust CLI that intercepts shell tool output and compresses it before it reaches LLM context. 60-90% token savings, agent-agnostic.
- Stack: Rust
- Star draw: **declarative rewrite registry** (60+ regex rules), two-tier extensibility (compiled Rust + TOML DSL), graceful degradation, **tee recovery** to disk, per-tool token tracking with `rtk gain --graph`.
- Already taken: nothing — plan §4.4 calls this out as MISSING.
- Worth taking:
  - **Either consume RTK as a binary** (zero-cost integration, single hook) **or** port the rewrite-registry pattern into a new `internal/tools/compressor/` package.
  - **Tee recovery on failure** is essential — drop raw output to disk with a hint path so the agent can re-read without re-executing.
- Specific files:
  - `inspiration/rtk/Cargo.toml` and `src/discover/rules.rs` — the registry pattern
  - `inspiration/rtk/CLAUDE.md` — published integration guide

### inspiration/understand-anything
- One-line: Plugin that ramps an agent on any codebase via 9 sub-skills + 9 specialized analysis agents.
- Stack: TypeScript
- Star draw: skill-per-modality (`understand`, `understand-chat`, `understand-dashboard`, `understand-diff`, `understand-domain`, `understand-explain`, `understand-knowledge`, `understand-onboard`); agent-per-perspective (`architecture-analyzer`, `domain-analyzer`, `file-analyzer`, `graph-reviewer`, `knowledge-graph-guide`, `project-scanner`, `tour-builder`).
- Already taken: nothing — `skills/understand-anything/` is empty.
- Worth taking:
  - The full **9-skill bundle** wholesale → `skills/understand-anything/`.
  - The **9-agent pattern** as templates for sub-agent definitions in `internal/subagent/external.go`.
  - Plan §4.18 (introspection) is exactly this pattern aimed inward at Ethos's own codebase.
- Specific files:
  - `inspiration/understand-anything/understand-anything-plugin/skills/understand-onboard/SKILL.md`
  - `inspiration/understand-anything/understand-anything-plugin/agents/project-scanner.md`
  - `inspiration/understand-anything/understand-anything-plugin/agents/tour-builder.md`

### inspiration/zeroclaw
- One-line: 30.7k-star Rust agent with deep automation/security focus.
- Stack: Rust (workspace of 14 crates: `zeroclaw-{api,channels,config,gateway,hardware,infra,macros,memory,plugins,providers,runtime,tool-call-parser,tools,tui}`).
- Star draw: SOP engine (5 modes incl. deterministic), routines, sandboxing, 3 autonomy levels, deterministic execution mode (no LLM calls).
- Already taken: SOP scaffolding (`internal/automation/sop.go` 334 lines).
- Worth taking:
  - **5 SOP execution modes** (Auto, Supervised, StepByStep, PriorityBased, Deterministic) — verify all five are in `internal/automation/sop.go`.
  - **Deterministic mode contract** — output of step N = input of step N+1, no LLM, survives crashes.
  - **Approval timeout policies** (Critical/High auto-approve after timeout, Normal/Low waits).
- Specific files:
  - `inspiration/zeroclaw/crates/zeroclaw-runtime/`
  - `inspiration/zeroclaw/crates/zeroclaw-tools/`
  - `inspiration/zeroclaw/crates/zeroclaw-memory/`

---

## 4. Honest Gap Matrix

| Concern | Plan section | Status in code | Honest assessment |
|---|---|---|---|
| Autonomous sub-agent loop with stop semantics | §5.3, §7.1 | `internal/subagent/manager.go` has depth (≤2) + capacity limits + `worker.go` (112) + `cost.go` (58). NO budget-aware self-stop on token overage; NO `lost`-status reaper. | Foundation present, governance missing. Add cost ceiling per task; reaper on `cmd/ethos/daemon.go`. |
| Deep wiki via /init | §4.18, §6.4 (understand-anything) | `internal/introspection/` 200 lines + 330 test, **0 callers**; `/init` slash command IS registered (`pkg/tui/tui.go:582`) but writes a "starter `.ethos/` config", NOT a deep wiki. `skills/understand-anything/` empty. | **Big gap.** Wire `/init` to introspection generators AND seed `~/.ethos/introspection/CODEBASE.md` from a project-scanner subagent (port `inspiration/understand-anything/.../agents/project-scanner.md`). |
| Sub-agent contract enforcement (no hallucination) | implied §5.3, §4.15, MIRROR (§4.16) | Confidence assessor on parent (`agent/confidence.go`); subagent worker has no spec-conformance check. | Add a verifier step: parent issues a contract (deliverables + post-conditions); worker output must satisfy parent's checker before merge. Today nothing prevents fabrication. |
| Journal alerts surfaced in next session opener | §4.19 | `AlertStore` + 4 alert types defined; **zero `.Create()` callers**, zero readers in `pkg/tui/boot.go`. Recovery does NOT push pattern_detected alerts. | Wire: (a) recovery → `AlertStore.Create(AlertPatternDetected)` on repeated errors; (b) frustration heuristic → `AlertFrustration`; (c) compaction → `AlertCompactionSkip`; (d) boot → read `AlertStore.Pending()` and surface in TUI. |
| LCM compaction wired to agent | §4.4 | `internal/compaction/lcm.go` (171) + `compactor.go` (67) + `prompt_compress.go` (106) all standalone. Agent's `agent.go:472` builds its own compact prompt and calls provider directly. | Replace agent's ad-hoc compact path with `compactor.Compact()`; subscribe `budget_warning` → trigger Level-1 escalation. |
| Tool output compression middleware (RTK) | §4.4 | MISSING — no `internal/tools/compressor/`. | New package + per-tool registrar in `internal/tools/`. Or shell out to RTK. |
| Python bridge actually consumed | §4.17, §6.1 | Bridge daemon ships, gRPC stubs ship, `bridge.Client` exists. **Only consumer is `internal/doctor/checks/system.go:100` health check.** `internal/memory/orchestrator.go` does NOT call `Embed/Rerank`. | Wire `orchestrator.go` to `bridge.Client.Embed()`; gate behind `cfg.Bridge.Enabled` so it degrades when daemon is down. |
| Skills handed to agent | §6.4 | Loader populates `app.Skills`; system prompt does NOT include skill instructions; no SkillTool to invoke them. All 6 bundled skill folders are EMPTY (no SKILL.md). | (a) Author bundled SKILL.md files (steal from Hermes/OpenClaw/Mattpocock). (b) Inject loaded skills into system prompt under `<skills>` section. (c) Add a SkillTool wrapper for selective activation. |
| BadgerDB durability (snapshots/exports/integrity) | §4.20 | MISSING all three. | Wire daily snapshot in `internal/cron/`; export ritual in `journal/summarizer.go`; `doctor --check-db` in `cmd/ethos/doctor.go`. |
| Walls (3) actually run | §6.5 | All three wall packages exist (~436 lines + 429 test) — no callers. No `/walls` command. | Add `/walls` command + pre-ship hook that runs Wall 3 (test quality) on every commit, Wall 1 (red team) on core-system touches. |
| Pipeline executes vertical slices | §4.11 | `internal/pipeline/{pipeline,executor,slicer}.go` 521 lines — no callers; no `/slice` or `/plan` command. | Wire `/plan` → produce PRD via slicer; `/slice` → enqueue tracer-bullet issues to subagent manager. |
| Diagnostic 10-tier feedback loop | §4.13 | `internal/diagnostic/analyzer.go` (238) — basic likelihood scoring; no 10-tier escalation list (failing test → curl → CLI → headless → property loop → bisection → HITL bash). | Extend `analyzer.go` with the explicit ladder; wire to `/diagnose` slash command. |
| Cron + automation actually scheduled | §7.1, §7.2 | `internal/cron/` 633 lines + `automation/` 1185 lines — no callers, no daemon entry. | `cmd/ethos/daemon.go` should boot cron scheduler + automation engine; expose `/cron` and `/routine` commands. |
| Prompt rewriter middleware | §4.10 | 510 lines + 457-line test, **0 callers**. | Insert at top of `agent.Run` / `Stream` step; gate behind `cfg.Rewriter.Enabled` so default-off is safe. |

---

## 5. Recommended Priority Order

Sequenced by impact × confidence; effort estimate is rough engineer-days.

1. **Wire alerts end-to-end** — 1d — unlocks §4.16 proactive transparency, §4.19 alerts, blind-spot surfacing. Code already exists; just plumb 4 producers + 1 reader.
2. **Author the 6 bundled SKILL.md files** (red-team, code-review, humanizer, understand-anything, frontend-design, mutation-test) — 1d — content can be ported from `inspiration/{hermes-agent,openclaw,mattpocock-skills}/skills/`. The dirs are empty embarrassments.
3. **Wire LCM compactor + prompt compression into agent loop** — 2d — biggest token-discipline win; unblocks §4.4. Replace ad-hoc compact at `agent.go:472`.
4. **Wire `/init` → introspection + project-scanner sub-agent** — 2d — fills the "deep wiki on first run" promise; unlocks §4.18 and §4.11 onboarding.
5. **Wire prompt rewriter middleware** (default-off flag) — 1d — code is done; add one call site; ship as opt-in.
6. **Wire pipeline + slicer behind `/plan` and `/slice`** — 2d — turns vertical-slice scaffolding into a usable PRD generator. Plan §4.11 / §4.13.
7. **Wire walls behind `/walls` + commit-time Wall 3** — 1.5d — turns 700+ lines of dormant quality-gate code on.
8. **Wire cron + automation in daemon** — 2d — makes `ethos daemon start` actually schedule SOPs and routines.
9. **Tool-output compression middleware (port RTK or shell out)** — 3d — single biggest token win after compaction. Plan §4.4.
10. **Bridge consumer in `memory/orchestrator.go`** — 2d — Python bridge is dead weight until something calls it. Embeddings + rerank → real memory.
11. **BadgerDB snapshots + export ritual + `doctor --check-db`** — 2d — §4.20 durability. Identity-crisis insurance.
12. **Sub-agent contract enforcement layer** — 3d — verifier between parent contract and worker output; reduces hallucinated deliverables.
13. **dev-browser as one of three browser tools** — 1.5d — shell-out, copy `llm-guide.txt` into a SKILL.md, register `/browser dev` subcommand.
14. **Behavioral regression bank persistence** (Wall 3 sub-feature) — 2d — every shipped bug → test → BadgerDB record so the same bug cannot reship.
15. **Cross-channel session continuity** + Telegram/Discord gateways — 5d+ — large surface, lower confidence, defer until §1–14 settle.

---

## 6. What I'd Cut from the Master Plan

- **§4.16 Visible memory dashboard with live SSE broadcast.** Cool demo, low ROI for a CLI-first single-user tool. The export markdown (§4.20) covers 90% of the user need.
- **§4.16 Cognitive blind-spot detection.** Gracefully expressed but inherently noisy and hard to tune. Ship after 6 months of real session data, not before.
- **§4.16 Persona Selection Model + 47-paper research grounding** as code work. Keep the philosophy in `personality/` but do not turn the 47-paper bibliography into runtime behavior beyond what's already in `internal/personality/{soul,style,personality}.go`. Diminishing returns.
- **§5.1 OpenTUI parse-state streaming markdown re-implementation in Go.** Bubble Tea + Glamour is good enough for a coding TUI. Don't port a Zig framebuffer reconciler.
- **§7.1 Standing orders + Background task ledger + Task flow** — three overlapping abstractions. Pick one (background task ledger gives the most observability) and skip the other two until a real workflow demands them.
- **§7.4 WhatsApp gateway via Baileys.** WhatsApp's TOS hostility + Baileys instability isn't worth the support load on a small project. Telegram + Discord cover the channel use case.
- **§8.1 Cartridge KV / Neural GC compaction.** Research papers, not 2026-Q2 code. Defer indefinitely.
- **§8.5 LATS-style tree search.** Famous, fun, expensive in tokens. Skip for a coding agent.
- **§8.6 RL self-improvement.** Decade-out. Cut.

---

## 7. Verification Commands

```sh
# Repo-level
cd /home/harsh/docker/ethos
go build ./...
go test -race ./...
golangci-lint run

# Phase 0
ls .github/workflows .github/ISSUE_TEMPLATE
test -f CONTRIBUTING.md && test -f SECURITY.md && test -f AGENTS.md && echo "phase0 community files OK"
ls research/papers/ 2>/dev/null | wc -l   # expect ~47

# Phase 1
go test -race ./internal/agent/...
go test -race ./internal/compaction/...
go test -race ./internal/journal/...
go test -race ./internal/personality/...
go test -race ./internal/rewriter/...
go test -race ./internal/pipeline/...
go test -race ./internal/diagnostic/...
go test -race ./internal/introspection/...
go test -race ./internal/security/...

# Phase 1 wiring sanity (callers outside the package itself)
for p in compaction rewriter pipeline introspection diagnostic walls automation cron memory; do
  printf "%-15s callers: " "$p"
  grep -rln "internal/$p" --include='*.go' . 2>/dev/null \
    | grep -v "^./internal/$p" | grep -v inspiration | wc -l
done

# Bundled skills sanity (currently broken — all empty)
for s in red-team code-review humanizer understand-anything frontend-design mutation-test; do
  test -f "skills/$s/SKILL.md" && echo "skills/$s: OK" || echo "skills/$s: MISSING SKILL.md"
done

# Bridge consumer sanity (today: only doctor)
grep -rln "bridge.Client\|bridge\.NewClient" --include='*.go' . 2>/dev/null \
  | grep -v inspiration | grep -v "^./bridge/"

# Phase 2
go test -race ./internal/subagent/... ./internal/routing/...
ethos doctor                          # human review of doctor output

# Phase 3 (most fail because unwired — that's the point)
go test -race ./internal/walls/... ./internal/skills/... ./internal/memory/...

# Phase 4
go test -race ./internal/automation/... ./internal/cron/... ./internal/browser/...

# CLI smoke
ethos --help
ethos doctor
ethos config --help
ethos run --help

# Slash command coverage in TUI registration
grep -E '^\s*\{ID: "' pkg/tui/tui.go | grep -oE '"[a-z-]+"' | sort -u

# Plan-mandated commands NOT yet in TUI (expect: walls, routine, cron, introspect, slice, journal, redteam, plan, diagnose)
for c in walls routine cron introspect slice journal redteam plan diagnose; do
  grep -q "ID: \"$c\"" pkg/tui/tui.go || echo "missing slash: /$c"
done
```

---

## Appendix: Notes & Caveats

- This audit deliberately uses "WIRED-NOT-VERIFIED" rather than rubber-stamping.
  Many sections marked SHIPPED have spec-aligned package code; runtime
  fidelity to the plan was not exhaustively traced for every claim.
- "claim unverified" rows in §2 mean the deliverable likely exists but this
  pass did not run the specific grep / test to prove it.
- 510+457 lines (`rewriter/`) being uncalled is the single most striking
  scaffolded-but-orphan finding — a near-complete subsystem awaiting one wire.
- Empty `skills/<name>/` dirs (`code-review`, `red-team`, `understand-anything`,
  etc.) are user-facing brand damage: the README/plan promises bundled skills,
  the dirs are 0 bytes. Cheapest high-impact fix on the list.
