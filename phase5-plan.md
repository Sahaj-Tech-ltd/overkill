# Phase 5 — Implementation Plan

> Honest read before charging in: Phase 5 is **paper-driven research
> territory**. Some items can be built as real engineering (worktrees,
> task graph, segment memory). Some can only be approximated at the
> application layer because the original papers operate on model
> internals we don't own (KV cache, attention weights, gradient
> attribution). The plan below splits "real builds" from "honest
> approximations" so you know exactly what you're getting.

## Scope rules

Three categories per item:

- **🟢 Real build** — concrete engineering, ships as code that does
  what the paper describes. Multi-batch but bounded.
- **🟡 Approximation** — the original paper requires model-training
  or kernel-level access we don't have. We can deliver the SHAPE of
  the contribution at the application layer (heuristic, retrieval-
  based, analytics) but it's not the paper's actual technique.
- **🔴 Out of scope** — requires model weights, training compute, or
  hardware access that this repo will never have. We document the
  gap and skip.

---

## Wave 1 — Highest user value, real engineering (recommended first)

### 1. §8.5 Worktrees for parallel agents  🟢
Spin a fresh git worktree per subagent so concurrent tasks can't
step on each other's working tree. Already have `internal/worktree/`
scaffolding — finish the wiring: branch-per-task, cleanup on
completion, lockfile-based mutual exclusion. **Estimated: 1 batch.**

### 2. §8.3 Cross-session task graph  🟢
"You asked me to fix X 3 days ago. That shipped (commit abc123)."
Build on top of the existing ledger + journal: tag user requests
with extracted intent, link to commits via `git log -S` or trailer
matching, surface stale-but-open threads at session open. **Estimated:
1–2 batches.**

### 3. §8.2 MemAgent segment-based memory  🟢
Today's memory architecture is hot/cold paging + relationship arc +
flight recorder. MemAgent's contribution: split memory into
*segments* with per-segment retrieval, so a massive codebase doesn't
fold into one cold blob. Build segment store + per-segment metadata
+ retrieval-driven loading. Composes with the vector path that just
shipped in §4.19. **Estimated: 2 batches.**

---

## Wave 2 — Medium value, real engineering

### 4. §8.5 Speculative tool execution  🟢
Cache reads of files the agent is likely to need next; prefetch on
heuristic ("agent just read foo.go, prefetch foo_test.go"). Real
performance win on long sessions. **Estimated: 1 batch.**

### 5. §8.3 Session replay  🟢
Deterministic replay of a journaled session. Useful for debugging
("why did the agent decide X?") and for the dashboard. Flight
recorder already has the data. **Estimated: 1 batch.**

### 6. §8.2 ACE playbooks  🟢
Zhang 2025 ACE = stored prompt pattern selected per task, refined
over use. We have skills + standing orders. Add a *ranking + auto-
refinement* loop: track which playbook succeeded at which task type,
let the agent propose refinements as failhypo-style records.
**Estimated: 2 batches.**

---

## Wave 3 — Security depth

### 7. §8.4 Advanced security composite  🟢
Four papers, one batch each — but they overlap heavily with
existing Walls 1–4. Pick what genuinely adds something:

- **MCPSHIELD** (Acharya 2026): MCP tool-call security layer. Builds
  on our existing pre-tool scanner + protected-path gate. **Real
  addition:** capability declarations per MCP server + policy check.
- **System defense-in-depth** (Xiang 2026): we have 4 walls. Paper
  argues for more layers. **Honest take:** marginal value vs. the
  walls we have. Skip unless we find a concrete gap.
- **ImpossibleBench cheating detection** (Zhong 2025): catches agents
  faking success on impossible tasks. Overlaps with the reward-hack
  detector. **Real addition:** dataset-based probe — periodically
  inject known-impossible tasks and check the agent doesn't claim
  success.
- **Owner-Harm threat model** (Zhang 2026): adversarial input that
  tricks the agent into harming the user. **Real addition:** prompt-
  injection classifier on user-controlled content (files, web
  fetches, tool outputs).

**Estimated: 2 batches (combined).**

---

## Wave 4 — Research-grade, best-effort approximations

### 8. §8.1 Advanced compaction  🟡 approximation
The papers (Cartridge / Neural GC / Fast KV via Attention Matching)
all operate on the model's KV cache. We don't have model weights or
kernel access — we cannot literally implement these.

**What we CAN build:** an application-layer compactor that uses the
same ideas:
- Importance-scoring of context segments (we have telemetry for
  what the agent actually re-reads — score by recency × reuse)
- Hierarchical compaction: summarize old segments into a
  retrievable index instead of dropping
- Pre-fetch on relevance signal

It won't hit 50× ratio. It will deliver *better* compaction than
today's LCM path. **Honest framing: §4.4 enhancement, not a paper
implementation.** **Estimated: 2 batches.**

### 9. §8.5 LATS-style tree search  🟢 with caveats
Zhou 2024 LATS = multi-path tree search for code exploration. Real
implementation requires running multiple agent instances in
parallel, scoring each branch, backtracking. Expensive in tokens
but technically buildable. **Recommendation: build a lightweight
2-branch version first** (try approach A, if it fails after N
steps, backtrack to approach B). Full N-branch can come later.
**Estimated: 2–3 batches.**

### 10. §8.6 Credit assignment  🟡 approximation
Zhang 2026 credit assignment uses gradient attribution to figure
out which past action caused a downstream success/failure. Requires
training. **Approximation:** retrospective heuristic analytics —
when a session ends in success/failure, scan back through the
flight recorder and score which tool calls / decisions correlated
with the outcome. Surface "the failhypo for action X has 3
successes, 7 failures" as a real heads-up. **Not credit assignment
in the RL sense.** **Estimated: 1 batch.**

### 11. §8.3 Drift detection  🟢
Statistical comparison of session metrics — tool-call distribution,
error rates, recovery rates — flagged when this session is N std-
devs from the per-user baseline. Real engineering; we have all the
data via the flight recorder. **Estimated: 1 batch.**

---

## Items I'd recommend SKIPPING

- **§8.1 Cartridge-style 50× KV compaction** — explicitly model-
  internal. We don't have the weights.
- **§8.6 Credit assignment via gradients** — requires training
  infrastructure. Approximation above is the best we can do.

These should land in the plan as `❌ out of scope (model-internal /
requires training)` with the engineering analogue noted next to
them.

---

## Suggested order

Wave 1 → Wave 2 → Wave 3 → Wave 4. Total: ~14 batches if everything
is built. Realistic: pick from the menu based on what you actually
want for v2.

**My pick for a focused v2:** Waves 1 + 2 + #7 (MCPSHIELD +
ImpossibleBench + Owner-Harm) = 8 batches, all real-engineering,
all delivering concrete user value. The rest can wait or be left as
research notes.

---

## Open questions for you

1. **Are we OK approximating §8.1 + §8.6** or do you want them
   left as documented gaps with the paper IDs?
2. **Wave 3 security** — do you want all four (skip System DiD as
   redundant?) or just MCPSHIELD + Owner-Harm?
3. **Order**: Wave 1 first, or jump ahead to a specific item that
   matters more to you (e.g. worktrees if you're hitting conflicts
   today, or LATS if you want the planning depth)?
