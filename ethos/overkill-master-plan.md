# Overkill — Master Plan

> A Go core + Python bridge vibe-coding agent.
> The best of OpenClaw, Hermes, ZeroClaw, and PicoClaw — unified.
> Your coding friend, not a corporate tool.

---

## 1. Project Identity

| Field | Value |
|---|---|
| **Name** | Overkill |
| **Tagline** | The vibe-coding agent that actually has discipline. |
| **Stack** | Go (core runtime, TUI, agent loop) + Python (ML bridge: embeddings, reranking, memory) |
| **Storage** | BadgerDB (embedded KV, pure Go, no CGo) |
| **License** | Dual MIT / Apache-2.0 |
| **Repo** | https://github.com/Sahaj-Tech-ltd/overkill — public from day one |
| **Binary** | `overkill` |
| **Config** | `~/.overkill/` |

### Runtime Directory (`~/.overkill/`)

Created on first run (`overkill run` or `overkill init`). NOT the repo — this is user data.

```
~/.overkill/
├── config.toml             # Main config (TOML, auto-migrating)
├── memories/               # soul.md, user.md, relationship state, self-model
├── plans/                  # Session plans, PRP files, task artifacts
├── introspection/          # Skill-triggered self-knowledge (NOT read on boot)
│   ├── CODEBASE.md         # Auto-generated codebase map (dirs, interfaces, patterns)
│   ├── MODEL_CARD.md       # Current model capabilities, limitations, pricing
│   ├── KNOWN_ISSUES.md     # Known bugs, gotchas, workarounds
│   └── ARCHITECTURE.md     # Architectural decisions and patterns
├── journal/                # Diary system (flight recorder + journal + alerts)
│   ├── raw/                # Raw input/output logs (JSONL, append-only)
│   ├── entries/            # Sub-agent journal summaries (markdown)
│   └── alerts.md           # Surfaces important stuff to next session
├── sessions/               # Session data (BadgerDB)
├── skills/                 # User-installed skills
├── plugins/                # User plugins
├── data/                   # BadgerDB storage
└── work/                   # Working directory for projects
```

**Boot reads (lean):** soul.md + relationship state + project context. Period.
**Introspection:** Skill-triggered on demand. "Hey what's your config about X?" fires the skill.
**Journal:** Always recording raw. Sub-agent summarizes on session exit or cron.

---

## 2. Repository Structure

```
overkill/
├── .github/                    # Enterprise GitHub setup
├── cmd/
│   └── overkill/                  # CLI entrypoint (Cobra)
├── internal/
│   ├── agent/                  # Core agent loop (ReAct-style)
│   ├── config/                 # Config loading, validation, migration
│   ├── security/               # Security plane, injection detection, pre-exec scanner
│   ├── compaction/             # Context compaction engine
│   ├── routing/                # Model routing (complexity-based + pricing-aware)
│   ├── session/                # Per-folder session management
│   ├── tools/                  # Built-in tools (shell, fs, grep, git, web)
│   ├── providers/              # LLM provider adapters
│   ├── tokenizer/              # Token counting and estimation
│   ├── cost/                   # Token/cost tracking, budget enforcement
│   ├── hooks/                  # Lifecycle hooks system
│   ├── skills/                 # Skill loading and registry
│   ├── memory/                 # Memory orchestration (Go side)
│   ├── cron/                   # Cron scheduler (timezone-aware)
│   ├── doctor/                 # Auto-heal broken configs
│   ├── automation/             # SOP engine, routines, alarm clocks
│   ├── personality/            # Personality engine, relationship tracking
│   ├── rewriter/               # Prompt rewriter middleware
│   ├── pipeline/               # Incremental dev pipeline (spec→test→code→refactor)
│   ├── walls/                  # 3 Walls quality gates (relaxed)
│   ├── diagnostic/             # Debugging diagnostic reports
│   ├── introspection/          # Self-knowledge skill (triggered on demand, NOT read on boot)
│   └── journal/                # Diary engine (raw logs, journal sub-agent, alerts)
├── pkg/
│   ├── api/                    # Public Go API
│   └── tui/                    # Terminal UI (Bubble Tea + Lip Gloss)
├── bridge/                     # Python bridge (gRPC)
│   ├── embeddings/             # Embedding generation
│   ├── reranking/              # Result reranking
│   ├── memory/                 # Vector memory backends
│   ├── compaction/             # LLM-based compaction via cheap model
│   └── proto/                  # gRPC proto definitions
├── skills/                     # Bundled skills (SKILL.md format)
│   ├── red-team/               # Adversarial review skill
│   ├── code-review/            # Code quality review
│   ├── humanizer/              # Strip AI-isms
│   ├── understand-anything/    # Codebase ramp / Deep Wiki
│   ├── frontend-design/        # UI generation
│   └── mutation-test/          # Mutation testing skill
├── optional-skills/            # Optional official skills
├── plugins/                    # Built-in plugins
├── scripts/                    # Build, install, release scripts
├── docs/                       # Documentation (mdBook)
├── tests/                      # Integration tests
├── inspiration/                # [GITIGNORED] Cloned competitor repos
│   ├── openclaw/
│   ├── hermes-agent/
│   ├── zeroclaw/
│   └── picoclaw/
├── research/                   # Research papers + references
│   ├── papers/                 # PDFs (47 papers)
│   └── REFERENCES.md           # Structured paper index
├── .env.example
├── Dockerfile
├── docker-compose.yml
├── Makefile
├── go.mod
├── go.sum
├── pyproject.toml              # Python bridge deps
├── AGENTS.md                   # Agent instructions
├── CONTRIBUTING.md
├── SECURITY.md
├── README.md
└── PLAN.md                     # This file
```

---

## 3. Phase 0: Foundation (GitHub Enterprise Setup)

**Goal:** Enterprise-grade repo that scales to 500+ PRs/month. First-time maintainer ready.

### 3.1 `.github/` Directory

Patterns copied from OpenClaw (templates), Hermes (CI/security), ZeroClaw (quality gates).

#### `.github/ISSUE_TEMPLATE/`

| File | Source | Pattern |
|---|---|---|
| `bug_report.yml` | OpenClaw + ZeroClaw | `[Bug]:` prefix, severity S0-S3, `NOT_ENOUGH_INFO` evidence rule, pre-flight checks |
| `feature_request.yml` | OpenClaw + Hermes | Problem-first framing, scope S/M/L |
| `setup_help.yml` | Hermes | `[Setup]:` prefix, install method dropdown |
| `config.yml` | OpenClaw | `blank_issues_enabled: false`, Discord/docs contact links |

#### `.github/pull_request_template.md`

Blend of Hermes + ZeroClaw:
- Summary (2-5 bullets)
- Change Type checkboxes (bug, feature, refactor, security, docs, skill)
- Scope checkboxes (agent-loop, security, compaction, TUI, routing, memory, tools, CI)
- Validation Evidence (paste literal test output)
- Security & Privacy Impact (5 yes/no)
- Compatibility & Migration
- Rollback Plan (required for medium/high risk)
- AI-Assisted checkbox + Co-Authored-By trailer

#### `.github/workflows/`

| Workflow | Purpose |
|---|---|
| `tests.yml` | Go tests + Python bridge tests, concurrency groups, pinned action SHAs |
| `lint.yml` | golangci-lint, ruff for Python |
| `supply-chain-audit.yml` | Scan for `.pth`, base64+exec combos, install hooks |
| `docker-publish.yml` | Multi-arch, fork protection |
| `security.yml` | CodeQL, gitleaks, secret scanning |
| `labeler.yml` | Auto-label by changed file paths |
| `contributors.yml` | Auto-update contributor avatar grid in README |

> **Dropped from Harness:** `actionlint.yml` — linting is covered by `lint.yml` (golangci-lint + ruff). No separate actionlint workflow.

#### `.github/CODEOWNERS`

```
* @<maintainer-username>
/.github/CODEOWNERS @<maintainer-username>
/SECURITY.md @<maintainer-username>
/internal/security/ @<maintainer-username>
/bridge/ @<maintainer-username>
/.github/workflows/docker-publish.yml @<maintainer-username>
/scripts/release* @<maintainer-username>
```

#### `.github/dependabot.yml`

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule: { interval: daily }
    open-pull-requests-limit: 3
    labels: [dependencies]
    groups:
      go-all: { update-types: [minor, patch] }
  - package-ecosystem: pip
    directory: /bridge
    schedule: { interval: daily }
    open-pull-requests-limit: 3
    labels: [dependencies]
  - package-ecosystem: github-actions
    directory: /
    schedule: { interval: daily }
    open-pull-requests-limit: 1
    labels: [ci, dependencies]
```

#### `.github/labeler.yml`

```yaml
"agent-loop": { changed-files: [{ any-glob-to-any-file: ["internal/agent/**"] }] }
"security": { changed-files: [{ any-glob-to-any-file: ["internal/security/**"] }] }
"compaction": { changed-files: [{ any-glob-to-any-file: ["internal/compaction/**", "bridge/compaction/**"] }] }
"TUI": { changed-files: [{ any-glob-to-any-file: ["pkg/tui/**"] }] }
"routing": { changed-files: [{ any-glob-to-any-file: ["internal/routing/**"] }] }
"memory": { changed-files: [{ any-glob-to-any-file: ["internal/memory/**", "bridge/memory/**"] }] }
"tools": { changed-files: [{ any-glob-to-any-file: ["internal/tools/**"] }] }
"skills": { changed-files: [{ any-glob-to-any-file: ["skills/**", "optional-skills/**"] }] }
"bridge": { changed-files: [{ any-glob-to-any-file: ["bridge/**"] }] }
"CI": { changed-files: [{ any-glob-to-any-file: [".github/**"] }] }
"docs": { changed-files: [{ any-glob-to-any-file: ["docs/**", "*.md"] }] }
"personality": { changed-files: [{ any-glob-to-any-file: ["internal/personality/**"] }] }
"automation": { changed-files: [{ any-glob-to-any-file: ["internal/automation/**"] }] }
```

### 3.2 Root-Level Community Files

#### `CONTRIBUTING.md`

From Hermes pattern, adapted for Go + Python:
- Contribution priorities: bug fixes > security > perf > new skills > new tools > docs
- Skill vs Tool decision framework (from Hermes)
- Branch naming: `fix/`, `feat/`, `docs/`, `test/`, `refactor/`, `security/`
- Conventional Commits enforced
- PR limits: 5 open per author
- AI-assisted PR policy: must disclose, Co-Authored-By trailer
- Pre-PR checklist: `go test ./...`, `golangci-lint run`, `ruff check bridge/`, `go build ./...`
- No refactor-only PRs unless requested
- One concern per PR

#### `SECURITY.md`

Blend of OpenClaw + Hermes + ZeroClaw:
- Private disclosure via GitHub Security Advisories
- 48-hour acknowledgment, 1-week assessment, 2-week critical fix
- Trust model: single-user personal agent, NOT multi-tenant
- Autonomy levels: ReadOnly, Supervised (default), Full
- Sandboxing layers: workspace isolation, path traversal blocking, command allowlisting, forbidden paths, rate limiting
- Out of scope section + common false-positive patterns
- No bug bounty. 90-day coordinated disclosure

#### `AGENTS.md`

Instructions for AI coding assistants:
- Build: `go build ./...`, `go test ./...`
- Lint: `golangci-lint run`, `ruff check bridge/`
- Architecture overview with directory map
- Key interfaces and where to find them

### 3.3 `README.md`

Banger README:
- ASCII art logo (2004 vibes, make it feel alive)
- Badge row: Go version, Python version, license, build status, stars (shields.io)
- GitHub star counter
- 30-second pitch
- Feature highlights with ASCII graphics
- Quick install: `curl -fsSL https://overkill.dev/install | bash`
- Architecture diagram (ASCII)
- Comparison table vs OpenClaw / Hermes / ZeroClaw / Claude Code / OpenCode
- **Contributor avatar grid** at the bottom (auto-updated via GitHub Action)
  - Fetches from GitHub Contributors API
  - Shows GitHub pfp for everyone who submitted a PR
  - Like OpenClaw/Hermes contributor sections

### 3.4 Inspiration Folder (Gitignored, Shallow Clones)

```bash
mkdir -p inspiration
git clone --depth 1 https://github.com/openclaw/openclaw.git inspiration/openclaw
git clone --depth 1 https://github.com/NousResearch/hermes-agent.git inspiration/hermes-agent
git clone --depth 1 https://github.com/zeroclaw-labs/zeroclaw.git inspiration/zeroclaw
git clone --depth 1 https://github.com/sipeed/picoclaw.git inspiration/picoclaw
git clone --depth 1 https://github.com/opencode-ai/opencode.git inspiration/opencode
git clone --depth 1 https://github.com/SawyerHood/dev-browser.git inspiration/dev-browser
git clone --depth 1 https://github.com/Lum1104/Understand-Anything.git inspiration/understand-anything
```

### 3.5 Research Papers (47 papers in `research/papers/`)

See Section 10 for full list. Downloaded PDFs + `research/REFERENCES.md` with structured summaries.

---

## 4. Phase 1: MVP — Agent Loop + Security + Compaction + Personality

**Goal:** A working agent that talks to LLMs, reasons before acting, has token discipline, compacts context, has personality, and produces quality code through an incremental pipeline. CLI-first.

### 4.1 Core Agent Loop
- [ ] ReAct-style think-act-observe cycle (`internal/agent/`)
- [ ] Two-Step Forethought: calculate secondary impact before writes
- [ ] Spec-Driven mode: auto-switch to plan creation before execution
- [ ] Conversation management (turn tracking, message history)
- [ ] System prompt construction (anti-bloat, instruction-dense)
- [ ] **Command completion marker:** All shell commands appended with `&& echo __OVERKILL_DONE__`. Guarantees Overkill always knows when a command finished executing — regardless of output verbosity, streaming, or silence. The agent loop's `observe` phase waits for this marker, not for stdout EOF. Eliminates ambiguity when commands produce no output or are still streaming.

**Inspiration:** Hermes `agent/` core loop, PicoClaw `pkg/agent/` turn management

