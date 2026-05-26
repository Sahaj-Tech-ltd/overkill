# Walls

> Things Overkill would do if the technology existed. Not "we haven't built it yet" — "nobody can build it yet."
> These are capability boundaries, not roadmap items. Revisit when papers drop or providers ship.

---

## 1. Persistent KV Cache (Mooncake)

**What we want:** Store the KV cache server-side between API calls. The model picks up exactly where it left off — no re-transmitting the entire conversation history. Every turn is incremental. A 100-turn session uses ~100 turns of tokens, not ~5000 turns of cumulative context window fees.

**Why we can't:** Moonshot AI (Kimi) published the Mooncake paper on this. The KV cache is massive — gigabytes per session — and it destroys load balancing. You can't move a session between GPUs if the cache lives on one GPU's VRAM. Anthropic's prompt caching is the closest production offering: 5-minute TTL, static prefixes only, ~10% of normal cost. Not stateful. Not incremental.

**What changes:** A provider ships true stateful inference — persistent KV cache with GPU-agnostic storage or session affinity routing. Or someone open-sources a self-hosted inference server that does it. Until then, Overkill's token conservation strategy is the client-side answer: LCM dual-state memory + tool output compression + LLMLingua prompt compression + hot/cold memory paging. The four layers together approximate what Mooncake would give natively.

---

## 2. True Model Self-Calibration

**What we want:** The model assesses its own confidence and says "I'm not confident on this" or "I'm confident this is correct." Overkill routes tasks based on that confidence — confident tasks go to cheap models, uncertain tasks go to expensive ones. The model knows what it doesn't know.

**Why we can't:** MIRROR (Wang 2026) demonstrated that models CANNOT self-calibrate. Giving them calibration data doesn't help. External scaffolding — tests, CI, linters — is mandatory. There is no prompt engineering fix for this. There is no fine-tuning fix for this. The model's self-assessment is not correlated with actual correctness.

**What changes:** A breakthrough in model metacognition — either a training methodology that produces calibrated uncertainty, or an architectural change that exposes internal confidence signals. Until then, Overkill uses external scaffolding exclusively: Wall 3 behavioral regression bank, Red Team adversarial review, mutation testing. The model never grades its own homework.

---

## 3. Cross-Model Competence Transfer

**What we want:** When Overkill switches from Claude Opus to Gemini 3, the competence profile transfers. "Good at auth refactoring, bad at complex SQL joins" was learned on Opus and applies to Gemini. The relationship arc survives model swaps intact.

**Why we can't:** Competence is model-specific. Model fingerprinting (§4.16) detects the swap and recalibrates, but recalibration means starting from scratch on the new model. The old profile is tagged with the old model ID and shelved. There's no way to predict Gemini's performance on auth refactoring from Opus's performance on auth refactoring.

**What changes:** A universal capability metric that correlates across model families. Or a provider API that exposes benchmark results per model per task category. Until then, Overkill recalibrates on swap — targeted probes, versioned failure history, one line to the user: *"Model changed since last session. Running quick calibration."*

---

## 4. Agentic Verification of Delegated Work

**What we want:** Overkill delegates a task to a sub-agent. The sub-agent says "done, here's the output." Overkill verifies — independently and automatically — that the output is real and correct. No "trust me bro" in the delegation chain.

**Why we can't:** Verification requires test execution, code review, or a second model's independent assessment. All of which are expensive and none of which are fully reliable. The Red Team sub-agent catches code assumptions. The behavioral regression bank catches known failure patterns. But true verification — "did the sub-agent actually solve the right problem, correctly?" — is an open research problem.

**What changes:** A formal verification framework for agent outputs. Or a model that produces verifiable proofs alongside its outputs. Or an evaluation harness that can independently assess task completion without relying on the same model family that did the work. Until then, Overkill provides conflict detection (file-state tracker), cross-agent fault attribution (journal delegation_failure alerts), and the user as confidence gate.

---

## 5. Real-Time Sentiment-Aware Tone Adjustment

**What we want:** Overkill detects frustration, stress, urgency, or excitement in the user's messages and adjusts its tone — not just keyword-matching "ugh" and "wtf," but understanding the difference between sarcasm and genuine distress, between hurried and angry, between thinking-out-loud and spiraling.

**Why we can't:** Sentiment analysis at this granularity requires a dedicated model call per message — defeating the purpose of tone mirroring (which is supposed to reduce friction, not add latency). Lightweight keyword heuristics catch the obvious signals but miss everything else. Running a sentiment classifier on every message is too slow and too expensive.

**What changes:** A local, sub-millisecond sentiment classifier. Or a model that embeds sentiment detection into its token generation with zero additional latency. Until then, Overkill uses keyword + punctuation heuristics (§4.16 frustration detection), flagging for human review in early sessions and tuning from real data. Full sentiment-aware behavior is v2.

---

## 6. 60fps Native Terminal Animation

**What we want:** OpenTUI-level pixel rendering: glitch effects, color matrices, smooth scrolling, animated transitions. A terminal agent that looks like a game, not a glorified `less`.

