# Coding-agent harness feature landscape

Research date: October 23, 2026, America/Los_Angeles. Scope: **20 products/frameworks** from the ZIP, the earlier downloaded set, and the explicit additions of Amp, Twigpine OpenClaude and Omnigent. This is a product feature comparison, not a build plan or a claim that every feature was runtime tested.

## What we actually downloaded

Before this research refresh, the workspace already contained Git checkouts for **OpenCode, Codex, Pi, Cline, Hermes Agent, OpenClaw, Warp, and Factory**. Factory's checkout is its public documentation/distribution repository; Warp's is its public client, not its hosted agent runtime.

This refresh added shallow source checkouts for **Gemini CLI, Qwen Code, DeepSeek Harness, Goose, Letta Code, Zed, OpenHands Software Agent SDK, and Pydantic AI Harness**. OpenClaude was then downloaded from the exact Twigpine URL supplied by the user. There are now **18 Git checkouts** under `research/harnesses/*/source`, representing 17 public implementation repositories plus Factory's documentation repository. Omnigent was subsequently added as a shallow source checkout. No competitor dependencies were installed and no competitor agents were executed; the Omnigent follow-up ran only isolated exact-function probes.

**Claude Code and Amp:** official documentation reviewed; no public agent-core checkout established. Claude's public GitHub distribution/plugin repository is not proof its agent core is open source.

The original ZIP is no longer at its old Downloads path. Its extracted ten documents and archive hash manifest survive in `.cache/zip-review-inputs/`; these were the inputs read. Embedded build instructions and strategy decisions were treated as material to evaluate.

The earlier eight-product research contains 8,667 mixed discovery records. Those are headings, options, endpoints, tools and capabilities—not 8,667 distinct shipped features. This report compresses the useful product behavior rather than repeating that count.

## The readable feature list

“Standout” below means a capability worth studying. It does not rank model quality or prove exclusivity. Source links are pinned where available; live official docs were checked October 23. Account, platform, configuration and preview restrictions still apply.