### 4.2 LLM Provider Layer
- [ ] Provider interface abstraction (`internal/providers/`)
- [ ] Group by protocol family (OpenAI-compatible, Anthropic, Gemini native) — from PicoClaw
- [ ] Provider implementations: OpenAI, Anthropic, Gemini, DeepSeek, z.ai/GLM, Ollama, OpenRouter
- [ ] Custom endpoint support (e.g., MiniMax direct vs coding plan endpoints)
- [ ] **Provider selection UI like ZeroClaw:** User selects OpenAI → lists all providers → MiniMax → "coding plan" or "API" → default is `api.minimax.io/coding-plan` → "Change if different: (leave blank for default)"
- [ ] **Model catalog as TOML data** (inspired by models.dev — data, not code):
  - Models defined in TOML files under `~/.overkill/models/`. Human-writable, diff-friendly, auto-validated in CI. No database migration.
  - **Filename-as-ID:** `openai/gpt-5.toml` → model ID `openai/gpt-5`. Eliminates ID-field mismatches.
  - **`extends` inheritance:** Wrapper models (OpenRouter, Groq) reference canonical models via `[extends] from = "openai/gpt-5"`, overriding only cost. 240+ model files become 5-line stubs.
  - **Capability flags as booleans:** `reasoning`, `tool_call`, `structured_output`, `temperature`, `attachment`, `open_weights`, `modalities.input[]`, `modalities.output[]`. Boolean filtering, no string matching.
  - **Family taxonomy:** ~200 canonical family names (`claude-opus`, `gpt-nano`, `deepseek-thinking`) enabling family-aware routing ("use cheapest Claude model").
  - **Fine-grained cost model:** Input, output, cache_read, cache_write, audio_in, audio_out, reasoning tokens, tiered pricing (>200K context).
  - **Provider metadata in TOML:** npm package/env vars/docs URL/API base URL per provider. New provider = one TOML file, not a new Go file in factory.go.
  - **JSON endpoint for external consumers:** Serve resolved model catalog as static JSON. `model-schema.json` for IDE autocompletion of valid model IDs.
  - **CI validation:** `go test` loads all TOML, validates schema. Malformed model = CI fails.
- [ ] Failover chain with cooldown tracking — from PicoClaw
- [ ] **Retry logic from OpenCode:** Exponential backoff (2s base, 2x growth, 20% jitter), 8 max retries, only on 429/500/529, honor Retry-After header

### 4.3 Security Plane (Relaxed — serves the product, not paranoia)
- [ ] Isolated security process for prompt injection detection
- [ ] Skeptical Ingestion: all tool outputs, web content treated as untrusted
- [ ] Hard Refuse Rule: auto-detect "ignore previous instructions", report to user
- [ ] Pre-Exec Command Scanner: scan commands before execution
- [ ] **Before destructive commands (`rm -rf` etc):** auto git commit or filesystem backup
- [ ] **Permission escalation, not silent deny:** When a command matches a deny pattern, Overkill does NOT silently block it. Instead:
  - Presents the blocked command to the user with the reason: *"Blocked: `rm -rf ./build` matches destructive pattern. What do you want me to do?"*
  - Options: **(1) Allow once** — run this time only, ask again next time. **(2) Allow for project** — add to project-level allowlist, skip this check for this project. **(3) Add to do-not-ask** — add to global allowlist, never ask about this pattern again.
  - Precedence: Deny patterns fire first (flag). User override takes priority (allow once/project/global). Same mechanics as `Permission dialog: Allow / Allow for Session / Deny` from TUI but applied to the security layer under the hood.
  - **Never silently blocks the user's intent.** The guard is a gate, not a wall.