**Why we can't:** This requires a Zig framebuffer or CGo pixel-level terminal control. Bubble Tea is Elm architecture — message-driven, string-based rendering. It's the right abstraction for an agent TUI but the wrong substrate for animation. Go doesn't have a native framebuffer terminal library. The cost of building one is not justified by the value — Overkill is a coding agent, not a terminal graphics demo.

**What changes:** Someone builds a Go-native framebuffer TUI framework that composes with Bubble Tea. Or terminal emulators add GPU acceleration that makes ANSI animations smooth. Until then, Overkill uses Bubble Tea + Lip Gloss with viewport culling, streaming markdown, conceal mode, and auto theme detection — the patterns stolen from OpenTUI, reimplemented in Go idioms.

---

## 7. Autonomous Skill Discovery from Observation

**What we want:** Overkill watches you work. After enough sessions, it notices you keep doing the same multi-step task — "he always runs this same set of git commands after a deploy, then checks these three endpoints." It auto-generates a skill from observation. No explicit teaching. Just... noticing.

**Why we can't:** Pattern detection across sessions is the problem the journal already partially solves with `pattern_detected` alerts. But distinguishing "this is a reusable skill" from "this is a coincidence" requires semantic understanding of intent, not just behavioral clustering. You can cluster repeated command sequences but you can't tell if the user wants them automated.

**What changes:** A model that reliably infers user intent from behavioral patterns. Or a user interface that makes skill-from-observation a one-click approval rather than a silent automation. Until then, Overkill uses hooks + manual skill creation (Voyager pattern §6.2). The user writes skills. Overkill improves them during use.

---

## 8. Full Session Replay with Deterministic Reconstruction

**What we want:** Given a session ID, reconstruct every message, every tool call, every model response — deterministically, from stored state, without re-calling any models. A time machine for debugging agent behavior.

**Why we can't:** The journal (§4.19) records raw I/O but model responses are non-deterministic. You can replay the inputs but you can't replay the outputs without re-calling the model — which would produce different outputs and cost tokens. True deterministic replay requires storing every model response verbatim, which the journal already does (append-only JSONL). But replaying the *agent's decision logic* — why it chose tool A over tool B — requires the model's internal reasoning, which isn't exposed.

**What changes:** Provider APIs that expose chain-of-thought reasoning tokens at the API level (some already do for reasoning models). Or a local replay framework that can match tool traces against journal records to flag divergence. Until then, Overkill uses the journal for post-mortem analysis — raw logs + journal sub-agent summaries + alerts. Not deterministic replay, but close.

---

## 9. Provider-Agnostic Token Counting

**What we want:** Know exactly how many tokens any model will consume for any input — before sending the request. Accurate across all 30+ providers. No estimation. No "tiktoken estimates for OpenAI but Claude uses different tokenizer."

**Why we can't:** Tokenizers are model-specific. OpenAI's tiktoken works for GPT models but not for Claude. Claude's tokenizer is proprietary. Gemini uses SentencePiece with different parameters. Ollama models use whatever tokenizer they were trained with. Even models in the same family can have different tokenizers. Overkill's tokenizer (§4.5) already mentions "estimation" — that's the best anyone can do client-side.

**What changes:** A standardized tokenizer API across providers. Or open-source implementations of every major tokenizer. Or a provider-side endpoint that returns token count without executing the request. Until then, Overkill uses statistical estimation with provider-specific models and a safety margin. Cost display shows "~12.3K tokens" not "12,345 tokens."

---

## 10. Latency-Free Tool Execution (Speculative Execution)

**What we want:** While the model is generating its response, predict which tools it will call and start executing them. By the time the model says "run git status," the output is already in context. Zero perceived tool latency.

**Why we can't:** Speculative execution of shell commands is a safety nightmare. You can't `rm -rf` speculatively and then undo it. Even safe commands have side effects — `git status` modifies the index, `npm install` writes to `node_modules`. Predicting which tools the model will call requires running a second model in parallel, which doubles costs.

**What changes:** A sand-boxed speculative execution environment — like dev-browser's QuickJS WASM sandbox — that can safely pre-execute read-only operations and discard writes. Or actual model support for streaming tool predictions alongside text output. Until then, Overkill uses streaming tool execution (execute as they arrive in the stream) and the `StreamingToolExecutor` pattern from Claude Code.

---

## Revisit Triggers

| Wall | Trigger to revisit |
|------|--------------------|
| Persistent KV Cache | Provider ships stateful inference API, or open-source self-hosted solution appears |
| Model self-calibration | Paper demonstrating calibrated uncertainty in frontier models |
| Cross-model competence transfer | Universal capability benchmark adopted across providers |
| Agentic verification | Formal verification framework for LLM outputs published |
| Sentiment-aware tone | Local sub-millisecond sentiment classifier released |
| 60fps TUI animation | Go-native framebuffer TUI library released |
| Skill discovery from observation | Model capable of intent inference from behavioral patterns |
| Deterministic session replay | Provider exposes chain-of-thought tokens at API level |
| Token counting | Standardized tokenizer API or open-source all-major tokenizers |
| Speculative execution | Safe sandboxed speculative execution environment for read-only tools |