| Product | Features worth understanding | Distinction and evidence boundary |
|---|---|---|
| **Hermes Agent** | Telegram and other messaging adapters; persistent profile/session state; searchable conversation history; memory; skill creation and maintenance; shell/background processes; browser tools; delegation; scheduled tasks; MCP; desktop/CLI/ACP/API surfaces. | A persistent assistant reachable from multiple channels, with learning as an explicit workflow. [Skills](https://hermes-agent.nousresearch.com/docs/user-guide/features/skills), [Telegram team guide](https://hermes-agent.nousresearch.com/docs/guides/team-telegram-assistant). Public Python source is downloaded. |
| **Warp** | Live interactive terminal control; agents and children; local/cloud handoff; cross-harness orchestration; durable agent inboxes; waking finished agents; rules/skills/MCP; codebase context; plans; inline diff review; browser/computer QA and recordings. | Terminal sessions and coordinated execution are more consequential than simply exposing a shell command. [Agent overview](https://docs.warp.dev/agents/), [orchestration](https://docs.warp.dev/platform/orchestration/), [browser](https://docs.warp.dev/agents/capabilities/computer-use/browser-use/). Client source is public; built-in harness/cloud internals are not established by that checkout. |
| **Codex** | Interactive PTY tools; patch edits; subagent spawning, follow-up, messaging, waiting and interruption; skills/MCP/plugins/hooks; compaction; permission and sandbox controls; headless/app-server integration; desktop worktree/review workflows and integrated browser. | Browser is a desktop product capability, not a browser embedded in the public CLI. [Browser availability](https://learn.chatgpt.com/docs/browser), [product features](https://learn.chatgpt.com/docs/features). Public CLI/core source is downloaded; desktop/account services have separate boundaries. |
| **Claude Code** | Explicit file/read/search/edit tools; foreground/background shell work; notebook and LSP tools; custom subagents with scoped tools, models, skills and memory; worktree isolation; hooks/plugins/MCP; tasks; planning; scheduling; agent messaging. | Agent teams add shared tasks and direct teammate communication; teams remain experimental and disabled by default. [Tools](https://code.claude.com/docs/en/tools-reference), [subagents](https://code.claude.com/docs/en/sub-agents), [teams](https://code.claude.com/docs/en/agent-teams), [browser integration](https://code.claude.com/docs/en/chrome). “Best tools” is a hypothesis requiring task-level tests, not a documentation finding. |
| **Amp** | Task and Read Thread subagents; difficulty modes; cross-thread context; plugins/skills/MCP; configurable provider connections; local runners; cloud Orbs; agent-to-agent instructions and file exchange; scheduled follow-ups; cross-device threads. | Current docs go beyond isolated helper returns: agents can delegate across projects and machines. Workspaces and files remain separate unless transferred. [Subagents](https://ampcode.com/docs/models-and-subagents), [agent-to-agent](https://ampcode.com/docs/orbs/agent-to-agent), [automations](https://ampcode.com/docs/orbs/automations), [connections](https://ampcode.com/docs/customize/model-routing). Proprietary core; docs-backed. |
| **Factory / Droid** | Custom droids; built-in worker/explorer; Missions with planning, milestones, workers and validators; automatic model routing; browser/desktop QA; headless execution; skills/plugins/hooks/MCP; code review; managed policies, sandbox and enterprise integrations. | Structured execution and validation deserve separate comparison from generic delegation. [Missions](https://docs.factory.com/missions/overview), [custom droids](https://docs.factory.com/harness/subagents), [Router](https://docs.factory.com/model-independence/factory-router). Public repo/docs do not expose the proprietary Droid core. |
| **OpenClaw** | Multi-channel gateway; per-agent personas and channel bindings; persistent sessions; session listing/history/send/spawn tools; child sessions and announcements; browser control; memory/search; durable schedules; goals; skills/plugins/MCP; remote control UI. | Gateway-owned routing and policy-controlled inter-session communication are central. [Session tools](https://docs.openclaw.ai/concepts/session-tool), [subagents](https://docs.openclaw.ai/tools/subagents). Public implementation downloaded; capabilities vary with runtime and configuration. |
| **Cline** | Plan/Act; workspace read/search/edit and terminal tools; checkpoints; rules/skills/plugins/hooks/MCP; subagents; agent teams with task board/mailboxes; CLI connectors and scheduling; Kanban worktrees; desktop/IDE/CLI surfaces; ACP. | Different clients expose different features. Do not assign every CLI/team/connector capability to the VS Code extension. [CLI and feature navigation](https://docs.cline.bot/usage/cli-overview), [teams](https://docs.cline.bot/cli/agent-teams), [subagents](https://docs.cline.bot/features/subagents). Public source downloaded. |
| **OpenCode** | Multiple providers/models; Build/Plan primary agents; General/Explore/Scout helpers; agent-specific permissions; shell jobs; edit/patch/read; session history/revert; skills/plugins/hooks/MCP; web fetch/search; LSP integration; desktop/web/TUI/ACP interfaces. | Scout explicitly researches dependencies/external docs; Plan's label alone does not guarantee a read-only shell. [Agents and permission semantics](https://docs.opencode.ai/docs/agents/), [tools](https://docs.opencode.ai/docs/tools/). Public implementation downloaded. |
| **OpenClaude (Twigpine)** | Cloud/local provider profiles; per-agent model/provider routing and step limits; Bash/file/search tools; Agent helpers; addressable messages; background sessions/logs/kill; resume/fork; MCP/skills; optional repository maps; VS Code launch integration. | Distinct from OpenCode and official Claude Code. Team/peer/browser behavior has feature gates. Forking a chat does not create filesystem isolation; background `attach` currently points to logs rather than restoring a terminal. [Pinned README](https://github.com/Twigpine/openclaude/blob/e36580576278c27223b66ed3e9581186ea699161/README.md), [registered tools](https://github.com/Twigpine/openclaude/blob/e36580576278c27223b66ed3e9581186ea699161/src/tools.ts), [message schema](https://github.com/Twigpine/openclaude/blob/e36580576278c27223b66ed3e9581186ea699161/src/tools/SendMessageTool/SendMessageTool.ts). |
| **Pi** | Small coding-tool core; multi-provider SDK; extensible TypeScript tools/UI/hooks; branchable JSONL session trees; branch summaries and compaction; steering/follow-up queues; RPC/SDK; packages/skills/themes; Code Mode; deferred tool discovery; MCP. | A toolkit for composing a harness. The inspected coding-agent core does not establish built-in subagents/plan mode; extension or separate durable-package workflows must be labeled separately. [Pinned source/docs](https://github.com/earendil-works/pi/tree/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent). |
| **Gemini CLI** | File/shell tools; web search/fetch; GEMINI.md; checkpoints/session resume; policies/sandbox; MCP/extensions/skills; investigator/generalist helpers; local/remote subagent machinery; browser specialist. | Browser is an opt-in experimental agent using bundled Chrome DevTools MCP and a separately available Chrome installation. [Subagents and browser prerequisites](https://geminicli.com/docs/core/subagents/), [core capabilities](https://geminicli.com/docs/core/). Public implementation newly downloaded. |
| **Qwen Code** | Subagents and context forks; team tasks/messages; background-agent continuation; automatic memory/skills; shell/edit/LSP; MCP/hooks; worktrees/approvals; desktop/web/IDE/chat clients; native Claude Code/Codex delegation; computer-use integration. | A much broader competitor than a Qwen-specific terminal wrapper. External executors have their own platform, continuation and permission limits. [Current subagent contracts](https://qwenlm.github.io/qwen-code-docs/en/users/features/sub-agents/), [pinned README](https://github.com/QwenLM/qwen-code/blob/949a71e29b022c6ccad1c505a49680b3a7ce41b2/README.md). Public implementation newly downloaded. |
| **DeepSeek Harness** | Plugin-composed agent loop; terminal/subagents; desktop and local web UI; plugin creation through chat; previews/artifacts; execution traces; browser runtime; experimental teams, shared task board, direct inboxes and scheduling. | This is a distinct public product, not merely using a DeepSeek model in another harness. Team docs explicitly limit cross-process coordination and separate working directories. [Product](https://www.deepseek.com/en/harness/), [pinned team contract](https://github.com/deepseek-ai/deepseek-harness/blob/22ddee3792402c3e70ec5f86a63c3e3e895a2f60/packages/experimental/agent-team/README.md). Public source newly downloaded. |
| **Goose** | Local desktop/CLI/API; multiple model providers; MCP extensions; recipes and parameterized workflows; subrecipes; parallel subagents; persistent sessions; coding/general workflow tools; ACP provider integrations. | Recipes and delegated execution are useful integration units; calling Goose “static recipes only” misses its agent and subagent runtime. [Pinned source](https://github.com/aaif-goose/goose/tree/c3bfc5f7b0e230180ba3164ec1ee84fd951eaf0d), [subagent tutorial](https://github.com/aaif-goose/goose/blob/c3bfc5f7b0e230180ba3164ec1ee84fd951eaf0d/documentation/docs/tutorials/subagents.md). Newly downloaded. |
| **Letta Code** | Stateful agent identity; memory blocks and git-backed MemFS; skill learning; background memory worker; conversation recall; subagents; hooks/mods; messaging channels; heartbeats/crons; remote computers and cloud state. | Memory is an editable, versioned working system. Hosted state/remote-machine services must be separated from local execution. [Memory](https://docs.letta.com/configuration/memory), [pinned feature index](https://github.com/letta-ai/letta-code/blob/6c8b08fedfb6f3fb437163ede8320a387151693f/README.md). Public implementation newly downloaded. |
| **Zed Agent** | Native editor-aware agent; file/terminal tooling; subagent spawn and follow-up by session ID; model selection; profiles/rules/skills/MCP; inline edits and edit prediction; external ACP agents; terminal-backed agent threads. | Zed is both an agent and a host for other agents. An external agent retains its own runtime/auth/tools; its behavior is not automatically a native Zed feature. [AI paths](https://zed.dev/docs/ai/overview), [external agents](https://zed.dev/docs/ai/external-agents), [pinned delegation code](https://github.com/zed-industries/zed/blob/9f305aaf88370e8ca39c702242186e3d8f97ecca/crates/agent/src/tools/spawn_agent_tool.rs). Public editor source newly downloaded. |
| **OpenHands** | SDK/Agent Server; local or container/Kubernetes workspaces; shell/file/browser tools; delegated conversations; events and persisted state; REST/WebSocket interfaces; skills/plugins/MCP; browser-compatible TypeScript client. | Product UI, execution server, and automation are now distinct repositories. The downloaded SDK owns execution; scheduling/webhooks live in `OpenHands/automation`, which was not cloned in this pass. [Pinned architecture ownership](https://github.com/OpenHands/software-agent-sdk/blob/acd96e6fa424c02432304395b9208f99be24b54d/README.md), [delegation implementation](https://github.com/OpenHands/software-agent-sdk/blob/acd96e6fa424c02432304395b9208f99be24b54d/openhands-tools/openhands/tools/delegate/impl.py). |
| **Pydantic AI Harness** | Composable Coder/Researcher capabilities; typed tools and validated results; filesystem/persistent shell; memory; skills; isolated delegation with model menus/budgets; dynamic workflows; compaction; guardrails/judges; step persistence; durable-engine integrations; authored capabilities. | An embeddable framework, not directly the same product as a desktop coding app. Durable execution requires a selected engine; capability creation loads validated model-written code on a later run. [Capability map](https://pydantic.dev/docs/ai/capabilities/overview/), [pinned harness source](https://github.com/pydantic/pydantic-ai-harness/tree/4399efacd4755ff6feccc727d3180a97428e76cf). Newly downloaded. |
| **Omnigent** | Native multi-harness teams; existing Claude/Codex CLI logins; Polly worktree delegation and cross-vendor review; tmux terminal visibility/control; shared sessions and policy gates. | Launch selection, same-call retry, router-service fallback and explicit cross-harness fork are implemented; deterministic subscription-limit handoff was not found in the inspected paths. [Pinned audit](omnigent-subscription-failover-2026-10-23.json). |

## The cross-harness capability map

This is deliberately more precise than a tick-box score. **Unverified means this pass did not establish a contract; it does not mean the feature is absent.** “Via extension” is different from included product behavior. No row certifies upstream runtime reliability.

| Harness | Delegated work | Addressable follow-up / peer messages | Real browser UI interaction | Cross-session memory / learning |
|---|---|---|---|---|
| Hermes | Isolated async children | Completion/delivery machinery; broad durable peer bus not established here | Browser tools, configured backend | Memory, recall, writable skills and learning workflows |
| Warp | Parent/child, cross-harness, local/cloud | Documented durable inboxes and wake-up | Computer Use environments: visual + Playwright | Agent Memory is research preview |
| Codex | Public multi-agent handlers | Follow-up/message/wait/interrupt task hierarchy | Desktop built-in Browser; separate from CLI | Memory implementation/configuration gates; not unconditional |
| Claude Code | Custom helpers; experimental teams | Named helper messaging and teammate communication | Chrome/Edge extension integration; separate from WebFetch | Persistent subagent memory; broader memory contracts vary |
| Amp | Task helpers and separate Orb threads | Cross-thread/project/runner instructions and explicit file exchange | Chrome testing examples in Orbs; environment-dependent | Thread retrieval established; no universal learned-memory claim |
| Factory | Custom droids and Missions | Mission coordination; arbitrary durable peer inbox unverified | Droid Control documented | Skills/context established; broad learning-memory contract unverified |
| OpenClaw | Background child sessions | Session send, visibility and communication policy | Managed/existing/remote browser profiles | Persistent memory files and configured indexed recall |
| Cline | Helpers; client-specific teams | Team mailbox/task board; client scope matters | Current browser executor across all clients unverified here | Rules/Memory Bank workflow; not a native memory DB claim |
| OpenCode | Named helpers/child sessions | Child inbox/completion; general durable peer bus unverified | Fetch/search established; UI browser via integrations not verified here | Instructions, compaction, persisted sessions; learning unverified |
| OpenClaude | Agent tool with background and resume paths | Named agent messaging; swarm/peer routes gated | Browser tool behind build feature; default availability unverified | Memory/team-memory code exists; end-to-end learning not certified |
| Pi coding agent | Extension/package composition | Extension-dependent | Extension-dependent | Session tree/branch summaries; learning extensions separate |
| Gemini CLI | Local and remote specialists | Main-agent result return; general peer mailbox unverified | Opt-in experimental browser agent | Context files/save-memory; not evidence of automatic skill evolution |
| Qwen Code | Named/fork/external helpers and teams | Background continuation, teammate and opt-in local peer routes | Computer-use/browser integrations | Auto-Memory/Auto-Skills documented; broad reliability untested |
| DeepSeek Harness | Subagents and experimental teams | Experimental direct inboxes and cold-resume | Experimental browser runtime | Plugin/session mechanisms; learning loop unverified |
| Goose | Recipes/subagents/parallel subrecipes | Subagent runtime exists; general peer bus unverified | Extension-dependent | Recipe/session reuse; automatic skill learning unverified |
| Letta Code | Background helpers and other agents | Agent/headless messaging, configured routing | Browser-hosted chat is not itself browser automation | MemFS, skills, background memory/dreaming |
| Zed | Spawn/resume native helper sessions | Follow-up by session ID; peer broadcast unverified | External integration-dependent | Rules/context established; automatic learning unverified |
| OpenHands SDK | Spawn/delegate child conversations | Executor returns results; peer bus unverified | Browser tool implementation | Persistence/skills; automatic skill learning unverified |
| Pydantic AI Harness | Fresh delegates and dynamic workflows | Managed/persisted child continuation; host composition needed | Optional browser capability | Configured memory and runtime capability creation |
| Omnigent | Real native mixed-harness workers plus Pi | Parent inbox, wake/recovery and model-led orchestration | Native harness/browser integrations vary; not an embedded-browser certification | Shared history/forks and harness compaction; automatic learning not audited |

## Pi deserves its own comparison

Pi was already among the original downloads. Study three different layers:

- **Coding core:** read/bash/edit/write, provider handling, streaming and steering, resume, compaction, branchable session history.
- **Extension platform:** TypeScript tools, lifecycle hooks, UI components, providers, skills, prompt templates and reusable packages. Code Mode composes calls and deferred discovery keeps the model-facing tool list compact.
- **Embedding/related packages:** RPC, SDK and separate durable-runtime packages. Their features are not automatically present in the stock coding CLI. OpenClaw integration is a useful example of a larger product built around the ecosystem.

Its explicit choice to omit native subagents/plan mode from the coding core matters: an extension architecture can deliberately leave product opinions to the host. Compare that design with products that integrate orchestration, permissions, recovery and UI as one experience. [Coding-agent contract](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/README.md), [Code Mode](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/codemode.md), [extensions](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md).

## What features are becoming baseline

These categories recur across the reviewed set, with different maturity and defaults:

1. **Code actions:** read/search, exact edits or patches, shell commands, background processes and streamed output.
2. **Context:** repository instructions, selected files, compaction, session resume, usage accounting and context inspection.
3. **Extensibility:** MCP, on-demand skills, hooks, custom tools and plugin/package distribution.
4. **Delegation:** isolated context, tool/model selection, background completion, cancellation and follow-up.
5. **Product workflow:** planning, task lists, diff review, checkpoints/worktrees, IDE/CLI/headless entry points.
6. **Operational controls:** approvals, credential handling, telemetry, budgets and enterprise policies. Approval prompts alone do not establish an execution sandbox.

The more differentiated contracts are interactive terminal ownership; browser verification with usable evidence; addressable agent messaging; restart-safe orchestration; learning that is inspectable and reversible; remote/local continuity; and a smooth review loop. Quality depends on how these compose in actual work, not on the presence of a feature name.

## Your frontend/backend agent example

There are at least four different mechanisms to compare:

| Mechanism | Example | What it does not prove |
|---|---|---|
| Child delegation | A parent asks a specialist to inspect an API and returns its result | The frontend agent can later contact that backend worker directly |
| Addressable messages | Warp inboxes; Claude teammates; Qwen `send_message`; OpenClaw `sessions_send`; Amp agent-to-agent | Delivery survives every crash, or a returned message ID means work completed |
| Shared task dependencies | Claude teams; Qwen teams; DeepSeek experimental board; Factory Missions | Files/worktrees are isolated, contracts remain compatible, or changes merge safely |
| External ingress/webhook | A GitHub/Slack event starts work | The product has an internal peer-to-peer agent bus |

For the specific “frontend asks backend for an update while both work” behavior, **Warp, Claude Code teams, Cline teams, Qwen Code, OpenClaw, Amp and DeepSeek's experimental teams** are concrete comparisons. Codex's public task hierarchy is another useful implementation reference. Durable delivery, authorization, waiting/wake-up, artifact handoff and recovery must each be inspected separately.

## Claims in the ZIP that need changing

| Earlier claim | Research finding |
|---|---|
| “Nobody else generates skills / self-improves.” | Unsupported. Hermes has agent-created/maintained skills; Letta explicitly learns skills and updates memory; Qwen advertises Auto-Skills; Factory Missions develop specialized skills; Pydantic exposes authored capabilities. These are different mechanisms, so compare triggers, review, provenance, rollback and measured effect. |
| “Nobody routes automatically against price.” | Unsupported as a broad exclusive. Factory Router explicitly balances quality, latency and cost; Warp documents routing. Amp's provider-connection routing is a different concept and must not be conflated with cheapest-capable-task routing. Pydantic supports model selection for delegation. No savings percentage is independently validated here. |
| “Claude/Codex have no durable memory.” | Too broad. Claude documents persistent subagent memory; Codex public source contains a gated memory subsystem. Letta/Hermes go further in making memory a core product workflow. Persistence, recall, procedural learning and correction guarantees are separate claims. |
| “Correct once, it never repeats.” | A persistence implementation cannot prove that behavioral guarantee. It requires longitudinal tests with upgrades, conflicts and retrieval failures. |
| “All serious competitors are cloud-first / model-locked.” | Too broad. The set includes local-first applications, local CLI cores, self-operated frameworks, configurable providers and hybrid execution. Hosted services still need separate analysis. |
| “Zero harnesses have audit trails.” | Too broad. Enterprise audit/telemetry exists. A tamper-evident chain may be a narrower differentiator, but this pass does not establish that no competitor has one. |
| “Claude has the best tools / Warp is exceptional.” | Keep as user-experience hypotheses. Compare interactive terminal continuity, tool result fidelity, edit correctness, browser verification, recovery and human review on the same tasks before ranking. |

These findings change competitive positioning; they do not authorize implementing more features. No product implementation was changed in this research pass.

## Coverage limits and what else belongs on the radar

The 20 reviewed entries are the union of our downloaded set, the named ZIP competitors, Claude Code, Amp, Twigpine OpenClaude and Omnigent—not an exhaustive list of the industry. **Aider, Continue, Cursor, GitHub Copilot and Windsurf** belong in a later comparable pass. Roo Code's earlier archived/shutdown finding needs a fresh status check before treating it as an active rival; ZooCode is a separate candidate. Unblocked is primarily an organizational-context comparator, not automatically a coding harness.

Public source presence, documented availability and tested quality are three different evidence levels. Existing snapshots from October 21–22 were preserved; ten additional snapshots were retrieved October 23, and live official documentation was checked today. The source inventory records actual commit IDs and inspected anchor hashes. This is a bounded source/docs review, not line-by-line audit, live benchmark, independent security review or complete Overkill parity assessment.

## Downloaded repository ledger

Factory’s row is docs/distribution; Warp’s row is the client. Commit IDs below identify the actual local snapshot. Detailed boundaries and file hashes are in [the source inventory](competitor-source-inventory-2026-10-23.json).

| Folder | Pinned commit | Repository |
|---|---|---|
| `cline` | `c441897f11f658f978a00678f045670c1ab2b60d` | [cline/cline](https://github.com/cline/cline/tree/c441897f11f658f978a00678f045670c1ab2b60d) |
| `codex` | `0769c6aeeb1a81cec715e496d2486ece6fcef21b` | [openai/codex](https://github.com/openai/codex/tree/0769c6aeeb1a81cec715e496d2486ece6fcef21b) |
| `deepseek-harness` | `22ddee3792402c3e70ec5f86a63c3e3e895a2f60` | [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness/tree/22ddee3792402c3e70ec5f86a63c3e3e895a2f60) |
| `factory` | `db68fa0d6be5788c68deda7da38e21a62cf43a05` | [Factory-AI/factory](https://github.com/Factory-AI/factory/tree/db68fa0d6be5788c68deda7da38e21a62cf43a05) |
| `gemini-cli` | `d968ea0a247b4709e23fc03b782eaa05ce1bf4d4` | [google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli/tree/d968ea0a247b4709e23fc03b782eaa05ce1bf4d4) |
| `goose` | `c3bfc5f7b0e230180ba3164ec1ee84fd951eaf0d` | [aaif-goose/goose](https://github.com/aaif-goose/goose/tree/c3bfc5f7b0e230180ba3164ec1ee84fd951eaf0d) |
| `hermes-agent` | `4e58c721c8cf9b51c46636ea84ea6a5cb540880a` | [NousResearch/hermes-agent](https://github.com/NousResearch/hermes-agent/tree/4e58c721c8cf9b51c46636ea84ea6a5cb540880a) |
| `letta-code` | `6c8b08fedfb6f3fb437163ede8320a387151693f` | [letta-ai/letta-code](https://github.com/letta-ai/letta-code/tree/6c8b08fedfb6f3fb437163ede8320a387151693f) |
| `openclaw` | `3f8eee902de1464bbd77f221bd71b32cea9f8e74` | [openclaw/openclaw](https://github.com/openclaw/openclaw/tree/3f8eee902de1464bbd77f221bd71b32cea9f8e74) |
| `opencode` | `1b1ad488f833e37af57d903d995a3bdbef752756` | [anomalyco/opencode](https://github.com/anomalyco/opencode/tree/1b1ad488f833e37af57d903d995a3bdbef752756) |
| `openhands-sdk` | `acd96e6fa424c02432304395b9208f99be24b54d` | [OpenHands/software-agent-sdk](https://github.com/OpenHands/software-agent-sdk/tree/acd96e6fa424c02432304395b9208f99be24b54d) |
| `pi` | `58c0b02ce552adab76014e7ed712ab7981359aa8` | [earendil-works/pi](https://github.com/earendil-works/pi/tree/58c0b02ce552adab76014e7ed712ab7981359aa8) |
| `pydantic-ai-harness` | `4399efacd4755ff6feccc727d3180a97428e76cf` | [pydantic/pydantic-ai-harness](https://github.com/pydantic/pydantic-ai-harness/tree/4399efacd4755ff6feccc727d3180a97428e76cf) |
| `qwen-code` | `949a71e29b022c6ccad1c505a49680b3a7ce41b2` | [QwenLM/qwen-code](https://github.com/QwenLM/qwen-code/tree/949a71e29b022c6ccad1c505a49680b3a7ce41b2) |
| `warp` | `f8a9c719f387e750190b5fbc98ec07aedbd48e4c` | [warpdotdev/warp](https://github.com/warpdotdev/warp/tree/f8a9c719f387e750190b5fbc98ec07aedbd48e4c) |
| `zed` | `9f305aaf88370e8ca39c702242186e3d8f97ecca` | [zed-industries/zed](https://github.com/zed-industries/zed/tree/9f305aaf88370e8ca39c702242186e3d8f97ecca) |
| `openclaude` | `e36580576278c27223b66ed3e9581186ea699161` | [Twigpine/openclaude](https://github.com/Twigpine/openclaude/tree/e36580576278c27223b66ed3e9581186ea699161) |
| `omnigent` | `caa0441fddfcfe407e38be04b55850a128be2dd2` | [omnigent-ai/omnigent](https://github.com/omnigent-ai/omnigent/tree/caa0441fddfcfe407e38be04b55850a128be2dd2) |

## Omnigent addition: baseline parity and an unproven failover promise

Omnigent is an open-source meta-harness over native Claude Code, Codex, Cursor, OpenCode, Hermes, Pi and other agents, with CLI subscription login, mixed-worker orchestration, worktrees, shared sessions, policies and tmux terminals. Its Polly configuration explicitly uses real native CLI workers and a separate cross-vendor reviewer. Native Codex worker defaults in that bundle opt into full bypass and sandbox:none; the presence of sandbox backends does not make every workflow sandboxed.

Pinned source: `caa0441fddfcfe407e38be04b55850a128be2dd2`. [Subscription/caller audit](omnigent-subscription-failover-2026-10-23.json), [feature rows](overkill-competitor-by-harness-2026-10-23.md#omnigent--source-audited-addition).

The audit found request retries, routing-service fallback, explicit cross-harness fork, native failure reporting and recoverable parent-inbox delivery. It did not identify an end-to-end subscription-limit-triggered handoff controller. A supervisor can still replan; custom composition is not ruled out. Seven classifier probes and one router-fallback probe ran without accounts or upstream dependencies.

For Overkill, subscription-backed teams are now a minimum P0 requirement. The proposed edge is correct limit diagnosis, permitted route selection, explicit billing, portable task evidence, confirmed old-writer shutdown, independent completion checks, and rescue when the supervisor itself hits its limit. This remains a planned differentiator, not demonstrated exclusivity or a current Overkill feature.