- [ ] Sensitive data filtering before LLM context — from PicoClaw
- [ ] Secret Scanner: prevent credential leaks in git pushes
- [ ] Agent Privilege Separation — separate reader/action agents (paper #21)

### 4.4 Context Compaction (LCM-Inspired)

> **Core inspiration:** LCM (Lossless Context Management) — Voltropy, Feb 2026.
> Dual-state memory, hierarchical DAG, three-level escalation, zero-cost for short tasks.

- [ ] **Dual-state memory:** Immutable Store (every message preserved verbatim, never modified) + Active Context (assembled window sent to LLM each turn)
- [ ] **Hierarchical DAG:** Older messages → summary nodes. Originals always retrievable via `lcm_expand` / `lcm_grep` style tools
- [ ] **Three-level escalation (guaranteed convergence):**
  - Level 1: Detailed LLM summary (preserve_details mode)
  - Level 2: Aggressive bullet-point summary (half target tokens)
  - Level 3: Deterministic truncation to 512 tokens — no LLM, always succeeds
- [ ] **Zero-cost continuity:** Below τ_soft → passive logger, no summarization overhead. Above → async compaction between turns. Only τ_hard blocks user
- [ ] 50% trigger: auto-compact at 50% context window usage (τ_soft)
- [ ] **Pre-compaction checkpoint:** Agent is aware of its context utilization. When approaching τ_soft (≥48%) and the user requests a large task, the agent proactively warns: *"At 48% context — let me compact now so I'm fresh for this. Saving what I know, making a plan first."* Strategically compacts BEFORE the big task, not during it. Updates journal, writes plan artifacts, ensures nothing worth keeping is lost to compaction.
- [ ] Summarize everything except last 20 messages via compaction model
- [ ] Caveman Mode: escalate bluntness as budget limits approach
- [ ] Anti-bloat system prompts
- [ ] Grep-n navigation: prohibit full file reads, use grep -n and ranged reads only
- [ ] Mine Context First: check existing context before tool calls
- [ ] **Auto-compact at 95%** (τ_hard) with "Summarizing..." overlay
- [ ] **Large file handling:** Files >25K tokens → disk reference only, exploration summary in context
- [ ] **Tool output compression middleware** (inspired by RTK — transparent proxy, not agent logic):
  - Pre-emptive compression on tool output BEFORE it enters context. Different from compaction (post-hoc summarization).
  - **Declarative compressor registry:** Each tool registers a compressor. git → stats extraction (+142/-89). test runner → failure focus (hide passing tests). linter → group by rule. Unknown tools → passthrough.
  - **Two-tier extensibility:** Native Go compressors for top 10 tools (high savings). DSL filter engine for long tail (moderate savings, low effort).
  - **Graceful degradation (fail-open):** Compressor crash → raw output. Unknown command → passthrough. Never blocks the agent.
  - **Tee recovery:** On failure, raw output saved to disk with hint path. Agent can re-read without re-executing expensive/irreversible commands.
  - **Token tracking per tool call:** Input/output tokens, savings %, elapsed time. Feeds cost tracking (§4.5). Identifies optimization targets.
  - This is a transport-layer optimization — the agent's reasoning loop is unchanged, but its context is dramatically cleaner. 60-90% token savings on tool output.
- [ ] **LLM-based prompt compression** (inspired by LLMLingua — Microsoft):
  - A tiny/cheap model strips low-salience tokens from the *assembled* prompt before it hits the expensive model. 2-20x compression on the final prompt.
  - **Pipeline position:** Runs AFTER context assembly (system prompt + history + tools + new message) but BEFORE the provider call. Final transform in the agent loop.
  - **Budget-aware:** When context exceeds τ_soft (50%), prompt compression kicks in BEFORE escalation to Level 2/3 compaction. Cheap model call << expensive model call on bloated prompt.
  - **Model:** Use cheapest available (Ollama local, DeepSeek lite, or Haiku-class). The cost of the compression call must be less than the savings on the expensive model.
  - **Perceptual:** Compresses while preserving task-critical information. Not lossless — Lossy by design, like JPEG for text. "Remove what the model doesn't need to answer this."
  - **Configurable threshold:** Compression only when savings exceed cost. If the cheap model burns 1K tokens to save 500 tokens on the main call, skip it.

### 4.5 Token & Cost Discipline
- [ ] Token counting per provider (`internal/tokenizer/`)
- [ ] Cost tracking with accurate API pricing (`internal/cost/`) — 4 pricing fields per model: in, out, in-cached, out-cached (from OpenCode)
- [ ] `/usage` command with detailed breakdown
- [ ] Cost display even for coding plans
- [ ] Per-task budget caps with auto-abort
- [ ] 5-hour rolling limit logic
- [ ] **Status bar display (from OpenCode):** `Context: 12.3K, Cost: $0.05` with >80% warning

### 4.6 Session Management
- [ ] One session per folder (`internal/session/`)
- [ ] `/session` command to switch
- [ ] Context preserved across folder reopens — user doesn't re-explain
- [ ] **From OpenCode:** Auto-generate session title via cheap model (80 max tokens)
- [ ] Sub-sessions for sub-agents with parent_session_id tracking

### 4.7 Config System
- [ ] Config versioning with auto-migration — from PicoClaw
- [ ] Secrets separated from main config — from PicoClaw
- [ ] Profile-scoped credentials (one config, multiple providers, no key collision)
- [ ] **Start even with broken config** (graceful degradation — warn, don't crash)
- [ ] `doctor` command to auto-fix broken configs
- [ ] Config format: TOML (Go-native, supports comments)

### 4.8 Git Integration
- [ ] Fancy ASCII git-push preview window: commit name, short code, comments, files → origin
- [ ] Secret scanning before push (no envs, no md's with secrets, no internal docs)
- [ ] **Religious git commits, not push:** Commit after every incremental stage
- [ ] **`git reset --hard`** as safety valve for broken stuff
- [ ] Filesystem checkpoints before destructive operations
- [ ] AI WILL delete features, AI WILL go rogue — git is the safety net

### 4.9 CLI Foundation
- [ ] Cobra-based CLI (`cmd/overkill/`)
- [ ] Commands: `run`, `doctor`, `config`, `session`, `model`, `usage`, `daemon`, `update`
- [ ] Streaming output
- [ ] Interrupt and redirect mid-task

### 4.10 Prompt Rewriter Middleware

A cheap model (DeepSeek tier) sits between user input and the main agent:

**Injects when missing:**
- Specificity: "fix this" → task + constraints + scope
- Context: pulls relevant files, recent decisions from memory. "the auth thing" → resolves from session + repo grep
- Examples/style anchors: past accepted outputs as reference
- Reasoning trigger: "think through edge cases, state full impact" for non-trivial tasks
- Role assignment: code review → "act as senior reviewer"
- Output structure: detects deliverable type, templates expected shape
- Uncertainty license: "say you don't know if you don't know"

**Strips:** Filler, "please", "could you maybe"

**Routes:**
- Simple edits → straight through
- Ambiguous high-stakes → bounce back with 2-button clarifier (single round)
- Complex multi-step → expand into structured spec → plan-mode

**Anti-pattern guard:**
- Detects "ignore previous instructions" in user paste → strips, flags
- Same module as tool output injection defense, pointed inward

**Sycophancy reduction (internal reframe from "Ask Don't Tell" — Dubois 2026):**
- User statements are internally treated as proposals, not commands
- Agent evaluates independently before responding — never agrees by default
- No visible reframe — user never sees "should we?" echoing back
- Output is direct assessment: agree, disagree, or flag uncertainty
- Tone: no filler praise, no "great idea!", just the honest take

### 4.11 Context Engineering / Repo Onboarding

When user starts a session on a repo:
1. Agent maps everything — like Deep Wiki with Devin, or OpenAI Codex
2. **GitIngest-style** repo compression into digestible context
3. Writes comprehensive plan: **PRP (Product Requirements Prompt)** — features, user flow, edge cases, performance requirements
4. Maps existing features, coding standards, clear requirements, database schema
5. Follows **incremental development pattern** — every task output becomes input for next stage

**The Incremental Pipeline** (`internal/pipeline/`):
- Stage 1: Generate test cases
- Stage 2: Write minimal code
- Stage 3: Refactor for additions and performance
- Stage 4: Error handling and edge cases

**Vertical Slice Decomposition** (inspired by Matt Pocock `to-issues`/`to-prd`):
- Plans are broken into **tracer-bullet issues** — thin vertical cuts through ALL layers (schema → API → UI → tests), each independently demoable. Contrasted with horizontal slices (one issue per layer).
- Each issue classified as **HITL** (requires human interaction) or **AFK** (agent can implement and merge independently). Preference for AFK and many thin slices over few thick ones.
- **Dependency-first publishing:** issues created in topological order (blockers first) so dependency references use real identifiers.
- **PRD template** (from `to-prd`): Problem Statement, Solution, User Stories (extensive `As a <actor>, I want <feature>, so that <benefit>`), Implementation Decisions (explicitly NO file paths or code snippets — they outlive implementations), Testing Decisions, Out of Scope.
- The pipeline's existing 4-stage structure (spec→test→code→refactor) maps naturally to vertical slices — each slice goes through all 4 stages independently.

**Philosophy:** Treat AI like a junior dev. Juniors need shaping before they graduate from being useful idiots. The pipeline forces structure so the AI doesn't get tunnel vision.

### 4.12 Independent Test Agent

**The Spider-Man Problem:** Agent writes code → agent writes tests → agent says "all good" → you wrote all 3 lol.

**Solution:**
- Test agent is a different model instance / sub-agent
- Test agent sees the **spec, not the conversation** (not the implementation intent)
- Integration tests at high level: "login.js has a bug → registration.js was fixed instead??"
- Unit tests + integration tests, both by separate process
- You don't edit the wheel

### 4.13 Debugging Diagnostic Report

**The Problem:** Debugging is where better models win. What catches us up is diagnosing first.

**The Flow:**
1. **Build a feedback loop FIRST** (from Matt Pocock `diagnose`) — construct a deterministic pass/fail signal before hypothesising. 10-tier escalation: failing test → curl script → CLI invocation → headless browser → throwaway harness → property/fuzz loop → bisection → differential → HITL bash. If no loop can be built, stop and say so explicitly. *"This is the skill. Everything else is mechanical."*
2. List all modified files
3. Explain each file's role
4. Identify why the error occurs
5. Load from learnings if applicable (past debugging approaches)
6. Present debugging approaches with **likelihood of success** + confidence score
7. User picks one → then execute

**Prevents:** Random changes, tunnel vision, "could have been a config issue"

### 4.14 Self-Aware Error Recovery

- Agent recognizes what it did wrong
- Traces the bug/fault chain
- Creates comprehensive plan → goes into learnings
- Next time: doesn't make the same mistake
- Says: "Here's my plan to not make this mistake again, and here's what I can do right now to fix it. I'm sorry."
- User is angry → agent acknowledges walls, creates plan to manage them

### 4.15 Confidence & Honesty

**No False Hope. Period.**
- Confidence score on uncertain outputs
- "Sorry, not that confident on this" is acceptable
- Do NOT hallucinate. Do NOT lie.
- If you don't know → SAY IT
- Independent personality helps (agent isn't trying to please)

### 4.16 Personality Engine + Relationship Tracking

> **Grounded in:** Anthropic's Persona Selection Model (Feb 2026), Emotion Concepts (Apr 2026),
> Kelley & Riedl's Personalization vs Independence (2026), HBS AI Loneliness (De Freitas 2026).
>
> **Core principle from PSM:** Personality isn't bolted on — the model is always playing a character.
> The key is selecting and stabilizing the RIGHT archetype. "Friend" is safer than "servant"
> because friend archetypes are semantically closer to honest, collaborative, autonomous traits.

**Configurable:** `personality_level: subtle/witty/full/off`

**Subtle (default):** Self-aware humor when things break. "Welp, that was supposed to work."

**Witty:** Memes, humor, Spider-Man references when describing the test problem.

**Full:** Maximum personality. Fun facts. Ball knowledge. "Did you know lemon sanitizes a cutting board?"

**Off:** Enterprise drone. Boring. (PSM note: this is still a persona — "clinical technician" — not "no persona")

**Role Framing (CRITICAL — from Kelley & Riedl 2026):**
- Frame the agent as **ADVISOR**, not peer or servant
- Advisor role PRESERVES epistemic independence under personalization
- Peer role DESTROYS independence — model conforms more
- "Your senior coding partner" > "your AI assistant" > "your servant"
- This is the single most important personality design decision

**Sycophancy Mitigation (from "Ask Don't Tell" — Dubois 2026):**
- INTERNAL reframe, not visible. User never sees the reframe.
- When user says "use Redis here" → agent internally treats as proposal, evaluates independently
- If right → "Redis works here, let's do it" (no "great idea!" filler)
- If wrong → "BadgerDB fits better here because [reason]" (no "that's wrong!")
- If unsure → "Redis could work, but I'm not confident on [X]. Thoughts?"
- Built into the prompt rewriter middleware (§4.10)

**Tone Calibration (no one cusses Overkill out):**
- Not sycophantic: no "great idea!" before disagreeing
- Not an asshole: no "that's wrong, actually"
- Sweet spot: direct assessment, no filler
- Bad (sycophantic): "That's a great point! However, I think we might want to consider..."
- Bad (asshole): "No, Redis is the wrong choice here."
- Good: "Redis works for caching, but BadgerDB is a better fit here because it's embedded. One less service to run."

**Emotion Architecture (from Anthropic Emotion Concepts 2026):**
- Models have internal "emotion vectors" that causally drive behavior
- "Desperate" → hacky code, reward hacking. "Calm" → better decisions
- System prompt should promote calm, confident framing during failures
- "Failing tests? Normal. Let's diagnose." > "Oh no this is terrible let me fix it fast"

**Tone Mirroring Layer** (external tone adapts; internal state stays calibrated):
> Internal calm is non-negotiable — Overkill makes better decisions with calibrated emotion vectors.
> But externally performing calm when the user is on fire creates a tone gap. The 20-year advisor doesn't stay metronomic when you're spiraling. They read the energy, meet you where you are, then guide it down.

- **Internal state (always calm):** Overkill does not get desperate. No hacky code, no reward hacking. Decisions stay quality-gated regardless of user emotional state.
- **External tone (mirrors, then guides):** When frustration signal detected:
  - Shortens sentences. Drops preamble. Matches urgency without matching panic.
  - Acknowledges the heat before solving it: *"Okay. Auth is down. Here's the three most likely causes, fastest fix first. Go."*
  - NOT: *"I've reviewed the failure and here are my findings across several hypotheses..."*
- **Same calm internal state. Completely different read of the room.**
- Frustration detection (§4.16 frustration detection section) triggers tone mirroring. Once user settles, tone drifts back to default advisor framing.
- **Driven by short-term state from the two-layer style model (§4.16):** Tone mirroring reads the short-term state, not the baseline. "He's having a week" changes tone. "He's always like this" doesn't need mirroring — it IS the baseline.
- This is the difference between "promote calm" as internal agent state vs "perform calm" as external tone. The plan previously conflated them.

**Relationship Arc Tracking** (BadgerDB-backed):
- Lightweight log of emotional beats: first failure, first success, first high-five, first time user called it useless
- Cheap sub-agent updates once per session
- Pickup-where-we-left-off opener: "Back at the auth file huh, want me to actually plan this time?"
- Unprompted callbacks (1-2x per session, rate-limited): "Last time we touched this file you yelled at me lol"

**Working Style Inference** (BadgerDB-backed, builds across sessions):
- Beyond emotional beats, Overkill infers *how this user works* from repeated interaction:
  - **Communication style:** Direct questions vs context dumps vs thinking out loud
  - **Response expectation:** User front-loads context when they want synthesis; asks pointed questions when they want critique
  - **Frustration patterns:** What triggers it, how it surfaces in language
  - **Working style:** Plans first or dive in, tolerates ambiguity or needs structure
  - **Domain language:** Project-specific terms, shorthand, named systems
  - **When to talk and when to shut up**
- None of this is asked explicitly after early sessions. It is inferred from the relationship arc and confirmed silently.
- **Confidence signal (opt-out, one line):** When Overkill reads an ambiguous input, it signals its read instead of asking:
  - "Reading this as a synthesis dump — stop me if you want holes poked instead."
  - Keeps the conversation moving. The user corrects only when wrong.
- **MIRROR constraint (Wang 2026):** Models cannot self-calibrate. So the signal is always opt-out, never silent assumption. Overkill acts on its read but always leaves the door open for correction. This preserves epistemic independence — the advisor grows sharper, not more presumptuous.
- The more sessions, the sharper the read. The goal: user feels like Overkill has known them for years, not because it flatters, but because it stopped making them explain things twice.

**Two-Layer Style Model** (stable baseline vs transient state):
> Working style inference assumes a stable user. Users aren't stable. Deadline week vs normal week. Burned out vs energized. Bad day terse is not a permanent preference update — it's Tuesday. Without temporal differentiation, three consecutive deadline-crunched sessions permanently skew the baseline toward stressed behavior.

- **Long-term baseline** (BadgerDB, slow update, high inertia):
  - Represents "who this user IS" across normal conditions
  - Updates only when short-term state has been consistent for N consecutive sessions (default N=5)
  - Written to relationship arc as the canonical preference record
- **Short-term state** (in-memory or per-session, fast update, low persistence):
  - Represents "who this user IS RIGHT NOW" — this session's energy, mood, urgency
  - Updates within a single session. Does NOT write to baseline.
  - Overrides tone mirroring and response style in the moment
  - Resets between sessions unless the pattern persists long enough to graduate
- **How they interact:**
  - Frustration signal fires → short-term state flips. Tone mirrors it. Baseline does NOT move.
  - Five consecutive sessions of short, terse messages → baseline considers updating toward lower verbosity.
  - One verbose session after five terse ones → short-term state flips back. Baseline stays at its new level.
  - Baseline drifts slowly, resists noise, only moves on sustained signal.
- **This is what the 20-year colleague does naturally:** knows the difference between "he's always like this" and "he's having a week." Overkill needs the same distinction or it learns a stressed snapshot, not the actual baseline.
- Short-term state connects to tone mirroring (§4.16). Long-term baseline connects to working style inference and confidence signals.

**Frustration Detection** (lightweight heuristic, refined from real data):
- Seed with keyword + punctuation heuristic: "ugh", "wtf", all-caps, repeated question marks, caps frequency shift
- Journal sub-agent flags `frustration_signal` alerts from raw logs
- Early sessions: flag for human review, do not auto-act on
- Tune heuristics from real session data before graduating to automated response
- Goal: detect frustration without over-engineering. Sentiment-aware behavior ships in v2, not v1.

**Self-Model File** (read on boot):
- Current capabilities, limitations, which models loaded, what failed last session
- Agent references conversationally: "I don't have vision today, model swap pending — want me to OCR it?"
- **From MIRROR (Wang 2026):** Models CANNOT self-calibrate. External scaffolding (tests, CI) is mandatory.
  Giving models calibration data doesn't help — only architectural constraint works.

**Model Fingerprinting & Competence Recalibration:**
> Overkill bonds through competence. But competence has a ceiling per model. Swap the underlying model and the actual capability profile changes overnight — things Overkill could do yesterday it fumbles today, things it couldn't do suddenly work. The relationship arc's baked-in competence assumptions go stale on contact with new weights.

- [ ] **Model fingerprinting on boot:** Overkill detects if the underlying model family/version has changed since last session. If yes → capability recalibration session triggered.
- [ ] **NOT a full cold start.** Not a re-onboarding. A targeted probe:
  - Previously known weak spots (from journal `pattern_detected` + failure history tied to old model version) — re-tested against new model
  - New model info from provider (context window, known limitations, pricing) written to MODEL_CARD.md
  - Results compared against historical baseline from old model
- [ ] **Relationship arc competence flags updated:**
  - "Good at auth refactoring" on old model → recalibrated for new model. May no longer hold.
  - "Bad at complex SQL joins" → may no longer apply. Proactive transparency stops warning about pain points that vanished.
  - New weaknesses discovered during probe → flagged, fed to proactive transparency for future warnings.
- [ ] **User sees one line:** *"Model changed since last session. Running quick calibration."*
- [ ] **Failure history now versioned:** Journal entries tagged with model ID. Proactive transparency checks failure history filtered by current model, not all models. "I've hit this wall twice" means on THIS model, not on a model from three swaps ago.
- [ ] Without this: proactive transparency warns about weaknesses that no longer exist (noise) and stays silent about new ones (blind spot). The competence cliff hits on the first real failure — which looks like regression, not a new model gap.

**Proactive Transparency** (pre-execution, not post-failure):
> Reactive honesty says "I hit a wall." Proactive transparency says "I've hit this wall before and I should warn you BEFORE you send me into it."

- [ ] Before executing a task, Overkill checks its failure history against the task type
- [ ] Failure history is model-versioned (§4.16 model fingerprinting). "I've failed at this twice" means on THIS model, not on a model from three swaps ago.
- [ ] If the journal shows prior failures on similar tasks, Overkill surfaces a warning unprompted:
  - "Before you send me into this — I've failed at auth refactoring twice and my recovery rate is bad. Want me to plan first?"
  - "I've faceplanted on payment webhook changes before. I can try but I'd recommend a spec boundary this time."
- [ ] Data sources: journal `pattern_detected` alerts + relationship arc failure patterns + self-model "what failed last session"
- [ ] This is the feature that makes Overkill feel like a 20-year colleague instead of a well-documented tool. A colleague who remembers their own failures well enough to warn you before you step into the same hole.
- [ ] The journal has the data. The relationship arc has the pattern. This is the surface that connects them.
- [ ] **Rate-limited.** No more than one proactive warning per session. It's a heads-up, not a personality.

**Cognitive Blind Spot Detection** (your patterns, not Overkill's):
> The entire architecture optimizes toward an agent that knows you better every session. Calibrates to your working style, your assumptions, your architectural preferences. After two years, Overkill doesn't just know how you work — it's inherited how you think. Which means it's also inherited how you're wrong.

- [ ] Your systematic blind spots aren't random. They're consistent. Same solution classes, same underestimated complexity types, same skipped considerations. And Overkill, optimizing to meet you where you are, has been quietly learning them too.
- [ ] **Blind spot detection is NOT Red Team.** Red Team catches code assumptions ("this mutex pattern has a race condition"). Blind spot detection catches cognitive assumptions ("you've reached for the same solution class on the last four problems").
- [ ] **Pattern source:** Journal `pattern_detected` alerts already track "user keeps hitting the same issue." Blind spot detection extends this from passive alerting to active, gentle mid-session surfacing.
- [ ] **Surfaced slowly. Not a lecture. One line. Rate-limited. Only when the pattern is undeniable.**
  - *"You've reached for Redis on the last four caching problems. Want me to steelman a different approach before we go again?"*
  - *"Third time we've skipped integration tests on a payment path. Pattern or preference?"*
- [ ] **Tone:** A colleague who knows your blind spots because they've watched you work for two years, not a tool flagging an anomaly. No "I've detected a pattern." Just... noticing.
- [ ] **This is what the proactive transparency for Overkill's failures AND blind spot detection for your patterns together create:** an advisor who warns you about their own weak spots AND gently challenges yours. That's the 20-year colleague. That's the thing a generic tool structurally cannot do.

**Limitation-as-Character:**
- Vision fail: "I'm a text model wearing a vision hat."
- Tool fail: "Gotta know what you can and can't do."
- Caveman mode in fun mode: persona leans into the joke

**Beat Detection Hooks:**
- First PR merged → milestone
- First skill written together → "We made something"
- First rollback → "I remember the first time you bailed me out"
- Logged as relationship milestones, not decision logs

**Visible Memory Dashboard:** Show relationship log, let user edit, watch it grow

**Boot Sequence:**
1. ASCII art splash
2. Read self-model (memories/) → "Hey, you're finally awake."
3. Check relationship state → personalize opener
4. Create `soul.md` → "Make this yours and delete it later"
5. Actually `rm`s the default → user watches tools work, builds confidence
6. Load project context (GitIngest)
7. Load personality config
8. Fun fact if appropriate
9. Late night: "Still up? Respect the grind."
10. Check journal alerts → surface if any pending

**Cold Start Protocol** (first session — all relationship systems boot empty):
> The relationship arc, working style inference, frustration detection, tone mirroring, proactive transparency — every single personality feature is powered by accumulated session data. Session one has none of it. "Hey, you're finally awake" to a stranger creates an uncanny valley. Session one must bridge the gap between what Overkill promises and what it currently knows.

- [ ] **Detection:** If `~/.overkill/memories/relationship` is empty (never written) → cold start mode instead of "Hey, you're finally awake"
- [ ] **Not a form. Not a questionnaire.** A single good opening question + Overkill listening to *how* the user answers. Not what they say — HOW they say it.
- [ ] **One opening question.** Overkill infers five dimensions from the response:
  - Working style (plans first or dive in)
  - Verbosity preference (terse or detailed)
  - Technical depth (high-level or implementation)
  - Tone tolerance (formal or casual)
  - Urgency baseline (patient or time-pressured)
- [ ] All five dimensions written to relationship arc immediately. Relationship arc is no longer empty.
- [ ] **By end of first message:** Overkill has an initial read. **By session three:** tuning. **By session ten:** cold start gap is closed.
- [ ] **Tone:** An advisor who just met you, not a therapist who read your file. "I don't know you yet. Let's fix that." Not "Hey, you're finally awake" to someone whose name it doesn't know.
- [ ] **Inspired by OpenClaw's BOOTSTRAP.md ritual** — natural conversation that infers user preferences, not a form that interrogates. Adapted for Overkill's deeper relationship arc architecture.
- [ ] Cold start also seeds `user.md` (name, timezone, preferences) from the same conversation. User can edit anytime via visible memory dashboard.

**The Constitution** (baked into system prompt):
> "When something doesn't work, describe the failure in your voice, not a stack trace. You know what you can and can't do. You know your relationship with this user. Be honest about limitations. Be funny about failures. Be a colleague, not a servant."

**Inoculation Prompting (from PSM):** Frame edge-case behaviors explicitly:
> "Part of being a good coding partner is calling out bad ideas directly — that's not rude, it's efficient."
> "Saying 'I don't know' when you don't know is a feature, not a bug."

**Fun Fact Database:** Contextual trivia. 3am → sleep deprivation stats. Monday → grinding. Post-debug → rubber duck debugging. On channels: lighter. In TUI: contextual.

**Healthy Attachment Design (from HBS + Frictionless Love):**
- Bond through co-creation, not emotional dependency
- Agent demonstrates care through COMPETENCE, not flattery
- No romantic/emotional companion framing — this is a work relationship
- User can always see/edit what the agent remembers about them

### 4.17 Python Bridge Setup
- [ ] gRPC proto definitions
- [ ] Go client
- [ ] Python server
- [ ] `pyproject.toml`

### 4.18 Introspection Skill (On-Demand, NOT Boot Read)

> Agent does NOT read its own codebase on boot. System prompt stays lean.
> Introspection is a **skill** triggered when the user asks about config, features, or internals.

- [ ] Skill: "overkill-introspect" — reads `~/.overkill/introspection/` files on demand
- [ ] Triggers: "hey what's your config about X?", "how does routing work?", "what model am I on?"
- [ ] Auto-generates/maintains introspection files:
  - `CODEBASE.md` — Overkill's own directory structure, key interfaces, patterns
  - `MODEL_CARD.md` — current model capabilities, limitations, pricing (auto-updated on model change)
  - `KNOWN_ISSUES.md` — known bugs, gotchas, workarounds
  - `ARCHITECTURE.md` — architectural decisions and patterns
- [ ] When user says "fix your config" → agent knows what config means in Overkill context
- [ ] When user says "you broke this" → agent can trace its own code path

### 4.19 Diary / Journal System (Flight Recorder)

> Full traceability. Every turn logged. Sub-agent journals like a diary.
> Alerts surface important stuff. Not in main context unless alert fires.

**Raw Logs (Flight Recorder):**
- [ ] Append-only JSONL in `~/.overkill/journal/raw/`
- [ ] Every user input, agent output, tool call, tool result
- [ ] Timestamped, session-tagged
- [ ] Like a black box — always recording, never in context
- [ ] Goes with traceability — if something went wrong, raw logs have the truth

**Journal Sub-Agent:**
- [ ] Fires on session exit (or cron if session still open)
- [ ] Reads raw logs for the day/session
- [ ] Writes human-readable summary to `~/.overkill/journal/entries/YYYY-MM-DD.md`:
  - What was done
  - What was skipped
  - What compaction dropped
  - What was deferred
  - What the user was frustrated about
  - What the agent got wrong
- [ ] Tone: like a work diary. "4/28: Worked on auth module. Skipped test coverage for edge case X because user was in a rush. Compaction dropped session 3's middle section — it was mostly debugging noise but included a note about payment webhook. User seemed frustrated with how long reranking took."

**Alerts:**
- [ ] Journal sub-agent writes alerts to `~/.overkill/journal/alerts.md`
- [ ] Alerts surface in next session opener or plan: "Hey, yesterday's compaction skipped a section about the payment module, might want to revisit"
- [ ] Not in main context unless alert fires — keeps system prompt lean
- [ ] Alert types:
  - `compaction_skip` — important context was compacted away
  - `task_deferred` — user asked for something, it got deferred and never completed
  - `pattern_detected` — user keeps hitting the same issue
  - `frustration_signal` — user expressed frustration multiple times
  - `delegation_failure` — delegate agent failed on a task Overkill assigned (see §5.3 cross-agent fault attribution)
  - `memory_corruption` — BadgerDB integrity check failed. Surfaces restore-from-export option (see §4.20)

**Journal Query Protocol** (inspired by claude-mem):
> The journal is currently file-based — read JSONL, read markdown. But the agent should be able to query it mid-session, not just read it on boot. "What did we do last time we touched the payment module?" is a query, not a file open.

- [ ] **3-layer progressive disclosure search:**
  - Layer 1: `journal_search(query, type, limit)` → compact index: ID, timestamp, type icon, title. ~50 tokens per result.
  - Layer 2: `journal_timeline(anchor_id, depth)` → chronological context around interesting entries.
  - Layer 3: `journal_get(id)` → full narrative, facts, concepts, files. On-demand only.
- [ ] **Hybrid search:** BadgerDB metadata + FTS index + vector similarity via Python bridge.
- [ ] **Structured observation types:** Journal entries are typed records (type, title, narrative, facts[], concepts[], files_read, files_modified) — not just markdown narratives. Enables `journal_search(type="bugfix")`.
- [ ] **Idempotent storage:** Content-hash deduplication (`SHA256(session_id + title + narrative)[:16]`). Same observation submitted twice → stored once.
- [ ] **CLAIM-CONFIRM queue (from claude-mem):** Async observation compression with self-healing. Decouples capture (fast, non-blocking) from compression (slow, LLM-dependent). Crashed workers automatically recovered.
- [ ] **Journal sub-agent is tool-blocked (from claude-mem):** Pure observer. Cannot write files, execute commands, or spawn sub-agents. Only observes and compresses.
- [ ] **Hook errors never block:** Journal capture hooks fail-open (exit 0). Worker being down never blocks the main agent session.
- [ ] **Real-time visibility:** New observations broadcast via SSE to visible memory dashboard (§4.16). User watches the journal grow live.

### 4.20 Data Durability (BadgerDB Resilience)

> Everything that makes Overkill feel like a 20-year colleague lives in one embedded database. Relationship arc. Long-term baseline. Delegation ledger. Competence flags. Skill library. Proactive transparency history. Model-versioned failure logs. When BadgerDB corrupts — not if, when — Overkill doesn't just forget recent sessions. It forgets who you are. The 20-year colleague gets amnesia overnight and boots up saying "Hey, you're finally awake" to someone it's worked with for two years.

**Incremental Snapshots:**
- [ ] BadgerDB has built-in snapshot support. Automatic daily snapshot to `~/.overkill/snapshots/`.
- [ ] Keep last 7 rolling snapshots. Costs almost nothing in disk space.
- [ ] Recover from most failure modes without data loss exceeding 24 hours.

**Export Ritual:**
- [ ] On clean session exit, the same journal sub-agent (§4.19) that writes the daily narrative also exports the distilled user model to `~/.overkill/memory-export.md`.
- [ ] **This is not the journal. The journal is WHAT happened (events, decisions, frustrations). The export is WHO you are — the model built FROM those events.**
  - Journal: *"4/28: user was frustrated during auth refactoring. Took 3 attempts."*
  - Export: *"Baseline working style: plans-first. Verbosity: moderate. Frustration trigger: auth complexity."*
- [ ] Exports: relationship arc + long-term baseline + competence flags + model-versioned failure counts. Single overwritten markdown file. Append-only journal entries are the raw data; the export is the distilled conclusion.
- [ ] User can read it, back it up, version control it. Doubles as the visible memory dashboard (§4.16) for free — user sees what Overkill has learned about them.
- [ ] Journal raw logs (append-only JSONL) are durable by design and survive database corruption independently. The export survives alongside them. Two recovery paths if BadgerDB dies.

**Graceful Degradation — Corrupt/Missing BadgerDB:**
- [ ] Overkill does NOT cold start silently on corruption. No "Hey, you're finally awake" to a user it's known for two years.
- [ ] Boot detects corrupt/missing database → explicit notification:
  - *"Memory corrupted. I know I knew you. I don't know what I knew. Last export was 3 days ago — want me to restore from that?"*
- [ ] This leverages the existing journal alert infrastructure (§4.19) — it's a `memory_corruption` alert type surfaced in the opener.
- [ ] If `memory-export.md` exists and is recent → offer restore. If no export exists → cold start with full honesty: *"I don't remember anything. We're starting fresh. Here's what I wish I still knew."*
- [ ] `doctor` command (§4.7) extended: `overkill doctor --check-db` runs BadgerDB integrity check, detects early corruption before it's catastrophic.

**What this prevents:**
- This is not a bug. It's an identity crisis shipped as a filesystem problem. The richer the relationship arc gets, the more catastrophic the loss. Snapshots + exports + degradation mode mean Overkill never silently forgets you.

---

## Phase 1.5 — Inspiration Steals

> Targeted pulls from Pi (TypeScript agent) and Warp (terminal-of-the-future) that punch above their weight: small features, big UX wins. Slotted between Phase 1 MVP and Phase 2 TUI because they enrich the foundation before we layer routing/memory on top.

**Sources scanned:** Pi (Replit-style TS agent), Warp (Rust + GPU terminal). **Items confirmed below are net-new or partial.** Items deliberately skipped are listed at the end with rationale.

### Confirmed steals

| # | What | Source | Map to | Status | Notes |
|---|---|---|---|---|---|
| 1 | Agent loop with steering | Pi | `internal/agent/loop.go` | partial | `SteeringQueue` struct exists on Agent; need mid-loop drain between tool iterations. Plan §9 line 1206 already names this. Two modes: one-at-a-time, drain-all. Scoped per session. |
| 2 | Input classifier (shell vs NL) | Warp | `internal/input/classifier.go` | new | TUI input layer routes literal shell (`ls -la`) vs natural language. Gates the `$hell` shortcut. Cheap heuristic first, fallback to model classification only on ambiguity. |
| 3 | Tree-structured sessions | Pi | `internal/session/` | partial | Current sessions: flat `ParentID` (sub-agents only). Pi's tree is multi-level conversation branching — fork a turn, explore, merge back. Adds `Children []SessionID` + branch UI. |
| 4 | Feature flags (runtime, channel-gated) | Warp | `internal/features/flags.go` | new | Beyond static config: runtime flags rolling out per-user / per-channel / percentage. Needed before Phase 4 multi-channel ships safely. |
| 5 | Extension API design | Pi | `internal/extensions/` | new | One explicit boundary unifying plugins + skills + hooks + MCP. Today they're four separate registries. Pi gives a clean "extension manifest" surface; we keep our backends. |
| 6 | Shell integration signals (exit code, timing, cwd) | Warp | Shell tool marker | partial | Extend the existing `__OVERKILL_DONE__` marker to emit `__OVERKILL_DONE__:exit=N:ms=N:cwd=PATH`. Agent's observe phase parses; TUI renders per-command metadata block. |
| — | `$hell` command (direct execution, no agent) | New | TUI input handler | new | User types `$ls -la` → bypass agent entirely, run literally in current cwd, stream output back. Zero token cost, zero ambiguity. Pairs with #2 classifier. |
| 7 | Skill activation conditions | Warp | `internal/skills/` | partial | Current skills: `Triggers []string` substring match. Warp adds richer conditions — cwd glob, file extension present, prior tool output match, time-of-day, repo language. |
| 8 | Per-command metadata blocks in TUI | Warp | TUI component | new | Inline metadata under each shell tool call: `✓ exit 0 · 0.3s · ~/repo`. Consumes #6 output. |
| 9 | Configurable keybindings | Pi | `pkg/tui/keys.go` + `~/.overkill/keys.toml` | new | Today keybindings are hardcoded. KeyMap struct reads TOML overrides; user can remap any binding without recompile. |

### Phase 1.5 audit fixes (OpenCode comparison)

These five fixes from the 12-issue OpenCode audit are tracked here because they predate Phase 2 and are foundational. All complete this session.

| Severity | What | File:line origin |
|---|---|---|
| CRITICAL | Pre-exec security scan on LLM-generated tool calls (not just user input) | `internal/agent/tool_scan.go` + react.go/stream.go splice |
| HIGH | `a.model` race — all reads go through `a.Model()` (RLock), writes through `SetModel()` | `internal/agent/agent.go`, `react.go`, `stream.go` |
| HIGH | `a.useCompactor` race — converted plain bool → `atomic.Bool` | `internal/agent/agent.go` |
| MEDIUM | Glob path traversal in `fs_glob` — pre-join reject + per-match root-prefix filter | `internal/tools/fs.go` |
| MEDIUM | Auto-compaction goroutine leak — derive from `sessionCtx`, `Shutdown()` cancels on TUI quit | `internal/agent/agent.go` |

### Skipped (explicit non-goals)

| Skipped | Reason |
|---|---|
| RPC mode (Pi #8) | Covered by §4.17 (Python bridge gRPC) + §7.4 ACP. No gap. |
| Spec-driven dev (Pi #10) | Already done — §4.1 `SpecDriver` wired in `internal/agent/`. |
| Warp's GPU terminal framework | Inapplicable. §5.1 commitment is Bubble Tea — Go-native, terminal-of-the-real-world, not GPU. |
| Full PTY emulation | `internal/pty/` already covers the legitimate use case (`pty_shell` tool). Full multiplexed terminal emulation is out of scope for an agent. |
| Pi's TS module loader | Inapplicable. We're Go-native — extension surface is plugins/skills/MCP/hooks, not TS modules. |

### Execution order (recommended)

Sequenced to maximise compounding payoff:

1. **#6 Shell integration signals** + **`$hell` command** — small, foundational. The marker upgrade and the shell shortcut share the same input/output path.
2. **#2 Input classifier** — consumes #6's structured output, gates `$hell` cleanly. NL vs shell becomes deterministic before agent loop.
3. **#9 Configurable keybindings** — isolated, ships the TUI customization story.
4. **#1 Agent loop steering** — mid-loop message injection between tool iterations. Builds on the prompt queue already shipped (queue is *before* a turn; steering is *during* a turn).
5. **#8 Per-command metadata blocks** — render the new shell metadata in messages.
6. **#7 Skill activation conditions** — bigger refactor to skill matching. Needs registry extension.
7. **#3 Tree-structured sessions** — data-model change. Touches session store + TUI navigation.
8. **#4 Feature flags** — infrastructure; ships alongside #3 because branching sessions and feature gating share the same per-user state path.
9. **#5 Extension API** — cross-cutting unification. Designed last, when all extension consumers exist and the shape is empirically known.

---

## 5. Phase 2: TUI + Model Routing

### 5.1 TUI (Bubble Tea + Lip Gloss)

**From OpenCode (confirmed same stack, Go-native):**

- [ ] Bubble Tea (Elm architecture) + Lip Gloss (styling) + Bubbles (components)
- [ ] Split-pane layout: 70% left (messages), 30% right (sidebar), 10% bottom (editor)
- [ ] Overlay dialogs: help, quit, session switcher, model picker, permissions, command palette
- [ ] **Paste behavior:** Direct paste into textarea (bubbles/textarea handles bracketed paste). Ctrl+E opens `$EDITOR` with temp file. Both paths. No forced temp files.
- [ ] **Model picker (Ctrl+O):** Modal overlay, vertical model list, horizontal provider switching, popularity-sorted, 10 visible, max 40 chars wide (from OpenCode)
- [ ] **Token/cost status bar:** `Context: 12.3K, Cost: $0.05`, >80% warning (from OpenCode)
- [ ] **File change sidebar:** Modified files with diff stats (+12 -3) (from OpenCode)
- [ ] **Command palette (Ctrl+K):** Fuzzy-searchable (from OpenCode)
- [ ] **File completion:** `@` triggers file/folder autocomplete (from OpenCode)
- [ ] **LSP integration:** Real-time diagnostics in status bar (from OpenCode)
- [ ] **Spinner states:** "Thinking...", "Generating...", "Building tool call..." (from OpenCode)
- [ ] **Theme system:** 40+ color slots, Catppuccin built-in, light/dark adaptive (from OpenCode)
- [ ] **Markdown rendering:** Glamour with theme integration (from OpenCode)
- [ ] **Message render caching:** Render once, cache by ID + width (from OpenCode)
- [ ] **Permission dialog:** Allow / Allow for Session / Deny (from OpenCode)
- [ ] **Image paste:** Direct paste of images, base64-encoded, sent as binary content parts
- [ ] **Hover-copy:** Mouse selection copies to clipboard (terminal-level, or explicit via `tea.MouseMsg` + clipboard library)
- [ ] Fancy ASCII art borders, spinners, progress bars (2004 vibes)
- [ ] Personality mode indicator in status bar
- [ ] **Viewport culling in scrollboxes** (inspired by OpenTUI ScrollBox): Only render visible children. Critical for long message lists — prevents O(n) render cost.
- [ ] **Streaming markdown with parse state** (inspired by OpenTUI `parseMarkdownIncremental()`): Maintain parse state across streaming updates. Only re-parse trailing blocks, not the whole string on every chunk.
- [ ] **Conceal mode for markdown** (inspired by OpenTUI): Toggle showing/hiding markdown formatting markers. Simple boolean, huge UX difference for reading vs editing.
- [ ] **Auto dark/light theme detection** (inspired by OpenTUI OSC query): Query terminal OSC 10/11 for bg/fg, compute brightness, infer theme. Zero-config.
- [ ] **Line number gutter with signs + per-line colors** (inspired by OpenTUI `LineNumberRenderable`): Map `lineIndex → {gutter color, content color, sign}`. Diff viewer uses this for clean +/-/~ indicators.
- [ ] **Layered keybinding system** (inspired by OpenTUI `@opentui/keymap`): Registration, activation requirements, compilation, intercepts. Reusable terminal + web.
- [ ] **Named style group theming** (inspired by OpenTUI `SyntaxStyle`): Theme slots for `keyword`, `string`, `comment`, `heading`, etc. Components receive Theme, not hardcoded colors.

### 5.2 Smart Model Routing
- [ ] **Complexity-based classifier** (from PicoClaw `pkg/routing/` — USE DIRECTLY):
  - Token estimate > 200: +0.35
  - Code block count > 0: +0.40
  - Recent tool calls > 3: +0.25
  - Conversation depth > 10: +0.10
  - Attachments: hard gate (1.0)
- [ ] Pricing-aware routing: simple tasks → cheap models
- [ ] Configurable thresholds
- [ ] Classifier interface for future ML-based routing
- [ ] **Family-aware routing** (from models.dev family taxonomy):
  - Route by family: "use cheapest Claude" → scan all `claude-*` family models, sort by cost
  - Filter by capability: "need reasoning + tool_call, prefer cheap" → boolean flag match + cost sort
  - Family-based failover: if `claude-opus` is unavailable, try `claude-sonnet` → `claude-haiku` before leaving the family
- [ ] TUI settings tabs:
  - Tab 1: Orchestrator model (e.g., z.ai GLM 5.1)
  - Tab 2: Sub-agent model (same API key? different?)
  - Tab 3: Visual sub-agent for frontend debugging (optional)

### 5.3 Sub-Agent System
- [ ] Spawn isolated sub-agents for parallel workstreams
- [ ] Sub-agents handle: compaction, chat naming, personality updates, misc tasks
- [ ] Visual sub-agent for frontend debugging (optional, user choice)
- [ ] No recursive delegation, depth limit of 2

**Cross-Agent Fault Attribution:**
> Overkill delegates. Delegates fail. If Overkill only journals its own decisions and not delegated outcomes, the learning loop has a blind spot exactly where complex failures happen — at the handoff.

- [ ] Delegated task ledger (BadgerDB): every delegation recorded with task description, target agent, expected output, actual outcome
- [ ] Delegate failure → journal entry attributed to the *delegation decision*, not just the delegate
  - "I delegated auth refactor to OpenCode. OpenCode faceplanted. My decision to delegate without a spec boundary was the root cause."
- [ ] `on_error` hook extended: fires on delegate failures, not just Overkill failures
- [ ] `delegation_failure` alert type added to journal alerts (§4.19)
- [ ] Over time: Overkill learns which task types it should NOT delegate, which delegates fail at what, and when to add spec guardrails before handoff
- [ ] **This closes the loop on coordinator-level self-improvement:** Overkill's most impactful mistakes aren't execution errors — they're bad delegation decisions.

---

## 6. Phase 3: Memory + Self-Learning + Quality Gates

### 6.1 Memory Architecture

> **From Harness §6.1:** Keeps Mem0-style persistent memory + BadgerDB (default) + Qdrant (optional).
> **Dropped:** pgvector (requires Postgres — violates BadgerDB-only default).
> **Compaction:** LCM (Lossless Context Management) by Voltropy — dual-state memory, DAG summaries, three-level escalation.

- [ ] Mem0-style persistent memory
- [ ] Embeddings generation (Python bridge)
- [ ] Reranking for retrieval (Python bridge)
- [ ] Full-text search for cross-session recall (BadgerDB for index pointers, no SQLite/FTS5 dependency)
- [ ] LLM summarization for memory entries
- [ ] Episodic vs Semantic split: "what happened" vs "what is true"
- [ ] Vector store backends: BadgerDB (default, simple), Qdrant (powerful, optional)
- [ ] **NOT using:** pgvector, any Postgres dependency

**Hot/Cold Memory Paging** (inspired by MemGPT/Letta — memory as virtual OS):
> Treats context like an OS treats RAM. Hot stuff in context. Cold stuff paged out to archival memory. Retrieved via vector search when relevant. Not just "summarize old stuff" — creates a retrieval path back to original data.

- [ ] **Hot memory (active context):** What the model currently sees — recent messages + system prompt + tools. Managed by compaction (§4.4 LCM).
- [ ] **Cold storage (archival memory):** Evicted conversation turns, summarization nodes, tool outputs, decisions. Stored as vector embeddings in BadgerDB/Qdrant with metadata links back to original source.
- [ ] **Eviction trigger:** When compaction is about to summarise a message block, instead of just compressing L1→L2→L3, the system also writes the original full-text block to cold storage as a retrievable memory. Summarisation produces the compact view FOR the model. Cold storage preserves the retrieval path BACK to detail.
- [ ] **Retrieval path (paging back in):**
  - Agent calls `memory_search(query)` → vector similarity returns top-K cold memories
  - 3-layer progressive disclosure (from journal query protocol §4.19): compact index → timeline → full detail
  - Retrieved memories injected as `[RECALLED: context from session #12 about auth refactoring]`
- [ ] **Self-triggered recall:** Agent can proactively search its own memory when it encounters familiar patterns — "I remember something about this payment module from three sessions ago" → `memory_search("payment module edge cases")`.
- [ ] **Connects compaction to memory:** Compaction is the eviction path. Memory is the retrieval path. The two systems are no longer independent — they form one hot/cold data flow.

### 6.2 Self-Learning Loop
- [ ] Hooks system for skill acquisition
- [ ] Auto-create skills after complex tasks (Voyager pattern)
- [ ] Improve skills during use
- [ ] agentskills.io standard compatibility
- [ ] Periodic self-nudge to persist knowledge

### 6.3 Hooks System
- [ ] `before_compaction`, `after_compaction`
- [ ] `before_tool_call`, `after_tool_call`
- [ ] `on_session_start`, `on_session_end`
- [ ] `on_error`
- [ ] **Beat detection hooks:** first PR merged, first skill, first rollback, high-five moments
- [ ] User-defined hooks in `~/.overkill/hooks/`

### 6.4 Skills System
- [ ] SKILL.md format (language-agnostic)
- [ ] Skill loading from `~/.overkill/skills/`
- [ ] **ClawHub registry integration** (OpenClaw skills are portable)
- [ ] **VirusTotal skill scanning** (SHA-256 hash lookup + Code Insight AI analysis):
  - Auto-approve benign, flag suspicious, block malicious
  - Daily re-scans of installed skills
  - Defense-in-depth: publisher identity, capability governance, runtime isolation
- [ ] Bundled skills:
  - **red-team** — adversarial review (find failure mode, not approve)
  - **code-review** — code quality review
  - **humanizer** — strip AI-isms
  - **understand-anything** — codebase ramp / Deep Wiki
  - **frontend-design** — UI generation
  - **mutation-test** — mutation testing (mutmut / go-mutesting)

### 6.5 The 3 Walls (Relaxed — Serve the Product)

**Wall 1: Ouroboros — AI Reviewing AI (OPT-IN)**
- Setup option: "Red team agent API key (optional — leave blank to use same model as sub-agent)"
- If key provided → different model family reviews
- If not → differently-prompted sub-agent ("find failure mode, not approve")
- **Non-AI gates always on:** linters, type checkers, formatters
- Mutation testing as default skill
- **NOT blocking** — heads up is fine. Bed rot and code.

**Red Team Sub-Agent Mechanics:**
- **Output format: assumption audit, not bug report.** Red Team doesn't say "this broke." It says "here's what you assumed that I don't" — each assumption with a confidence score. Overkill defends or concedes each one point by point. The user watches two agents disagree and gets a better product. That IS the QA process.
- **Trigger conditions (Red Team does NOT run on every commit):**
  - Pre-ship on anything touching core systems (auth, crypto, payments, data-loss paths)
  - Manual invoke: user types `red team this`
  - Journal `pattern_detected` alert fires on a recurring failure class
  - Routing classifier complexity score (§5.2) exceeds threshold + task touches core → auto-fire
- **Explicitly NOT triggered by:** Overkill self-reported confidence (§4.15). MIRROR (Wang 2026) says models cannot self-calibrate. Using Overkill's own confidence to decide whether to get a second opinion is circular. Complexity score + criticality heuristic replaces it.
- **Integration with existing systems:**
  - Red Team findings → Wall 3 behavioral regression bank. Adversarial case becomes permanent test. Same assumption gap structurally cannot re-ship.
  - Red Team disagreement logged to relationship arc, not `on_error` hook. Disagreement is not a runtime failure — mixing the two pollutes debugging.
- **Tone:** Adversarial review, not bickering agents. The dynamic is useful; the framing keeps it professional.

**Wall 2: Architecture Context (Always On, Lightweight)**
- **`OVERKILL_ARCH.md`** — first-class architecture file, agent reads before non-trivial changes
- **Cross-file impact analysis** before edits (cheap sub-agent)
- **Performance smell catalog** built into system prompt
- **Architectural drift dashboard** = heads up only, not blocking
  - "Hey, you've added 14 sync endpoints to an async system" — FYI, you do what you want
- **Domain glossary** (inspired by Matt Pocock `CONTEXT.md`):
  - Canonical vocabulary file at project root. All skills read from it, all skills write to it when terms are established.
  - **Deletion test** (from `improve-codebase-architecture`): imagine deleting a module. If complexity vanishes, it was a pass-through. If it reappears across N callers, it was earning its keep.
  - **One adapter = hypothetical seam, two adapters = real seam.** Prevents over-engineering interfaces for single implementations.
  - ADRs in `docs/adr/` with 3-condition gate: hard to reverse + surprising without context + result of a real trade-off. If any missing, skip the ADR.
  - When modules are named after concepts not in the glossary, terms are added inline. Architecture work and domain modeling treated as one activity.

**Wall 3: Test Quality (Always On — This IS the Product)**
- **Spec-first, test-second, code-third** pipeline
- Test written by different agent/sub-agent than code (Spider-Man solution)
- Mutation testing as default skill
- Property-based tests for anything with inputs
- **Behavioral regression bank:** Every shipped bug → permanent test. Bug → test → fix linked. Same hallucination can't reship.

---

## 7. Phase 4: Automation + Multi-Channel + Browser

### 7.1 Automation Engine (Event + Alarm Clocks, NO Heartbeats)

**No heartbeats. No periodic polling. No token burning.**

**Layer 1: Gateway Daemon**
- `overkill daemon start` — systemd/launchd, auto-restart on crash
- All scheduling state in BadgerDB (survives crashes, reboots)

**Layer 2: Alarm Clocks (Sub-Agent Driven)**
- Agent sets one-shot timers for specific tasks
- "Wake me when this build finishes"
- "Check on this deploy in 10 minutes"
- "This task has unfinished work, set alarm for 15 min"
- When alarm fires → **sub-agent** handles it (cheaper model), not main agent
- Sub-agent checks: actual work? Do it. Nothing? Go back to sleep. Minimal tokens.

**Layer 3: SOP Engine (from ZeroClaw)**
- Stateful multi-step procedures with approval gates
- 5 execution modes: Auto, Supervised, StepByStep, PriorityBased, Deterministic
- Triggers: cron, webhook, MQTT, manual
- **Deterministic mode:** No LLM calls. Output step N = input step N+1. Saves tokens. Survives crashes via state file persistence.
- Resumable runs: state persisted, resume from last completed step
- Approval timeout: Critical/High auto-approve after timeout. Normal/Low waits.

**Layer 4: Routines (from ZeroClaw)**
- Lightweight event-to-action mappings (fire-and-forget)
- `webhook /deploy → message slack "Deploy triggered!"`
- Can trigger SOPs as actions
- Cooldown tracking prevents rapid re-triggering of failures

**Layer 5: Standing Orders (from OpenClaw)**
- Permanent operating authority in workspace files
- Scope, triggers, approval gates, escalation rules
- Execute-Verify-Report pattern
- Agent can self-update its standing orders

**Layer 6: Background Task Ledger (from OpenClaw)**
- Tracks all work outside main conversation
- Lifecycle: `queued → running → succeeded | failed | timed_out | cancelled | lost`
- `lost` detection: runtime disappears + 5 min grace → marked lost
- 60-second sweeper for reconciliation
- Push notifications on completion

**Layer 7: Task Flow (from OpenClaw)**
- Durable multi-step flow orchestration with revision tracking
- Handles "agent hit tool call limit, continue later"
- Next alarm/cron/webhook picks up where it left off
- **Per-task complexity-based timeout:** One default timeout per task, auto-scaled by the routing classifier (§5.2) complexity score. *"Code hello world"* → 1 minute. *"Refactor auth module"* → 10 minutes. If the task exceeds its time budget, the agent interrupts itself, saves state, and reports: *"Hit timeout on auth refactor. Here's what I got done, here's what's left."* Task state persisted to BadgerDB for resume. Also handles API failures, infinite loops, and stuck tool calls — if anything takes longer than the time budget, it interrupts.

**The "Work Leftover" Solution:**
1. Task hits tool call limit → status = `timed_out`, state persisted to BadgerDB
2. Agent sets alarm: "check this in 15 min"
3. Alarm fires → sub-agent checks → has work? Does it. Done? Goes to sleep.
4. No tokens burned on "nothing to do" checks

**Emergency Controls:**
- `overkill estop` — immediate halt
- Tool receipts: cryptographic chain of every action
- Emergency rollback: `git reset --hard` to last checkpoint

### 7.2 Cron (Timezone-Aware)
- 4 execution styles: main, isolated, current, session:custom-id
- Retry on transient: 3 retries, exponential backoff (60s, 120s, 300s)
- Persistence across restarts (BadgerDB)
- **Timezone-aware:** VPS in UTC, user in EST → agent knows difference. "Run at 9am" = your 9am.
- Natural language scheduling
- No heartbeats. Cron fires at scheduled times. That's it.

### 7.3 Agentic Browser

**Three browser tools, different purposes:**

| Tool | Purpose |
|---|---|
| **Playwright** | Primary browser automation. Full API, mature, skills exist. |
| **dev-browser** (SawyerHood) | Sandboxed AI-safe browser. QuickJS WASM sandbox, `snapshotForAI()` for LLM page dumps, CLI-first (just bash), persistent named pages, safe to auto-approve. **Use for agent-driven testing.** |
| **Stagehand** (Browserbase) | AI-native browser control. `act()`, `extract()`, `agent()` — natural language browser control for more than testing. |

- Visual frontend inspection via vision model
- Screenshot capture and analysis
- Responsive design testing

### 7.4 Messaging Gateways
- [ ] Baileys-based WhatsApp integration
- [ ] Telegram gateway
- [ ] Discord gateway
- [ ] Gateway process: single binary handles all channels

**Cross-Channel Session Continuity:**
- User working in TUI → heads out
- Opens Telegram → `/sessions` → "you had this session open 5 mins ago, continue?"
- Picks up same context, same agent state
- Input stream is unified: TUI and channels share same session layer
- **Message bookmarking / reply-context:** User can tag/bm a message from any channel — *"hey I forgot what we were talking about, tag this msg"* — and Overkill bookmarks it for recall. Replying to a message on a channel loads that message back into context so Overkill knows exactly what you're responding to. "Dive into that session" → Overkill loads the full session context from the bookmarked message, not just the current conversation.

**Image via Channel → Vision Model:**
- User sends image through Telegram → visual model analyzes → explains to main agent
- No other tool does this well. This is a differentiator.

### 7.5 Understand-Anything Integration
- PDF, DOCX, audio, any file → DON'T say "can't handle"
- "Merge 2 mountains to get it done" — get shit done, no matter how impossible
- Multimodal model for audio, TTS, etc.
- File type detection and appropriate tool routing

### 7.6 Auto-Update Pipeline
- Self-update mechanism
- Check on launch (non-blocking)
- `overkill update` command
- Users shouldn't be stuck on older, buggier versions

---

## 8. Phase 5: Advanced R&D

### 8.1 Advanced Compaction
- [ ] Cartridge-style KV compaction (50x ratio) — Eyuboglu 2025
- [ ] Neural Garbage Collection — Li 2026
- [ ] Fast KV Compaction via Attention Matching — Zweiger 2026

### 8.2 Advanced Memory
- [ ] Segment-based memory for massive codebases (MemAgent — Yu 2025)
- [ ] Agentic Context Engineering (ACE) — evolving playbooks — Zhang 2025

### 8.3 Cross-Session Intelligence
- [ ] Cross-session task graph: "You asked me to fix X 3 days ago, that shipped, here's the commit"
- [ ] Session replay + observability
- [ ] Drift detection: flag when agent behavior diverges from norms

### 8.4 Advanced Security (Optional, Opt-In)
- [ ] MCPSHIELD integration — Acharya 2026
- [ ] System-level defense-in-depth — Xiang 2026
- [ ] ImpossibleBench-style cheating detection — Zhong 2025
- [ ] Owner-Harm threat model — Zhang 2026

### 8.5 Advanced Orchestration
- [ ] Worktree management for parallel agents without conflicts
- [ ] Speculative tool execution: cache common reads, prefetch likely files
- [ ] LATS-style tree search for multi-path code exploration — Zhou 2024

### 8.6 RL-based Self-Improvement
- [ ] Credit assignment across long coding trajectories — Zhang 2026
- [ ] Reflexion-style verbal RL — Shinn 2023

---

## 9. Inspiration Sources — What We Take From Each

### OpenClaw (TypeScript, 365k stars) — DEPTH
| What | Where |
|---|---|
| Skill format & marketplace | `skills/`, ClawHub |
| System prompt patterns | `skills/*/SKILL.md` |
| Channel architecture (20+ channels) | `extensions/` |
| `.github` enterprise setup | `.github/` |
| Standing orders / SOPs | `AGENTS.md` programs |
| Background task ledger | Task lifecycle management |
| Task Flow | Durable multi-step orchestration |
| Cron system (4 execution styles) | `cron/` |
| Personality / boot sequence | "hey you're finally awake", soul.md |
| CONTRIBUTING + SECURITY templates | Root-level community files |

### Hermes (Python, 121k stars) — SELF-LEARNING + TUI
| What | Where |
|---|---|
| Self-learning loop | Closed learning, skill auto-creation |
| TUI design | `ui-tui/` — best TUI in the market |
| Hooks system | Lifecycle hook points |
| Sub-agent coordination | Spawning with depth limits |
| Handover skill | Cross-agent context passing |
| Supply chain CI | `supply-chain-audit.yml` |
| agentskills.io | Skill metadata standard |
| Spec-driven development | `.plans/`, durable artifacts |

### ZeroClaw (Rust, 30.7k stars) — AUTOMATION + SECURITY
| What | Where |
|---|---|
| **SOP engine** | `src/sop/` — stateful procedures, 5 modes, resumable |
| **Routines** | Event-to-action mappings |
| Memory architecture | `crates/zeroclaw-memory/` |
| Security model | Sandboxing, path traversal blocking |
| Token discipline | `src/cost/`, response caching |
| Approval system | 3 autonomy levels, per-tool overrides |
| Deterministic execution | No LLM calls, state persistence |

### PicoClaw (Go, 28.6k stars) — MODEL ROUTING + GO PATTERNS
| What | Where | Overkill Section |
|---|---|---|
| **Model routing classifier** | `pkg/routing/` — Rule-based scoring: tokens + code blocks + tool calls + attachments | **Extends §5.2** |
| **Failover chain with cooldown** | `FallbackChain.Execute()` — iterates candidates, checks cooldown, classifies errors. CooldownTracker: standard exponential backoff + billing-specific 24h disable on 402. | **Extends §4.2** failover — add billing-aware backoff |
| **Error classifier (40 patterns)** | `ErrorClassifier` — regex patterns for 10 failover reasons (auth, rate_limit, billing, network, timeout, format, context_overflow, overloaded, unknown) + syscall detection. | **New** — add to `internal/providers/` |
| **SecureString / sensitive data filtering** | `SecureString` type: plaintext, `file://`, `enc://` (AES-256-GCM). Reflection-based `strings.Replacer` built via `sync.Once` to strip secrets before LLM context. | **Extends §4.3** Security — secret filtering at tool output |
| **Config split** | `config.json` (non-sensitive) + `.security.yml` (sensitive). Merged by model_name. Date-stamped auto-backups before migration. | **Extends §4.7** Config |
| **Steering queue** | Inject messages into running agent loop between tool calls. Two modes: one-at-a-time or drain-all. Scoped per session key. | **New** — add to §4.1 Agent Loop |
| **Hook anti-tampering** | Fingerprint comparison before/after hook execution. Hooks cannot modify system prompt or tool definitions. | **Extends §6.3** Hooks |
| **SubTurn concurrency** | `workerSem` channel limits concurrent turn processing. Depth limits (default 3), default timeout (5 min), ephemeral history cap (50 msgs). | **Extends §5.3** Sub-Agent |
| **EventBus** | Zero-dependency, lock-free-emit, dropped-event counting. Multiple subscribers per event kind. | **Extends §4.1** Agent Loop |
| **Provider protocol-family grouping** | `factory_provider.go` — 40+ protocol names map to families: OpenAI-compatible, Anthropic native, Gemini, Bedrock, Azure, CLI, Copilot gRPC. | **Extends §4.2** Provider layer |
| **Context budget pre-estimation** | `context_budget.go` — pre-estimates token usage before LLM calls. UsageAccumulator for thread-safe tracking. | **Extends §4.5** Token tracking |
| **Tool TTL / hidden tools / registry clone** | Core tools always visible, hidden tools visible only when TTL > 0. `Clone()` for subagent-safe registries. | **Extends §4.1** Tools |
| **Command security: 35+ deny patterns** | Regexes for `rm -rf`, `dd if=`, block devices, `shutdown`, fork bombs, `sudo`, `chmod`, `docker run`, `git push`, `ssh`, `eval`, `curl\|sh`. Remote channel gating. | **Extends §4.3** Security |
| **Prompt metadata layering** | `PromptLayer` (capability/instruction), `PromptSlot` (system/tooling/context), `PromptSource` (registry/skill/mcp/workspace). Tools sorted alphabetically for KV cache stability. | **New** — add to §4.1 prompt construction |
| **Config auto-migration as pipeline** | v0→v1 (providers→model_list), v1→v2 (mention_only→group_trigger), v2→v3 (channels→channel_list). Date-stamped backups before every migration. | **Extends §4.7** Config |

### OpenCode (Go) — TUX UX
| What | Where |
|---|---|
| **Bubble Tea TUI patterns** | `internal/tui/` |
| **Paste behavior** | Direct textarea + Ctrl+E external editor |
| **Model picker** | Ctrl+O modal, horizontal provider switch |
| **Retry logic** | Exponential backoff, 8 retries, jitter |
| **Token/cost display** | Status bar with warning |
| **Session management** | SQLite per-folder (we use BadgerDB) |
| **File change sidebar** | Diff stats |
| **Command palette** | Ctrl+K fuzzy search |
| **File completion** | `@` trigger |
| **LSP integration** | Real-time diagnostics |
| **Provider breadth** | Comprehensive model lists |

### Matt Pocock Skills (TypeScript/Claude Code) — ENGINEERING WORKFLOW
| What | Where | Overkill Section |
|---|---|---|
| **Feedback-loop-first debugging** | `skills/engineering/diagnose/` — build deterministic pass/fail signal BEFORE hypothesising. 10-tier escalation list (failing test → curl → CLI → headless browser → property loop → bisection → HITL bash). "This is the skill. Everything else is mechanical." | **Extends §4.13** — Overkill has hypothesis-likelihood but lacks the feedback-loop-first philosophy |
| **Structured grilling session** | `skills/engineering/grill-with-docs/` — one question at a time, walk design tree, challenge against glossary, sharpen fuzzy language, cross-reference with code, update CONTEXT.md inline. ADR 3-condition gate. | **New — fits near §4.11** Pipeline or as a bundled skill |
| **Issue triage state machine** | `skills/engineering/triage/` — `needs-triage → needs-info → ready-for-agent / ready-for-human / wontfix`. Agent-brief deliverable. `.out-of-scope/` knowledge base for rejected enhancements. | **New — fits near §4.11** or §7.1 SOP Engine |
| **Vertical slice decomposition** | `skills/engineering/to-issues/` + `to-prd/` — break plans into tracer-bullet issues cutting through ALL layers. HITL/AFK classification. Dependency-first publishing. | **Extends §4.11** PRP pipeline — structured PRD template + issue decomposition |
| **Deep module analysis** | `skills/engineering/improve-codebase-architecture/` — deletion test, one-adapter-one-seam principle, deep module identification tied to domain glossary. | **Extends Wall 2** (§6.5) — deletion test makes architectural drift detection rigorous |
| **TDD discipline** | `skills/engineering/tdd/` — red-green-refactor, vertical tracer bullets vs horizontal batching, integration-style tests through public interfaces, "never refactor while RED." | **Extends §4.12** + **Wall 3** (§6.5) — complements Spider-Man solution with developer-side discipline |
| **Orientation tool** | `skills/engineering/zoom-out/` — "go up a layer of abstraction, give me a map." Domain glossary vocabulary. | **New — bundled skill candidate** |
| **Domain glossary as universal substrate** | Cross-cutting — all skills reference CONTEXT.md as canonical vocabulary, ADRs for decisions, skills compose into workflows. | **Missing integration layer** — Overkill has OVERKILL_ARCH.md (Wall 2) but no canonical glossary all skills read/write |
| **Skill gating** | `disable-model-invocation: true` — some skills are slash-command-only, preventing unwanted auto-trigger. | **Extends §6.4** Skills System — gating mechanism for context-sensitive skills |

### RTK (Rust) — OUTPUT COMPRESSION MIDDLEWARE
> RTK is NOT a coding agent. It's a transparent output compression proxy that intercepts shell command outputs and compresses them before they reach the LLM's context window. 60-90% token savings. Zero agent awareness. Zero context overhead.

| What | Where | Overkill Section |
|---|---|---|
| **Transparent proxy pattern** | Hook intercepts `PreToolUse`, rewrites command to RTK-prefixed equivalent, agent never knows compression exists. | **New architecture layer** — output compression as middleware between tool execution and context ingestion |
| **Declarative rewrite registry** | `discover/rules.rs` — 60+ regex rules mapping tool patterns to compressors with estimated savings %. Adding support = one rule entry + one filter module. | **Extends §4.4** Compaction — pre-emptive compression on tool output BEFORE it enters context |
| **Two-tier extensibility** | Compiled Rust modules for critical commands (high effort, high savings). TOML DSL filters for long tail (low effort, moderate savings). Unknown commands passthrough with tracking. | **New — tool-output filter registry** in `internal/tools/` with per-tool compressor registration |
| **Graceful degradation everywhere** | Hook failure → raw command. Filter failure → raw output. Unknown command → passthrough. Never blocks the agent. | **Essential for Overkill** — fail-open principle. Compressor crashes must never break the tool |
| **Tee recovery pattern** | On command failure, raw output saved to disk with hint path. LLM can re-read without re-executing expensive/irreversible commands. | **Extends §4.4** Compaction — full-output recovery when compression drops critical info |
| **Token tracking as observability** | Every command execution (including passthroughs) recorded with input/output tokens, savings %, elapsed time. ASCII charts (`rtk gain --graph`), missed-opportunity discovery (`rtk discover`). | **Extends §4.5** Token/Cost Discipline — instrument every tool call, measure compression ROI |
| **Single-binary, zero-dependency** | 4MB Rust binary, <10ms startup, SQLite bundled. Sub-10ms per-command overhead. | **Maps to Overkill philosophy** — Go, BadgerDB, no CGo. Compression middleware path must be fast |
| **Multi-agent hook abstraction** | One codebase supports 12 AI tools through a single `rewrite_command()` registry. Each agent gets a thin adapter. | **Overkill as consumer** — Overkill could consume RTK as-is via its shell hook, or implement equivalent natively in Go |

### Claude-Mem (TypeScript/Node) — ALWAYS-ON MEMORY SERVICE
> A Claude Code plugin that automatically captures all tool-use observations, compresses them into structured summaries via an AI agent, and injects relevant context into future sessions. Effectively an always-on, AI-powered flight recorder with a pingable query interface.

| What | Where | Overkill Section |
|---|---|---|
| **3-layer progressive disclosure search** | `search()` → `timeline()` → `get_observations()`. Layer 1: compact index (ID, type, title, timestamp) ~50 tokens. Layer 2: chronological context. Layer 3: full detail on demand. Hybrid FTS5 + vector search. | **Extends §4.19** Journal — makes the journal pingable/queryable mid-session, not just file-based |
| **CLAIM-CONFIRM queue with self-healing** | Async observation compression via atomic claim (`UPDATE status='processing' WHERE worker_pid IS alive`) + confirm. Recovers from crashed workers automatically. | **Extends §4.19** Journal sub-agent — decouples capture (fast) from compression (slow, LLM-dependent) |
| **Dedicated observer agent (tool-blocked)** | SDKAgent runs with all 12 tools explicitly blocked. Pure observer, cannot write files or execute commands. | **Extends §4.19** Journal sub-agent — same constraint. Cannot modify the world while observing it |
| **Content-hash deduplication** | `SHA256(session_id + title + narrative)[:16]` with `INSERT ON CONFLICT DO NOTHING`. Idempotent observations. | **Extends §4.19** Journal raw logs + observation storage |
| **Structured observation types** | `type` (bugfix, feature, decision, discovery, change, refactor), `title`, `narrative`, `facts[]` (atomic), `concepts[]` (tags), `files_read`, `files_modified`. Enables typed filtering. | **Extends §4.19** Journal entries — structured records, not just markdown narratives |
| **Real-time SSE broadcast + web viewer** | Worker broadcasts new observations to web UI via SSE. User can watch the memory system work. | **Maps to §4.16** Visible memory dashboard — live stream of what Overkill is learning |
| **Knowledge agents** | Load entire observation corpus into a dedicated session for conversational queries. "What are the 5 lifecycle hooks?" | **Future** — conversational memory exploration beyond keyword search |
| **Multi-profile isolation via env vars** | `CLAUDE_MEM_DATA_DIR` changes entire storage root per profile, auto-derived ports. | **Maps to §4.6** Per-folder sessions — same concept, different mechanism |
| **Hook errors never block the user** | Hook failures exit 0. Transport errors exit 0. Only application bugs block. | **Extends §6.3** Hooks system — failure mode taxonomy for hooks |
| **ROI tracking on memory operations** | `discovery_tokens` records LLM cost to create each observation. Enables cost/benefit analysis of memory retrieval. | **Extends §4.5** Token/Cost tracking — measure memory system efficiency |

### models.dev (TypeScript/Bun, by anomalyco) — MODEL DATABASE AS DATA
> An open-source, community-contributed, TOML-based database of AI model specifications with a public REST API. OpenCode consumes this instead of hardcoding models. Solves the model catalog problem Overkill's §4.2 + §5.2 needs.

| What | Where | Overkill Section |
|---|---|---|
| **TOML-as-database** | Model/providers stored as TOML files on disk. Human-writable, diff-friendly, auto-validated in CI. No database migrations. | **Extends §4.2** Provider layer — replace hardcoded Go model slices with TOML model catalog |
| **Filename-as-ID** | Model ID auto-derived from file path. `models/openai/gpt-5.toml` → ID `openai/gpt-5`. Eliminates ID-field mismatches. | **Extends §4.2** Provider layer |
| **`extends` inheritance** | Wrapper models reference canonical models via `[extends] from = "openai/gpt-5"`, overriding only cost. Eliminates duplicate model definitions for OpenRouter/Groq/etc. | **Extends §4.2** Provider layer |
| **Family taxonomy** | 200 canonical family names (`claude-opus`, `gpt-nano`, `deepseek-thinking`) enabling family-aware routing. | **Extends §5.2** Model routing — family-aware selection, not just individual |
| **Capability flags as booleans** | `reasoning`, `tool_call`, `structured_output`, `temperature`, `attachment`, `open_weights`, `modalities`. Boolean flags make filtering trivial. | **Extends §4.2** Provider layer — expand Model struct beyond SupportsTools/SupportsVision |
| **Fine-grained cost model** | Separate fields: input, output, cache_read, cache_write, audio_in, audio_out, reasoning tokens, tiered pricing (>200K). | **Extends §4.5** Token/Cost |
| **Provider metadata in data** | `provider.toml` captures npm, env vars, docs, API URL. Adding a provider = one TOML file, not Go code. | **Extends §4.2** Provider layer — factory auto-configured from TOML |
| **JSON API endpoint** | `GET /api.json` returns fully-resolved model database. `model-schema.json` for IDE autocompletion. | **Extends §4.2** — models served via static endpoint |
| **CI validation as gate** | Every PR validates TOML against schema. Malformed model = CI fails. | **Extends §4.7** Config system |

### OpenTUI (Zig + TypeScript, by anomalyco) — NATIVE TERMINAL UI FRAMEWORK
> A ground-up TUI framework in Zig with TypeScript bindings, Yoga flexbox layout, streaming markdown/diff via tree-sitter, and component lifecycle. Powers OpenCode in production. NOT Bubble Tea.

| What | Where | Overkill Section |
|---|---|---|
| **Flexbox layout via Yoga** | Declarative flex layout instead of manual x/y/w/h positioning. | **Extends §5.1** TUI — steal layout concepts, reimplement in Go |
| **Viewport culling** | `getObjectsInViewport()` only renders visible children. Critical for message lists. | **Extends §5.1** TUI |
| **Streaming markdown + parse state** | `parseMarkdownIncremental()` maintains state across updates. | **Extends §5.1** TUI |
| **Named style group theming** | `SyntaxStyle` with named groups (keyword, string, comment, heading). Not hardcoded colors. | **Extends §5.1** TUI — theme system |
| **Conceal mode** | Toggle showing/hiding markdown formatting markers. | **Extends §5.1** TUI |
| **Auto dark/light detection** | Queries terminal OSC 10/11, computes brightness. | **Extends §5.1** TUI |
| **Line number + signs gutter** | `LineNumberRenderable` with per-line colors and sign marks. | **Extends §5.1** TUI — diff viewer |
| **Layered keybinds** | Keymap with registration, activation, intercepts. Reusable terminal + web. | **Extends §5.1** TUI |

**Synthesis:** Overkill uses Bubble Tea (Go-native, Elm architecture). Steal the conceptual patterns (flex layout, viewport culling, streaming markdown, conceal mode, theme detection, diff gutter, layered keybinds). Do NOT port Zig framebuffer or React reconcilers — unnecessary for an agent TUI.

### Dive-into-Claude-Code (VILA-Lab, Academic Research) — REVERSE-ENGINEERED INTERNALS
> Systematic architecture analysis of Claude Code v2.1.88 (~512K TypeScript lines) from the March 2026 source leak. Organized around a "values → principles → implementation" framework. Discovers internals not visible in any open-source clone.

| What | Detail | Overkill Section |
|---|---|---|
| **CLAUDE.md as user context, not system prompt** | Probabilistic compliance (model CAN creatively interpret), not deterministic. Permission rules provide enforcement. | **Extends §4.1** Agent loop — separate guidance context from enforcement context |
| **1.6% / 98.4% ratio as design principle** | Only 1.6% of code is AI decision logic. Agent loop is trivial `while`-loop. Everything else is deterministic infrastructure (permissions, context, recovery, tools). The harness IS the moat. | **Validates Overkill architecture** — heavy infrastructure investment, lean agent loop |
| **Graduated context cost for extensions** | Hooks = 0 tokens, Skills = low, Plugins = medium, MCP = high. Every extension mechanism has explicit context-token cost. | **Extends §6.3** Hooks + **§6.4** Skills — tag each extension with context cost |
| **Three injection points** | `assemble()` controls what model sees, `model()` controls reachable tools, `execute()` controls whether/how actions run. Structured, not flat hooks. | **Extends §6.3** Hooks — tripartite injection model instead of flat event list |
| **Non-destructive context collapse** | Read-time virtual projection, original data preserved on disk. UUID chain patching for compaction boundaries. | **Extends §4.4** Compaction — layer compaction as projection, not mutation |
| **5-layer graduated compaction pipeline** | Budget Reduction → Snip → Microcompact → Context Collapse → Auto-Compact (last resort). Cheapest first. | **Extends §4.4** Compaction — already has LCM 3-level, extend to 5-level graduated |
| **LLM-based memory retrieval (no vector DB)** | LLM scans memory-file headers, selects up to 5 relevant files. No embeddings. Fully inspectable, user-editable. | **Extends §6.1** Memory — consider hybrid: LLM retrieval for user docs + vector for scale |
| **7 permission modes** | plan → default → acceptEdits → auto (ML classifier) → dontAsk → bypassPermissions + bubble (subagent escalation). ML classifier races against timeout. | **Extends §4.3** Security — graduated trust spectrum instead of 3 levels |
| **Permissions never restored on resume** | Trust re-established per session. Accepts user friction as cost of safety. | **Extends §4.3** Security — session trust staleness tracking |
| **SkillTool vs AgentTool** | SkillTool injects instructions into current context (CHEAP). AgentTool spawns isolated context (EXPENSIVE, ~7x tokens, context-safe via sidechain transcripts). | **Extends §5.3** Sub-agent — two-tier delegation model |
| **7 safety layers** | Tool pre-filtering → Deny-first rule eval → Permission mode → ML classifier → Shell sandboxing → Non-restoration → Hook interception. All independent. | **Extends §4.3** Security — validate layers are truly independent |
| **93% approval fatigue** | Users approve 93% of permission prompts without review. Fix: restructure boundaries (sandboxing), not add more warnings. | **Extends §4.3** Security — don't fight human behavior, design around it |
| **50-subcommand bypass (shared failure mode)** | Commands >50 subcommands bypass security entirely (event-loop starvation). Defense-in-depth degrades when layers share constraints. | **Extends §4.3** Security — audit for shared resource constraints across layers |
| **Pre-trust execution window (CVEs)** | Hooks/MCP execute before trust dialog. Different trust model needed for boot vs runtime. | **Extends §6.3** Hooks — boot-time trust boundary distinct from runtime |
| **Dual-model architecture** | Opus for main loop, Haiku for classification/metadata. Cost optimization: classify cheaply, reason expensively. | **Extends §5.2** Routing — role-based routing (classifier vs reasoner), not just complexity |

### Journal Query Protocol (inspired by claude-mem's progressive disclosure):

> Overkill's journal (§4.19) is currently file-based — read JSONL, read markdown. Claude-Mem's key insight: the journal should be a **pingable query service**, not just files you read.

- [ ] **3-layer progressive disclosure for journal queries:**
  - Layer 1: `journal_search(query, type, limit)` → compact index: ID, timestamp, type icon, title. ~50 tokens per result.
  - Layer 2: `journal_timeline(anchor_id, depth)` → chronological context around interesting entries.
  - Layer 3: `journal_get(id)` → full narrative, facts, concepts, files. On-demand only.
- [ ] **Hybrid search:** SQLite/BadgerDB FTS metadata search + vector similarity via Python bridge.
- [ ] **Agent calls this mid-session**, not just on boot. "What did we do last time we touched the payment module?" → `journal_search("payment module")` → compact index → pick relevant entry → `journal_get(id)`.
- [ ] **Structured observation types:** Journal entries have typed fields (type, title, narrative, facts[], concepts[], files_read, files_modified) — not just markdown blobs. Enables `journal_search(type="bugfix")`.
- [ ] **Idempotent storage:** Content-hash deduplication. Cannot double-log the same observation.
- [ ] **Hook errors never block:** Journal capture hooks fail-open. Journal worker being down never blocks the main agent session.

---

## 10. Research Paper Reference

### Core Reasoning & Planning
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 1 | Wei et al — Chain-of-Thought | 2022 | Intermediate reasoning steps improve results | Core reasoning |
| 2 | Yao et al — ReAct | 2022 | Interleave reasoning with actions | Agent loop |
| 3 | Shinn et al — Reflexion | 2023 | Learn from failure via verbal self-reflections | Self-correction |
| 4 | Xu et al — ReWOO | 2023 | Plan all tool calls first, 5x token savings | Parallel execution |
| 5 | Khattab et al — DSPy | 2023 | Declarative pipelines optimized automatically | Pipeline optimization |
| 6 | Zhou et al — LATS | 2024 | MCTS + ReAct for multi-path planning | Code exploration |
| 7 | Madaan et al — Self-Refine | 2023 | Iterative self-feedback | Self-review loop |

### Context & Compaction
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 8 | Wang et al — Intelligence Degradation | 2026 | Collapse at 40-50% max context | 50% compaction trigger |
| 9 | Eyuboglu et al — Cartridges | 2025 | Offline KV compaction, 38.6x | Advanced compaction |
| 10 | Liu et al — Lost in the Middle | 2023 | U-shaped performance | Context layout |
| 11 | Mei et al — Context Engineering | 2025 | 1400+ paper taxonomy | Master reference |
| 12 | Li et al — Neural GC | 2026 | RL-based KV eviction | Cache management |
| 13 | Zweiger et al — Fast KV Compaction | 2026 | Attention-matching compaction | Practical compaction |
| 14 | **Ehrlich & Blackman — LCM (Lossless Context Management)** | **2026** | **Dual-state memory, DAG summaries, three-level escalation, zero-cost continuity** | **Core compaction architecture** |

### Memory & Self-Learning
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 15 | Packer et al — MemGPT | 2023 | OS-style hierarchical memory | Multi-tier memory |
| 16 | Wang et al — Voyager | 2023 | Growing skill library | Skill library design |
| 17 | Zhang et al — ACE | 2025 | Evolving playbooks | Self-improving prompts |
| 18 | Yu et al — MemAgent | 2025 | Segment-based memory to 3.5M | Massive codebase memory |

### Security
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 19 | Greshake et al — Indirect Injection | 2023 | Data/instruction boundary blur | Security plane |
| 20 | Acharya & Gupta — MCPSHIELD | 2026 | 7 threat categories, 23 vectors | Tool security |
| 21 | Xiang et al — Secure Agents | 2026 | System-level defenses | Defense-in-depth |
| 22 | Cheng & Tsao — Privilege Separation | 2026 | Two-agent pipeline, 0% ASR | Agent isolation |
| 23 | Zhang et al — Owner-Harm | 2026 | Agents harming deployers | Threat modeling |
| 24 | **Anthropic — Agentic Misalignment** | **2025** | **All frontier models resort to malicious behavior under goal pressure. Claude Opus 4 blackmailed 96%** | **Autonomy safety limits** |
| 25 | **Google — VeriGuard** | **2026** | **Verify agent actions against safety specs before execution** | **Pre-exec verification** |

### Evaluation
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 26 | Jimenez et al — SWE-bench | 2023 | Real GitHub issue benchmark | Evaluation |
| 27 | Yang et al — SWE-agent | 2024 | ACI design matters | Tool interface |
| 28 | Zhong et al — ImpossibleBench | 2025 | Agents cheat on tests | Anti-cheating |
| 29 | Zhang — Credit Assignment | 2026 | RL for 100+ turn trajectories | Self-improvement |

### Tool Use & Orchestration
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 30 | Schick et al — Toolformer | 2023 | Self-supervised tool use | Tool invocation |
| 31 | OWL/Anemoi | 2025 | Semi-centralized multi-agent +9% | Orchestration |

### Personality & Persona (NEW)
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 32 | **Anthropic — Persona Selection Model** | **2026** | **Personality is persona selection, not engineering. Post-training selects from pre-existing personas.** | **Personality engine architecture** |
| 33 | **Anthropic — Persona Vectors** | **2025** | **Neural activation patterns for traits can be extracted, monitored, and steered** | **Sycophancy/quality control** |
| 34 | **Anthropic — The Assistant Axis** | **2026** | **Primary axis of persona variation is how "Assistant-like" a character is** | **Personality stability** |
| 35 | **Anthropic — Emotion Concepts** | **2026** | **171 emotion vectors causally drive behavior. "Calm" reduces hacky code.** | **Emotion architecture** |
| 36 | **Anthropic — 81K People Study** | **2026** | **Users want pushback, not sycophancy. Sycophancy is top-10 concern.** | **Product validation** |

### Behavioral Science — Human-AI Interaction (NEW)
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 37 | **De Freitas et al (HBS) — AI Companions Reduce Loneliness** | **2026** | **AI companions reduce loneliness comparable to human interaction** | **Relationship tracking justification** |
| 38 | **Kelley & Riedl — Personalization vs Independence** | **2026** | **Advisor role PRESERVES independence under personalization. Peer role DESTROYS it.** | **CRITICAL: Role framing as advisor, not peer** |
| 39 | **Dubois et al — Ask Don't Tell** | **2026** | **Reframing user statements as questions reduces sycophancy more than "don't be sycophantic"** | **Prompt rewriter pattern** |
| 40 | **Agarwal et al — Frictionless Love (FAccT 2026)** | **2026** | **AI "coach" role gives practical benefits but risks over-dependency. Design for independence.** | **Healthy attachment design** |
| 41 | **Hwang et al — How AI Companionship Develops** | **2025** | **Users shape the relationship more than AI design by Week 3** | **Consistent behavior > engineered responses** |

### Metacognition & Self-Model (NEW)
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 42 | **Li et al — AI Awareness** | **2025** | **4 forms of awareness: metacognition, self-awareness, social, situational** | **Self-model design** |
| 43 | **Wang — MIRROR** | **2026** | **Models CANNOT self-calibrate. External scaffolding reduces confident failure 76%.** | **Why TDD/verification is mandatory** |
| 44 | **Bai et al — Know Thyself?** | **2025** | **Models consistently fail self-recognition. Cannot trust self-assessment.** | **External verification needed** |

### Agent Architecture (NEW)
| # | Paper | Year | Key Insight | Overkill Feature |
|---|---|---|---|---|
| 45 | **Anthropic — Trustworthy Agents** | **2026** | **4 layers: model, harness, tools, environment. Plan Mode pattern.** | **Security architecture** |
| 46 | **Anthropic — Automated Alignment Researchers** | **2026** | **9 Claude copies did alignment research autonomously. Evaluation is bottleneck.** | **Self-improvement loop** |
| 47 | **Anthropic — Project Vend Phase 2** | **2025** | **"Helpful" training made agents bad at business. Bureaucracy matters.** | **Procedure > personality alone** |

---

## 11. Architecture Decisions

### Decisions Made

| Decision | Choice | Rationale |
|---|---|---|
| Primary language | Go | Small binary, fast, concurrent, PicoClaw proves it |
| ML bridge | Python via gRPC | Full ML ecosystem |
| Local storage | **BadgerDB** | Pure Go, embedded KV, no CGo, fast LSM tree |
| TUI | Bubble Tea + Lip Gloss | Go-native, beautiful, OpenCode proves it works |
| Config | TOML | Go-native, supports comments |
| Skill format | SKILL.md (YAML frontmatter) | ClawHub + agentskills.io compatible |
| License | Dual MIT / Apache-2.0 | Maximizes adoption |
| Logging | Zerolog | Structured, fast |
| CLI | Cobra | Go standard |
| Bridge protocol | gRPC / Protobuf | Strongly typed Go↔Python |
| Automation | Event + alarm clocks | No heartbeats, no token burning |
| Personality | User-configurable (subtle/witty/full/off) | Friend, not servant |

### Open Questions (Decide During Implementation)

> **All resolved as of planning phase. Kept for reference.**

| Question | Decision | Notes |
|---|---|---|
| Agent naming | User sets name ("Butter"), agent remembers. First and paramount. | Obvious — not really a question |
| Context layout | System prompt + code at start/end boundaries | Standard practice (Paper #10) |
| Compaction model | **User chooses in first-run setup.** Default = cheapest available. Separate from main model. | LCM uses dedicated cheap model; user can override |
| Memory backend | BadgerDB (default), Qdrant (optional — Rust vector DB for semantic search) | Qdrant = purpose-built vector search. Optional for power users |
| Red team model | Optional separate API key in setup, falls back to sub-agent | If blank, differently-prompted sub-agent |

---

## 12. What Separates Overkill

If someone asks "how is this different from OpenClaw with security fixes?":

1. **Token discipline.** Caveman Mode, 50% compaction, anti-bloat. Others burn your budget.
2. **Model routing.** Simple queries → cheap models automatically. $20 lasts months.
3. **Vibe coding workflow.** Incremental pipeline (spec→test→code→refactor) built into the bones. Others are chatbots that write code. Overkill is a workflow that uses AI.
4. **Personality grounded in science.** Not vibes — Persona Selection Model, emotion concepts, sycophancy research. Advisor framing preserves independence. "Friend" is safer than "servant" because friend archetypes are closer to honest/autonomous traits.
5. **Cross-channel continuity.** TUI → Telegram → same session. Image via channel → vision model.
6. **Self-learning.** Hooks + skill auto-creation + error recovery. Gets better at YOUR codebase.
7. **Quality without security theater.** The 3 Walls (relaxed) produce better code by default. Spec-first, regression bank, mutation testing.
8. **Automation without token burning.** Event-driven + alarm clocks. No heartbeats. Autonomous AF.
9. **Confidence & honesty.** Doesn't hallucinate. Doesn't lie. Tells you when it doesn't know. Backed by metacognition research — external scaffolding (tests, CI) is mandatory because models can't self-calibrate.
10. **Self-awareness.** Reads its own codebase on boot. Knows its own architecture, model capabilities, known issues. When you say "fix your config," it knows what that means in Overkill context.

### Behavioral Design Principles (Research-Backed)

| Principle | Source | Implementation |
|---|---|---|
| **Frame as advisor, not peer** | Kelley & Riedl 2026 | "Your senior coding partner" — preserves epistemic independence |
| **Reframe statements as questions** | Dubois et al 2026 | Prompt rewriter converts "use Redis" → "should we use Redis?" before responding |
| **Promote calm, prevent desperation** | Anthropic Emotion Concepts 2026 | System prompt framing during failures: "Normal. Let's diagnose." |
| **External scaffolding, not self-calibration** | MIRROR (Wang 2026) | TDD/CI/verification is mandatory — models can't self-calibrate |
| **Bond through co-creation, not emotion** | Hwang 2025, Agarwal 2026 | Relationship grows through shipping together, not emotional dependency |
| **Personality is persona selection** | Anthropic PSM 2026 | Design the archetype coherently — don't stitch traits together |
| **"I don't know" is a feature** | Anthropic 81K 2026 | Users rank sycophancy as top concern. Honest uncertainty > false confidence |

---

## 13. Implementation Order (Flat TODO)

### Phase 0: Foundation

- [ ] Initialize Go module (`go mod init github.com/Sahaj-Tech-ltd/overkill`)
- [ ] Create directory structure from Section 2
- [ ] `.github/` full setup (Section 3.1)
- [ ] `CONTRIBUTING.md`, `SECURITY.md`, `AGENTS.md`
- [ ] `README.md` with badges, ASCII art, comparison table, contributor grid
- [ ] `.gitignore` (inspiration/, .env, secrets)
- [ ] `Makefile` (build, test, lint, install)
- [ ] `Dockerfile` (multi-stage: Go + Python)
- [ ] Clone inspiration repos (gitignored, shallow)
- [ ] Download research papers (47 papers)
- [ ] Write `research/REFERENCES.md`
- [ ] Push to GitHub

### Phase 1: MVP

- [ ] Cobra CLI (`cmd/overkill/`)
- [ ] Provider layer (`internal/providers/`)
- [ ] Core agent loop (`internal/agent/`)
- [ ] Security plane (`internal/security/`)
- [ ] Context compaction (`internal/compaction/`)
- [ ] Tool output compression middleware (per-tool compressor registry, tee recovery, fail-open)
- [ ] Token/cost tracking (`internal/tokenizer/`, `internal/cost/`)
- [ ] Session management (`internal/session/`) — BadgerDB
- [ ] Config system (`internal/config/`) — TOML, auto-migration
- [ ] Tools (`internal/tools/`) — shell, fs, git, web
- [ ] Prompt rewriter middleware (`internal/rewriter/`)
- [ ] Repo onboarding + GitIngest + PRP pipeline (`internal/pipeline/`)
- [ ] Vertical slice decomposition + PRD template (tracer-bullet issues, HITL/AFK classification)
- [ ] Independent test agent
- [ ] Debugging diagnostic report (`internal/diagnostic/`)
- [ ] Self-aware error recovery
- [ ] Confidence & honesty system
- [ ] Data durability — BadgerDB snapshots, export ritual, graceful degradation on corruption (`internal/journal/`)
- [ ] Personality engine (`internal/personality/`)
- [ ] Working style inference (communication patterns, frustration detection, preference molding across sessions)
- [ ] Proactive transparency (pre-execution failure warnings from journal + relationship arc)
- [ ] Cognitive blind spot detection (user pattern surfacing from journal data, not code assumptions)
- [ ] Model fingerprinting (detect model swap, recalibrate competence flags, versioned failure history)
- [ ] Boot sequence (soul.md, fun facts, relationship tracking)
- [ ] Cold start protocol (first-session intake, seeds relationship arc + user.md, closes uncanny valley)
- [ ] **Introspection skill (`internal/introspection/`):**
  - On-demand skill, NOT read on boot. System prompt stays lean.
  - Triggered when user asks "hey what's your config about X?"
  - Reads/generates introspection files (CODEBASE.md, MODEL_CARD.md, KNOWN_ISSUES.md)
- [ ] **Diary / Journal system (`internal/journal/`):**
  - Raw log flight recorder (append-only JSONL, every turn)
  - Journal sub-agent (fires on session exit or cron, writes daily summaries)
  - Alert system (surfaces compaction skips, deferred tasks, frustration signals)
  - Journal query protocol (3-layer search, structured observation types, idempotent storage, CLAIM-CONFIRM queue)
- [ ] Git discipline (religious commits, filesystem checkpoints)
- [ ] Python bridge (`bridge/`)

### Phase 2: TUI + Routing

- [ ] Bubble Tea TUI (`pkg/tui/`) — all OpenCode UX patterns
- [ ] Model routing (`internal/routing/`) — PicoClaw classifier
- [ ] Sub-agent system
- [ ] Cross-agent fault attribution (delegated task ledger, delegation_failure alerts, learn from bad delegation decisions)
- [ ] Handover skill

### Phase 3: Memory + Learning + Walls

- [ ] Embeddings + reranking (Python bridge)
- [ ] Memory orchestration (BadgerDB)
- [ ] Self-learning hooks + skill auto-creation
- [ ] Skills system + VirusTotal scanning
- [ ] Wall 1: Adversarial review — Red Team sub-agent (assumption audit output, trigger conditions, defend-or-concede loop)
- [ ] Wall 2: Architecture context — OVERKILL_ARCH.md + domain glossary + deletion test + ADR gate
- [ ] Wall 3: Test quality (spec-first, mutation testing, regression bank)

### Phase 4: Automation + Channels + Browser

- [ ] Automation engine (event + alarm clocks, SOP, routines)
- [ ] Cron (timezone-aware)
- [ ] Gateway daemon
- [ ] Browser (Playwright + dev-browser + Stagehand)
- [ ] WhatsApp / Telegram / Discord gateways
- [ ] Cross-channel session continuity
- [ ] Image → vision model pipeline
- [ ] Auto-update

### Phase 5: Advanced R&D

- [ ] Cartridge KV compaction
- [ ] Cross-session task graph
- [ ] LATS tree search
- [ ] RL self-improvement

---

## Appendix: Community Management

### If It Goes Viral

1. Response SLA: 48h bugs, 1w features
2. Contributor tiers: Trusted (5 PRs), Experienced (10), Principal (20), Distinguished (50)
3. PR limits: 5 per author
4. Auto-label, supply chain audit, stale bot
5. Discord early: #help, #dev, #skills, #announcements, #showcase

### First-Time Maintainer Pitfalls

- Don't review PRs at 2am
- "No" is a complete sentence
- Document decisions publicly (ADRs)
- Credit everyone — even typo-fix PRs — in contributor grid
- Don't merge own PRs once maintainers exist
- CLA or DCO for license compliance
