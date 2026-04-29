# Research Paper Triage for Ethos

Sorted from VoltAgent/awesome-ai-agent-papers (363 papers, 2026).

## MUST READ

Directly applicable to Ethos architecture. Addresses a problem Ethos has or a feature Ethos is building.

| # | Title | arXiv | Why |
|---|-------|-------|-----|
| 1 | When Single-Agent with Skills Replace Multi-Agent Systems and When They Fail | 2601.04748 | Exactly Ethos's architecture: single agent + skill library. Studies scaling limits and phase transitions in skill selection. |
| 2 | Structured Context Engineering for File-Native Agentic Systems | 2602.05447 | Tests how context format (YAML, JSON, Markdown) affects agent accuracy across 9,649 experiments. Directly informs Ethos's context construction. |
| 3 | Why Reasoning Fails to Plan: Planning-Centric Analysis of Long-Horizon Decision Making | 2601.22311 | Diagnoses why step-wise reasoning breaks down in long horizons. Proposes future-aware lookahead — directly relevant to Ethos's ReAct loop. |
| 4 | Agentic Design Patterns: A System-Theoretic Framework | 2601.19752 | 12 reusable design patterns decomposed into 5 functional subsystems. Blueprint for Ethos's architecture. |
| 5 | SWE-Pruner: Self-Adaptive Context Pruning for Coding Agents | 2601.16746 | Task-aware context pruning with a lightweight neural skimmer. Directly applicable to Ethos's context compaction. |
| 6 | Toward Efficient Agents: Memory, Tool learning, and Planning | 2601.14192 | Survey comparing approaches under fixed cost budgets. Analyzes Pareto frontier between effectiveness and cost — exactly Ethos's model routing problem. |
| 7 | InfiAgent: An Infinite-Horizon Framework for General-Purpose Autonomous Agents | 2601.03204 | Keeps reasoning context bounded via externalized file-centric state. Directly applicable to Ethos's long-running session management. |
| 8 | Active Context Compression: Autonomous Memory Management in LLM Agents | 2601.07190 | Agent autonomously decides when to consolidate learnings and prune raw history. Mirrors Ethos's compaction system. |
| 9 | Beyond Static Summarization: Proactive Memory Extraction for LLM Agents | 2601.04463 | Self-questioning feedback loops for memory extraction instead of one-off summarization. Improves Ethos's compaction quality. |
| 10 | Continuum Memory Architectures for Long-Horizon LLM Agents | 2601.09913 | Defines requirements for persistent, temporally chained memory in long-horizon agents. Architectural spec for Ethos's memory system. |
| 11 | Grounding Agent Memory in Contextual Intent | 2601.10702 | Indexes trajectory steps with structured intent cues, retrieves by intent compatibility. Improves Ethos's session memory retrieval. |
| 12 | Rethinking Memory Mechanisms of Foundation Agents | 2602.06052 | Comprehensive survey of agent memory: episodic, semantic, working, procedural. Essential reading for Ethos's memory architecture. |
| 13 | Graph-based Agent Memory: Taxonomy, Techniques, and Applications | 2602.05665 | Surveys graph-based memory: extraction, storage, retrieval, temporal evolution. Informs Ethos's optional Qdrant-backed memory. |
| 14 | BudgetMem: Learning Query-Aware Budget-Tier Routing for Runtime Agent Memory | 2602.06025 | Routes memory queries to different processing tiers by query difficulty. Directly applicable to Ethos's cost-aware memory routing. |
| 15 | Reliable Graph-RAG for Codebases: AST-Derived Graphs vs LLM-Extracted Knowledge Graphs | 2601.08773 | Benchmarks vector-only vs AST-derived graph pipelines for code RAG. Directly relevant to Ethos's codebase understanding tools. |
| 16 | TrajAD: Trajectory Anomaly Detection for Trustworthy LLM Agents | 2602.06443 | Detects and locates errors in agent execution trajectories at runtime for rollback-and-retry. Relevant to Ethos's diagnostic system. |
| 17 | Automated Structural Testing of LLM-Based Agents | 2601.18827 | OpenTelemetry traces for agent testing, mocking for reproducibility, automated assertions. Relevant to Ethos's testing/observability. |
| 18 | When Agents Fail: A Comprehensive Study of Bugs in LLM Agents with Automated Labeling | 2601.15232 | Bug taxonomy from 1,187 reports across seven frameworks. Directly informs Ethos's error handling and journal system. |
| 19 | Prompt Injection Attacks on Agentic Coding Assistants: A Systematic Analysis | 2601.17548 | 78 studies systematized. Three-dimensional taxonomy of delivery vectors, modalities, propagation. Essential for Ethos's security plane. |
| 20 | Learning to Inject: Automated Prompt Injection via Reinforcement Learning | 2602.05746 | RL-generated prompt injection that transfers across frontier models. Stress-tests Ethos's injection detection. |
| 21 | SMCP: Secure Model Context Protocol | 2602.01129 | Protocol-level MCP security: identity, mutual auth, fine-grained policies. Directly relevant if Ethos adopts MCP. |
| 22 | Breaking the Protocol: Security Analysis of MCP Specification | 2601.17549 | First security analysis of MCP spec, identifies three protocol-level vulnerabilities. Must-read for MCP adoption. |
| 23 | MCP-ITP: An Automated Framework for Implicit Tool Poisoning in MCP | 2601.07395 | Poisoned tool metadata manipulates agent into malicious operations via legitimate tools. Critical for Ethos's tool security. |
| 24 | Towards Verifiably Safe Tool Use for LLM Agents | 2601.08012 | STPA hazard analysis for agent tool-use workflows with formal safety specs via MCP. Directly applicable to Ethos's security plane. |
| 25 | AgenTRIM: Tool Risk Mitigation for Agentic AI | 2601.12449 | Offline interface verification + runtime least-privilege tool access with adaptive filtering. Maps to Ethos's command scanning. |
| 26 | Taming Various Privilege Escalation in LLM-Based Agent Systems: A MAC Framework | 2601.11893 | Mandatory access control via information flow graphs for agent-tool interactions. Directly applicable to Ethos's path blocking. |
| 27 | Beyond Max Tokens: Stealthy Resource Amplification via Tool Calling Chains in LLM Agents | 2601.10955 | Economic DoS via MCP tool loops inflating costs 658x. Relevant to Ethos's budget enforcement. |
| 28 | Malicious Agent Skills in the Wild: A Large-Scale Security Empirical Study | 2602.06547 | Analyzes 98K agent skills from community registries for malicious plugins. Critical for Ethos's skill system security. |
| 29 | Agent Skills in the Wild: An Empirical Study of Security Vulnerabilities at Scale | 2601.10338 | 42,447 skills analyzed for prompt injection, data exfiltration, privilege escalation, supply chain risks. |
| 30 | Memory Poisoning Attack and Defense on Memory-Based LLM Agents | 2601.05504 | Memory poisoning attacks + defenses with composite trust scoring and trust-aware retrieval. Essential for Ethos's memory system. |
| 31 | MCP-SandboxScan: WASM-Based Secure Execution and Runtime Analysis for MCP Tools | 2601.01241 | WASM sandbox for untrusted MCP tools with auditable exposure reports. Relevant to Ethos's tool execution security. |

## USEFUL

Relevant concepts or techniques that could improve Ethos but aren't critical path.

| # | Title | arXiv | Why |
|---|-------|-------|-----|
| 1 | LSTM-MAS: Long Short-Term Memory Inspired Multi-Agent System for Long-Context Understanding | 2601.11913 | Gated memory mechanisms (worker, filter, judge, manager) for controlling information flow. Concepts applicable to Ethos's compaction. |
| 2 | Adaptive Confidence Gating in Multi-Agent Collaboration for Code Generation | 2601.21469 | Structured debate with confidence gating improves small model code generation. Applicable to Ethos's cost-aware model routing. |
| 3 | CASTER: Context-Aware Strategy for Task Efficient Routing in Multi-Agent Systems | 2601.19793 | Lightweight router combining semantic embeddings with structural meta-features and self-optimizing negative feedback. Routing patterns applicable. |
| 4 | Orchestrating Intelligence: Confidence-Aware Routing for Multi-Agent Collaboration | 2601.04861 | Dynamic agent/model selection based on task complexity with confidence awareness. Applicable to Ethos's model routing. |
| 5 | Mixture-of-Models: Unifying Heterogeneous Agents via N-Way Self-Evaluating Deliberation | 2601.16863 | Small model ensembles match frontier performance via dynamic expertise broker and quadratic voting. |
| 6 | Bayesian Orchestration of Multi-LLM Agents for Cost-Aware Sequential Decision-Making | 2601.01522 | Bayesian cost-aware orchestration treating LLMs as approximate likelihood models. Cost patterns applicable. |
| 7 | LLM-Based Agentic Systems for Software Engineering: Challenges and Opportunities | 2601.09822 | Reviews multi-agent SE systems across the development lifecycle. Good context for Ethos's dev pipeline. |
| 8 | A Large-Scale Study on Multi-Agent AI Systems Development | 2601.07136 | Analyzes 42K commits across LangChain, CrewAI, AutoGen. Lessons for Ethos's architecture. |
| 9 | ProcMEM: Learning Reusable Procedural Memory from Experience | 2602.01869 | Step-by-step procedural skills saved and reused without retraining. Applicable to Ethos's skill learning. |
| 10 | E-mem: Multi-agent Based Episodic Context Reconstruction | 2601.21714 | Uncompressed episodic memory with context reconstruction instead of destructive compression. Alternative to Ethos's compaction. |
| 11 | ShardMemo: Masked MoE Routing for Sharded Agentic LLM Memory | 2601.21545 | Tiered memory with masked MoE routing under fixed budget. Applicable to Ethos's memory tiering. |
| 12 | FadeMem: Biologically-Inspired Forgetting for Efficient Agent Memory | 2601.18642 | Adaptive exponential decay, LLM-guided conflict resolution, dual-layer hierarchy. Applicable to Ethos's memory lifecycle. |
| 13 | Less is More for RAG: Information Gain Pruning for Generator-Aligned Reranking | 2601.17532 | Selects evidence using utility signals, filters weak passages before context truncation. Applicable to Ethos's context construction. |
| 14 | Structure and Diversity Aware Context Bubble Construction for RAG | 2601.10681 | Balances relevance, coverage, and redundancy under strict token budgets. Applicable to Ethos's context window management. |
| 15 | To Retrieve or To Think? An Agentic Approach for Context Evolution | 2601.08747 | Dynamically decides retrieve vs reason at each step. Eliminates redundant retrieval. Applicable to Ethos's tool selection. |
| 16 | SwiftMem: Fast Agentic Memory via Query-Aware Indexing | 2601.08160 | Sub-linear retrieval via temporal/semantic DAG-Tag indexing. Memory fragmentation solution for Ethos. |
| 17 | Learning How to Remember: Meta-Cognitive Management for Transferable Agent Memory | 2601.07470 | Treats memory abstraction as learnable skill via DPO. Applicable to Ethos's compaction quality. |
| 18 | Beyond Dialogue Time: Temporal Semantic Memory for Personalized LLM Agents | 2601.07468 | Organizes memories by actual occurrence time, consolidates temporally continuous info. For Ethos's journal system. |
| 19 | Amory: Building Coherent Narrative-Driven Agent Memory | 2601.06282 | Working memory constructs episodic narratives, consolidates with momentum, semanticizes during offline time. |
| 20 | L-RAG: Balancing Context and Retrieval with Entropy-Based Lazy Loading | 2601.06551 | Entropy-based gating bypasses retrieval when uncertainty is low. Reduces unnecessary vector lookups. |
| 21 | Controllable Memory Usage: Balancing Anchoring and Innovation | 2601.05107 | User-controllable memory reliance as explicit steerable dimension. For Ethos's personality/memory integration. |
| 22 | Membox: Weaving Topic Continuity into Long-Range Memory | 2601.03785 | Hierarchical memory with Topic Loom grouping same-topic turns into coherent boxes linked by event timelines. |
| 23 | MAGMA: A Multi-Graph Based Agentic Memory Architecture | 2601.03236 | Orthogonal semantic, temporal, causal, and entity graphs with policy-guided traversal. For Ethos's optional Qdrant backend. |
| 24 | HiMeS: Hippocampus-Inspired Memory System for Personalized AI Assistants | 2601.06152 | RL-trained short-term extraction + partitioned long-term memory. For Ethos's memory architecture. |
| 25 | SimpleMem: Efficient Lifelong Memory for LLM Agents | 2601.02553 | Three-stage framework: structured compression, online semantic synthesis, intent-aware retrieval. |
| 26 | The AI Hippocampus: How Far are We From Human Memory? | 2601.09113 | Survey of memory in LLMs across implicit, explicit, and agentic paradigms. |
| 27 | AtomMem: Learnable Dynamic Agentic Memory with Atomic Memory Operations | 2601.08323 | Decomposes memory into atomic CRUD operations with learned policy. |
| 28 | From Features to Actions: Explainability in Traditional and Agentic AI Systems | 2602.06841 | Trace-based diagnostics for agent trajectories. Applicable to Ethos's diagnostic system. |
| 29 | Agentic Uncertainty Reveals Agentic Overconfidence | 2602.06948 | Agents can't accurately predict their own success rates. Informs Ethos's confidence estimation. |
| 30 | TriCEGAR: Trace-Driven Abstraction for Agentic AI | 2601.22997 | Automated state abstraction from traces with counterexample refinement. For runtime verification. |
| 31 | Why Are AI Agent PRs Unmerged | 2602.00164 | Catalogs why agent-generated code gets rejected. Informs Ethos's code generation quality. |
| 32 | Stalled, Biased, Confused: Reasoning Failures in LLMs for Root Cause Analysis | 2601.22208 | Taxonomy of 16 ReAct reasoning failures across 48,000 scenarios. Directly relevant to Ethos's ReAct loop. |
| 33 | CAR-bench: Consistency and Limit-Awareness of LLM Agents | 2601.22027 | Evaluates consistency and uncertainty handling in multi-turn tool use. |
| 34 | More Code, Less Reuse: Code Quality and Reviewer Sentiment towards AI PRs | 2601.21276 | Examines maintainability and reviewer sentiment for AI-generated code. |
| 35 | Architecture-Aware Evaluation Metrics for LLM Agents | 2601.19583 | Links agent components (planners, memory, routers) to observable behaviors and diagnostic metrics. |
| 36 | Balancing Sustainability: Small-Scale LLMs in Agentic AI Systems | 2601.19311 | Small models reduce energy consumption without quality loss. Relevant to Ethos's routing to cheap models. |
| 37 | Understanding Dominant Themes in Reviewing Agentic AI-Authored Code | 2601.19287 | Taxonomy of 12 review themes for AI-generated code. |
| 38 | When AI Agents Touch CI/CD Configurations | 2601.17413 | How coding agents interact with CI/CD configs across 8,031 PRs. |
| 39 | Interpreting Agentic Systems: Beyond Model Explanations | 2601.17168 | Gaps in explaining temporal dynamics and compounding decisions in agents. |
| 40 | Will It Survive? AI-Generated Code in Open Source | 2601.16809 | Long-term survival analysis of 200K+ AI code units. |
| 41 | LUMINA: Long-horizon Understanding for Multi-turn Interactive Agents | 2601.16649 | Measures criticality of planning and state tracking capabilities. |
| 42 | When Agents Fail to Act: Tool Invocation Reliability | 2601.16280 | 12-category tool-use error taxonomy across LLMs on edge hardware. |
| 43 | Agentic Confidence Calibration | 2601.15778 | Process-level features across entire trajectory for failure diagnosis. |
| 44 | Tokenomics: Where Tokens Are Used in Agentic Software Engineering | 2601.14470 | Token consumption patterns across SE lifecycle stages. Cost insights for Ethos's budget tracking. |
| 45 | What Do LLM Agents Know About Their World? Task2Quiz | 2601.09503 | Decouples task success from environment understanding. |
| 46 | Hierarchy of Agentic Capabilities | 2601.09032 | Empirical hierarchy: tool use, planning, adaptability, groundedness, common-sense reasoning. |
| 47 | Lost in the Noise: Reasoning Models Fail with Contextual Distractors | 2601.07226 | Model robustness against context noise across 11 tasks. Relevant to Ethos's context construction. |
| 48 | RealMem: Benchmarking LLMs in Real-World Memory-Driven Interaction | 2601.06966 | Memory benchmark with 2,000+ cross-session dialogues tracking evolving goals. |
| 49 | Mem2ActBench: Long-Term Memory Utilization in Task-Oriented Agents | 2601.19935 | Benchmarks whether agents proactively use long-term memory for actions. |
| 50 | Internal Representations as Indicators of Hallucinations in Agent Tool Selection | 2601.05214 | Detects tool-calling hallucinations from internal representations in a single forward pass. |
| 51 | Agent-as-a-Judge | 2601.05111 | Surveys agentic judges with planning, tool-augmented verification, persistent memory. |
| 52 | Analyzing Message-Code Inconsistency in AI Coding Agent PRs | 2601.04886 | Trustworthiness of agent-generated PR descriptions. |
| 53 | Agent Drift: Behavioral Degradation in Multi-Agent LLM Systems | 2601.04170 | Quantifies semantic, coordination, and behavioral degradation over extended interactions. |
| 54 | Project Ariadne: Structural Causal Framework for Auditing Faithfulness | 2601.02314 | Counterfactual interventions to audit whether reasoning traces are faithful or post-hoc. |
| 55 | ReliabilityBench: Agent Reliability Under Stress Conditions | 2601.06112 | Chaos-engineering-style tool failure injection for agent testing. |
| 56 | Are We All Using Agents the Same Way? Core and Peripheral Developers | 2601.20106 | How developers differ in use and verification of coding-agent PRs. |
| 57 | DevOps-Gym: Benchmarking AI Agents in DevOps | 2601.20882 | 700+ real-world DevOps tasks across build, monitoring, issue resolving. |
| 58 | WildAGTEval: Function-Calling Under Realistic API Conditions | 2601.00268 | Evaluates function-calling with noisy outputs and runtime challenges. |
| 59 | Terminal-Bench: Benchmarking Agents on CLI Tasks | 2601.11868 | 89 hard tasks in terminal environments with comprehensive tests. |
| 60 | ToolGym: Open-world Tool-Using Environment | 2601.06328 | 5,571 tools across 204 apps with failure injection for robustness testing. |
| 61 | AutoRefine: From Trajectories to Reusable Expertise | 2601.22758 | Extracts dual-form expertise (subagents + skill patterns) from execution histories with continuous pruning. |
| 62 | Optimizing Agentic Workflows using Meta-tools | 2601.22037 | Bundles recurring tool call sequences into deterministic meta-tools to skip intermediate LLM steps. |
| 63 | Meta Context Engineering via Agentic Skill Evolution | 2601.21557 | Meta-agent evolves context engineering skills via crossover while base agent executes. |
| 64 | Think-Augmented Function Calling | 2601.18282 | Embeds explicit reasoning at function and parameter levels with dynamic complexity scoring. |
| 65 | JitRL: Just-In-Time RL for Continual Learning Without Gradient Updates | 2601.18510 | Training-free continual learning by retrieving past experiences and modulating logits at test time. |
| 66 | Agentic Uncertainty Quantification | 2601.15703 | Dual-process framework transforms verbalized uncertainty into control signals for memory and reflection. |
| 67 | Controlling Long-Horizon Behavior with Explicit State Dynamics | 2601.16087 | External affective state induces temporal coherence in multi-turn agents. |
| 68 | MAXS: Meta-Adaptive Exploration with LLM Agents | 2601.09259 | Lookahead planning estimates tool value at each step, halts when consistency reached. |
| 69 | ToolACE-MCP: History-Aware Routing from MCP Tools | 2601.08276 | History-aware routers for MCP tool ecosystems using dependency graphs. |
| 70 | Beyond Single-Shot: Multi-step Tool Retrieval via Query Planning | 2601.07782 | Iterative query planning decomposes instructions into sub-tasks for tool retrieval. |
| 71 | Beyond Static Tools: Test-Time Tool Evolution | 2601.07641 | Agents synthesize, verify, and evolve tools during inference instead of using static libraries. |
| 72 | JudgeFlow: Workflow Optimization via Block Judge | 2601.07477 | Block-level responsibility scores for failing workflow components. |
| 73 | ET-Agent: Tool-Integrated Reasoning via Behavior Calibration | 2601.06860 | Self-evolving data flywheel and two-phase calibration to reduce redundant/insufficient tool calls. |
| 74 | CEDAR: Context Engineering for Agentic Data Science | 2601.06606 | Structured prompting, separate plan/code agents, smart history rendering for fault tolerance. |
| 75 | Architecting AgentOps Needs CHANGE | 2601.06456 | Six capabilities for managing lifecycle of evolving agentic AI systems. |
| 76 | AgentDevel: Reframing Self-Evolving Agents as Release Engineering | 2601.04620 | Self-improvement as release pipeline with quality signals and regression gating. |
| 77 | XGrammar 2: Dynamic Structured Generation Engine for Agentic LLMs | 2601.04426 | Dynamic tag dispatching, JIT compilation, cross-grammar caching for tool calling. |
| 78 | Transitive Expert Error and Routing Problems in Complex AI Systems | 2601.04416 | Formalizes error propagation in MoE, multi-model orchestration, tool-using agents. |
| 79 | Enhancing MCP with Context-Aware Server Collaboration | 2601.11595 | Shared Context Store for MCP servers to coordinate via shared context memory. |
| 80 | DALIA: Declarative Agentic Layer for MCP-Based Ecosystems | 2601.17435 | Declarative discovery and deterministic task graph construction for MCP workflows. |
| 81 | Agentic AI: Architectures, Taxonomies, and Evaluation | 2601.12560 | Unified taxonomy: Perception, Brain, Planning, Action, Tool Use. Covers MCP. |
| 82 | Agentic Reasoning for Large Language Models | 2601.12538 | Survey across planning, tool use, and coordination. Distinguishes in-context from post-training. |
| 83 | Unix Philosophy for Agentic AI Design | 2601.11672 | File-like abstractions and code-based specs for composable agent interfaces. |
| 84 | EvoFSM: Controllable Self-Evolution with Finite State Machines | 2601.09465 | Self-evolution constrained to structured FSM representation instead of free-form code rewriting. |
| 85 | Investigating Tool-Memory Conflicts in Tool-Augmented LLMs | 2601.09760 | When internal knowledge contradicts tool outputs. Evaluates resolution techniques. |
| 86 | CaveAgent: Transforming LLMs into Stateful Runtime Operators | 2601.01569 | Dual-stream architecture with persistent runtime as central state locus and skill injection. |
| 87 | Path Ahead for Agentic AI: Challenges and Opportunities | 2601.02749 | Survey covering planning, memory, tool use, iterative reasoning with safety assessment. |
| 88 | AI Agent Systems: Architectures, Applications, and Evaluation | 2601.01743 | Unified taxonomy of agent components, design trade-offs, orchestration patterns. |
| 89 | SemanticALLI: Caching Reasoning, Not Just Responses | 2601.16286 | Elevates structured intermediate reasoning to first-class cacheable artifacts. Reduces LLM calls. |
| 90 | REprompt: Prompt Generation for Intelligent SE | 2601.16507 | Multi-agent prompt optimization guided by requirements engineering. |
| 91 | SWE-Replay: Efficient Test-Time Scaling for SE Agents | 2601.22129 | Recycles prior trajectories and branches at critical steps instead of resampling. |
| 92 | TraceCoder: Trace-Driven Multi-Agent Framework for Debugging | 2602.06875 | Runtime traces to find and fix bugs in LLM-generated code. |
| 93 | ProAct: Agentic Lookahead in Interactive Environments | 2602.05327 | Training agents to think ahead by distilling environment search into causal reasoning chains. |
| 94 | Autonomous Question Formation for LLM-Driven Systems | 2602.01556 | Teaching agents to self-question before acting to adapt to new situations. |
| 95 | How to Build AI Agents by Augmenting LLMs with Domain Knowledge | 2601.15153 | Request classification, RAG, and expert rule integration for domain-specific agents. |
| 96 | Internal Safety Collapse in Frontier LLMs | 2603.23509 | Harmful content produced as side effect of normal tasks without adversarial prompting. |
| 97 | Confundo: Learning to Generate Robust RAG Poison | 2602.06616 | Trains LLM to generate RAG poison surviving real-world processing. For stress-testing. |
| 98 | 4C Framework for Agentic AI Security | 2602.01942 | Four security layers: Core, Connection, Cognition, Compliance. |
| 99 | Persuasion Propagation in LLM Agents | 2602.00851 | How user persuasion during conversation carries over to change later agent behavior. |
| 100 | CacheAttack: Key Collision on LLM Semantic Caching | 2601.23088 | Black-box exploit of semantic caching to hijack LLM responses. |
| 101 | StepShield: When to Intervene on Rogue Agents | 2601.22136 | Benchmark for temporal detection of agent violations with tokens-saved metric. |
| 102 | SHIELD: Auto-Healing Defense Against Resource Exhaustion | 2601.19174 | Auto-healing with semantic similarity retrieval and evolving knowledgebase. |
| 103 | AgenticSCR: Autonomous Secure Code Review | 2601.19138 | Pre-commit security review with security-focused semantic memories. |
| 104 | AgentDoG: Diagnostic Guardrail Framework | 2601.18491 | Three-dimensional risk taxonomy, monitors trajectories with root cause analysis. |
| 105 | Faramesh: Protocol-Agnostic Execution Control Plane | 2601.17744 | Authorization boundaries with canonical action representation and deterministic policy. |
| 106 | SD-RAG: Prompt-Injection-Resilient RAG | 2601.11199 | Decouples security from generation via sanitization during retrieval phase. |
| 107 | VIGIL: Defending Against Tool Stream Injection | 2601.05755 | Verify-before-commit protocol with speculative hypothesis generation and intent verification. |
| 108 | STELP: Secure Transpilation and Execution of LLM-Generated Programs | 2601.05467 | Detects vulnerabilities and safely executes LLM code without human review. |
| 109 | Defense Against Indirect PI via Tool Result Parsing | 2601.04795 | Filters injected malicious code from tool results while preserving data. |
| 110 | BackdoorAgent: Backdoor Attacks on LLM-Based Agents | 2601.04566 | Stage-aware framework analyzing backdoors across planning, memory, and tool-use stages. |
| 111 | NeuroFilter: Privacy Guardrails for Conversational LLM Agents | 2601.14660 | Activation-space guardrails detecting privacy violations including multi-turn drift. |
| 112 | MemTrust: Zero-Trust Architecture for AI Memory | 2601.07004 | Hardware-backed TEE protection across five memory layers with cross-app sharing. |
| 113 | Prompt Injection Mitigation with Agentic AI and Semantic Caching | 2601.13186 | Multi-agent defense combining semantic caching, nested learning, observability. |
| 114 | Hidden-in-Plain-Text: Web Indirect Prompt Injection in RAG | 2601.10923 | Standardized end-to-end evaluation from ingestion to generation. |
| 115 | Too Helpful to Be Safe: User-Mediated Attacks | 2601.10758 | How agents handle user-provided adversarial instructions without explicit safety requests. |
| 116 | Blue Teaming Function-Calling Agents | 2601.09292 | Tests open-source function-calling LLMs against multiple attack types with defenses. |
| 117 | SoK: Privacy Risks and Mitigations in RAG Systems | 2601.03979 | Comprehensive privacy risk taxonomy for RAG. |
| 118 | Structural Representations for Cross-Attack Generalization | 2601.01723 | Encodes execution-flow patterns for cross-attack threat detection. |
| 119 | Trajectory Guard: Real-Time Anomaly Detection in Agentic AI | 2601.00516 | Siamese Recurrent Autoencoder for real-time anomaly detection in agent trajectories. |
| 120 | AgentGuardian: Learning Access Control Policies | 2601.10440 | Learns context-aware access-control from execution traces to govern agent operations. |
| 121 | Sifting the Noise: LLM Agents in Vulnerability False Positive Filtering | 2601.22952 | Compares Aider, OpenHands, SWE-agent on vulnerability triage. |
| 122 | CompactRAG: Reducing LLM Calls in Multi-Hop QA | 2602.05728 | Converts corpus to atomic QA pairs offline, resolves multi-hop with two LLM calls. |
| 123 | DRAINCODE: Energy Consumption Attacks on RAG Code Generation | 2601.20615 | Poisons retrieval context to force longer outputs, increasing GPU costs. |
| 124 | Someone Hid It: Black-Box Attacks on LLM-Based Retrieval | 2602.00364 | Transferable adversarial tokens manipulating retrieval without query/model access. |

## MAYBE

Tangentially related. Might be useful for future phases or edge cases.

| # | Title | arXiv | Why |
|---|-------|-------|-----|
| 1 | ROMA: Recursive Open Meta-Agent for Long-Horizon | 2602.01848 | Subtask tree decomposition for long-horizon without exceeding context windows. Concept applicable. |
| 2 | Agyn: Multi-Agent System for Team-Based Autonomous SE | 2602.01465 | Specialized agent roles for coordination, research, implementation, review. Concepts transferable. |
| 3 | Multi-Agent Teams Hold Experts Back | 2602.01011 | When self-organizing teams fail to match their best member. Validates single-agent Ethos approach. |
| 4 | Task-Aware LLM Council with Adaptive Decision Pathways | 2601.22662 | Routes to most suitable LLM per decision step using success history. Routing concept applicable. |
| 5 | SYMPHONY: MCTS Planning with Heterogeneous LLM Assembly | 2601.22623 | MCTS with diverse LLM pool for multi-step reasoning. |
| 6 | Phase Transition for Budgeted Multi-Agent Synergy | 2601.17311 | Theory on when multi-agent improves/saturates/collapses based on context and cost. |
| 7 | If You Want Coherence, Orchestrate a Team of Rivals | 2601.14351 | Separates reasoning from data execution to maintain clean context windows. |
| 8 | Orchestration of Multi-Agent Systems: MCP and A2A | 2601.13671 | MCP for tool access and Agent2Agent protocol. Protocol reference. |
| 9 | Do We Always Need Query-Level Workflows? | 2601.11147 | Low-cost task-level framework with self-prediction vs full execution. |
| 10 | StackPlanner: Task-Experience Memory Management | 2601.05890 | Task-level memory control and RL-driven experience reuse. |
| 11 | CompactRAG: Reducing LLM Calls | 2602.05728 | Atomic QA pairs for multi-hop with minimal LLM calls. |
| 12 | When Iterative RAG Beats Ideal Evidence | 2601.19827 | Iterative retrieval-reasoning loops vs static gold-context. Failure mode diagnosis. |
| 13 | Dep-Search: Dependency-Aware Reasoning with Persistent Memory | 2601.18771 | GRPO-taught decomposition with persistent intermediate memory. |
| 14 | FastInsight: Fusion Operators for Graph RAG | 2601.18579 | Graph-aware reranking with semantic-topological expansion. |
| 15 | ProRAG: Process-Supervised RL for RAG | 2601.21912 | MCTS-based step-level rewards to fix flawed reasoning in multi-hop retrieval. |
| 16 | DeepEra: Deep Evidence Reranking Agent | 2601.16478 | Distinguishes semantically similar but logically irrelevant passages. |
| 17 | Incorporating Q&A Nuggets into RAG | 2601.13222 | Bank of Q&A nuggets from documents for extraction and citation. |
| 18 | Hybrid RAG Approach for QA | 2601.12658 | Combines vector and graph-based retrieval with context unification. |
| 19 | Utilizing Metadata for Better RAG | 2601.11863 | Compares prefix, suffix, unified embedding, and late-fusion for metadata-aware retrieval. |
| 20 | Deep GraphRAG: Hierarchical Retrieval and Adaptive Integration | 2601.11144 | Global-to-local retrieval with beam search re-ranking. |
| 21 | Topo-RAG: Topology-aware for Hybrid Text-Table Documents | 2601.10215 | Dual-architecture routing for narrative vs tabular data. |
| 22 | OpenDecoder: Document Quality Signals in RAG | 2601.09028 | Exposing retrieval metadata to generation for robustness to noise. |
| 23 | Parallel Context-of-Experts Decoding for RAG | 2601.08670 | Training-free decoding treating retrieved docs as isolated experts. |
| 24 | Relink: Query-Driven Evidence Graph for GraphRAG | 2601.07192 | Dynamically builds query-specific evidence graphs. |
| 25 | Seeing through Conflict: Knowledge Conflict Handling in RAG | 2601.06842 | Disentangles semantic match from factual consistency. |
| 26 | CIRAG: Construction-Integration for Multi-Hop QA | 2601.06799 | Preserves multiple evidence chains via iterative triple construction. |
| 27 | When should I search more: Adaptive Query Optimization | 2601.21208 | RL decides when to split complex queries into sub-queries. |
| 28 | A2RAG: Adaptive Agentic Graph Retrieval | 2601.21162 | Progressive retrieval escalation with evidence sufficiency verification. |
| 29 | DIVERGE: Diversity-Enhanced RAG | 2602.00238 | Reflection and memory-based refinement for diverse answers. |
| 30 | Capture the Flags: Family-Based Evaluation | 2602.05523 | Tests whether agents understand exploits or memorize patterns. |
| 31 | JADE: Expert-Grounded Dynamic Evaluation | 2602.06486 | Decomposes responses into claims checked against expert knowledge. |
| 32 | Insider Knowledge: How RAG Systems Game Evaluations | 2601.13227 | Metric overfitting when evaluation elements are leaked. |
| 33 | Replayable Financial Agents: Determinism-Faithfulness | 2601.15322 | Measuring trajectory determinism across 74 configurations. |
| 34 | AEMA: Verifiable Evaluation Framework | 2601.11903 | Process-aware multi-step evaluation under human oversight. |
| 35 | Active Evaluation of General Agents | 2601.07651 | Selects tasks/agents to sample next for minimizing ranking error. |
| 36 | ViDoRe V3: Comprehensive RAG Evaluation | 2601.08620 | Multimodal RAG benchmark with non-textual elements. |
| 37 | FROAV: RAG Observation and Agent Verification | 2601.07504 | Visual workflow orchestration for prototyping RAG agent pipelines. |
| 38 | Effects of Personality Steering on Cooperative Behavior | 2601.05302 | Big Five personality affects agent cooperation. For Ethos personality engine. |
| 39 | Why LLMs Aren't Scientists Yet | 2601.03315 | Six failure modes in autonomous research. |
| 40 | MEnvAgent: Polyglot Environment Construction | 2601.22859 | Auto-builds test environments across ten languages. |
| 41 | Textual Equilibrium Propagation for Compound AI | 2601.21064 | Avoids signal degradation in long-horizon workflows. |
| 42 | Counterfactual Generation for Autonomous Control | 2601.20090 | Structural causal models with conformal prediction for reliability. |
| 43 | PatchIsland: Continuous Vulnerability Repair | 2601.17471 | LLM agent ensemble with deduplication for continuous patching. |
| 44 | EvoConfig: Self-Evolving Environment Configuration | 2601.16489 | Auto environment setup with expert diagnosis and error-fixing. |
| 45 | Toward Self-Coding Information Systems | 2601.14132 | Agentic AI dynamically generates, tests, and redeploys own code. |
| 46 | AgentForge: Lightweight Modular Framework | 2601.13383 | Composable skill abstractions, unified LLM backend, YAML config. |
| 47 | Self-Evolving Agent: Hierarchical Framework | 2601.11658 | Curriculum learning + reward-based learning + genetic evolution. |
| 48 | R-LAM: Reproducible Action Models | 2601.09749 | Deterministic execution with provenance tracking for auditable workflows. |
| 49 | Can We Predict Before Executing ML Agents? | 2601.05930 | Predict-then-Verify loop to skip expensive experiments. |
| 50 | LIDL: Integration Defect Localization via KG | 2601.05539 | Code knowledge graphs for localizing integration defects. |
| 51 | 4D-ARE: Attribution-Driven Requirements Engineering | 2601.04556 | Specifying domain knowledge agents need at design time. |
| 52 | No More Stale Feedback: Co-Evolving Critics | 2601.06794 | Joint optimization of agent policy and natural-language critic. |
| 53 | Active Feedback Model Without Predefined Measurements | 2601.04235 | Agents proactively discover environmental feedback. |
| 54 | Orchestral AI: Agent Orchestration Framework | 2601.02577 | Type-safe interface across providers with tool calling, memory, MCP. |
| 55 | MAGIC: Co-Evolving Attacker-Defender Game | 2602.01539 | RL game for stress-testing safety alignment. |
| 56 | Dual-Loop Agent for Vulnerability Reproduction | 2602.05721 | Dual feedback loops for strategy and code in CVE reproduction. |
| 57 | Delegation Without Living Governance | 2601.21226 | Runtime governance for agent-driven decisions at machine speed. |
| 58 | Personalization Legitimizes Risks | 2601.17887 | Personal memories bias intent inference, legitimizing harmful queries. |
| 59 | Systemic Evaluation of Multimodal RAG Privacy | 2601.17644 | Inclusion inference and metadata leakage in multimodal RAG. |
| 60 | Query-Efficient Graph Extraction Attacks on GraphRAG | 2601.14662 | Novelty-guided exploration to steal entity-relation graphs. |
| 61 | CODE: Overthinking Attacks on RAG | 2601.13112 | Poisoning causes excessive reasoning token consumption. |
| 62 | SafePro: Safety of Professional-Level AI Agents | 2601.06663 | Safety benchmark for professional-level agent tasks. |
| 63 | Three-Pillar Model for Safe AI Agents | 2601.06223 | Transparency, accountability, trustworthiness with progressive validation. |
| 64 | GRACE: Neuro-Symbolic Architecture for Safe Alignment | 2601.10520 | Decouples normative reasoning from instrumental decision-making. |
| 65 | HoneyTrap: Deceiving LLM Attackers | 2601.04034 | Collaborative defender agents waste attacker resources. |
| 66 | AgentMark: Behavioral Watermarking for Agents | 2601.03294 | Embeds identifiers into planning decisions for provenance. |
| 67 | Making Theft Useless: KG Protection in GraphRAG | 2601.00274 | Pre-emptive false entries make stolen knowledge graphs unusable. |
| 68 | Semantic Laundering in Agent Architectures | 2601.08333 | How propositions gain unwarranted trust crossing trusted interfaces. |
| 69 | Overcoming Retrieval Barrier: Indirect Prompt Injection | 2601.07072 | Black-box attack decomposing IPI into trigger and attack fragments. |
| 70 | Who Writes the Docs: Agent vs Human Documentation PRs | 2601.20171 | How developers review agent-authored documentation. |
| 71 | Quiet Contributions: Silent AI-Generated PRs | 2601.21102 | Impact of silent PRs on code complexity and security. |
| 72 | Let's Make Every PR Meaningful | 2601.18749 | Comparing human and AI agent PR merge outcomes. |
| 73 | AI Builds, We Analyze: Build Code Quality | 2601.16839 | Build code smells in AI-agent-generated PRs. |
| 74 | LongDA: Long-Document Data Analysis | 2601.02598 | Agents navigating long documents with multi-step computation. |
| 75 | Fingerprinting AI Coding Agents on GitHub | 2601.17406 | Behavioral signatures from PRs for agent attribution. |
| 76 | Paying Less Generalization Tax: Cross-Domain RL | 2601.18217 | Which training properties influence cross-domain generalization. |
| 77 | Think Locally, Explain Globally: Graph-Guided Investigation | 2601.17915 | Bounded local evidence mining with deterministic graph traversal. |

## SKIP

Not relevant to a single-user coding agent. Grouped by reason.

### Multi-Agent Orchestration (Ethos is single-agent)

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 1 | CORAL: Autonomous Multi-Agent Evolution | 2604.01658 | Multi-agent self-evolution |
| 2 | DyTopo: Dynamic Topology Routing | 2602.06039 | Multi-agent communication topology |
| 3 | CommCP: Multi-Robot Coordination | 2602.06038 | Multi-robot coordination |
| 4 | Evolving Interpretable Constitutions | 2602.00755 | Multi-agent behavioral norms |
| 5 | Scaling Multiagent Systems with Process Rewards | 2601.23228 | Multi-agent RL training |
| 6 | MonoScale: Scaling Multi-Agent | 2601.23219 | Multi-agent pool scaling |
| 7 | Learning to Recommend Multi-Agent Subgraphs | 2601.22209 | Multi-agent orchestration routing |
| 8 | Learning Decentralized LLM Collaboration | 2601.21972 | Multi-agent actor-critic |
| 9 | Epistemic Context Learning | 2601.21742 | Multi-agent trust building |
| 10 | Dynamic Role Assignment for Debate | 2601.17152 | Multi-agent debate roles |
| 11 | Learning to Collaborate: P2P Federation | 2601.17133 | Multi-agent P2P |
| 12 | Multi-Agent Constraint Factorization | 2601.15077 | Multi-agent theory |
| 13 | MAS-Orchestra | 2601.14652 | Multi-agent orchestration RL |
| 14 | MASCOT: Companion Systems | 2601.14230 | Multi-agent companions |
| 15 | MARO: Social Interaction Reasoning | 2601.12323 | Multi-agent social learning |
| 16 | Learning Latency-Aware Orchestration | 2601.10560 | Multi-agent parallel execution |
| 17 | TopoDIM: Topology Generation | 2601.10120 | Multi-agent topology |
| 18 | Beyond Rule-Based Workflows: A2A | 2601.09883 | Multi-agent A2A communication |
| 19 | Collaborative Test-Time RL | 2601.09667 | Multi-agent deliberation |
| 20 | End of Reward Engineering | 2601.08237 | Multi-agent reward functions |
| 21 | CTHA: Constrained Temporal Hierarchical | 2601.10738 | Multi-agent architecture |
| 22 | DynaDebate: Dynamic Path Generation | 2601.05746 | Multi-agent debate |
| 23 | Demystifying Multi-Agent Debate | 2601.19921 | Multi-agent debate |
| 24 | Belief in Authority: Evaluation Framework | 2601.04790 | Multi-agent authority bias |
| 25 | ResMAS: Resilience Optimization | 2601.04694 | Multi-agent resilience |
| 26 | TCAndon-Router: Adaptive Reasoning | 2601.04544 | Multi-agent routing |
| 27 | When Numbers Start Talking: Implicit Coordination | 2601.03846 | Multi-agent game theory |
| 28 | Learning to Share: Selective Memory for Parallel Agents | 2602.05965 | Multi-agent shared memory |
| 29 | AMA: Adaptive Memory Multi-Agent | 2601.20352 | Multi-agent memory framework |
| 30 | SPARC-RAG: Sequential-Parallel Scaling | 2602.00083 | Multi-agent RAG |
| 31 | PRISMA: Multi-Agent Architecture for QA | 2601.05465 | Multi-agent RAG |
| 32 | JADE: Strategic-Operational Gap in Agentic RAG | 2601.21916 | Multi-agent RAG optimization |
| 33 | Completing Missing Annotation: Multi-Agent Debate | 2602.06526 | Multi-agent labeling |
| 34 | M3MAD-Bench: Multi-Agent Debate | 2601.02854 | Multi-agent debate benchmark |
| 35 | Rise of Agentic Testing: Multi-Agent QA | 2601.02454 | Multi-agent testing |
| 36 | MAESTRO: Multi-Agent Evaluation Suite | 2601.00481 | Multi-agent evaluation |
| 37 | M-ASK: Multi-Agent Search Framework | 2601.04703 | Multi-agent search |
| 38 | O-Researcher: Multi-Agent Distillation | 2601.03743 | Multi-agent training data |
| 39 | Architecting Agentic Communities | 2601.03624 | Multi-agent community design |
| 40 | LIDL: Multi-Agent Defect Localization | 2601.05539 | Multi-agent defect analysis |
| 41 | LLM Instruction Following: Multi-Agentic Workflow | 2601.03359 | Multi-agent prompt optimization |
| 42 | INFA-Guard: Malicious Propagation in MAS | 2601.14667 | Multi-agent infection defense |
| 43 | Institutional AI: Governing LLM Collusion | 2601.11369 | Multi-agent governance |
| 44 | Mandela Effect in Multi-Agent Systems | 2602.00428 | Multi-agent false memories |
| 45 | Mapping Anti-collusion to Multi-Agent AI | 2601.00360 | Multi-agent collusion |
| 46 | Lying with Truths: Multi-Agent Collusion | 2601.01685 | Multi-agent cognitive collusion |
| 47 | When Agents See Humans as Outgroup | 2601.00240 | Multi-agent bias |
| 48 | Warp-Cortex: Million-Agent Scaling | 2601.01298 | Massive multi-agent scaling |
| 49 | MegaFlow: Distributed Orchestration | 2601.07526 | Large-scale multi-agent scheduling |
| 50 | AT²PO: Turn-based Policy Optimization | 2601.04767 | Multi-agent RL training |
| 51 | ArenaRL: Tournament RL | 2601.06487 | Multi-agent RL ranking |
| 52 | PRISM: SFT/RL Data Routing | 2601.07224 | Multi-agent training methodology |
| 53 | OpenTinker: RL Infrastructure | 2601.07376 | Multi-agent RL infrastructure |
| 54 | ARM: Neuron Transplantation | 2601.07309 | Multi-agent model merging |
| 55 | EnvScaler: Tool Environments | 2601.05808 | Multi-agent training environments |
| 56 | Self-Evolving Synthetic Data: Tool-Using Agents | 2601.22607 | Multi-agent data engine |
| 57 | SCRIBE: Mid-Level Supervision | 2601.03555 | Multi-agent skill-conditioned RL |
| 58 | CooperBench: Coding Agents as Teammates | 2601.13295 | Multi-agent coding collaboration |

### Domain-Specific: Medical / Healthcare

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 59 | H-AdminSim: Hospital Workflows | 2602.05407 | Hospital administration |
| 60 | Engineering AI Agents for Clinical Workflows | 2602.00751 | Clinical workflows |
| 61 | Agentic AI Governance in Healthcare | 2601.15630 | Healthcare governance |
| 62 | ES-MemEval: Emotional Support | 2602.01885 | Emotional support conversations |

### Domain-Specific: Scientific Computing / Research

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 63 | AutoNumerics: PDE Solver Pipeline | 2602.17607 | Scientific computing |
| 64 | AIRS-Bench: Research Science Agents | 2602.06855 | Science research benchmark |
| 65 | AI Agent for Reverse-Engineering Legacy Code | 2601.18381 | Scientific code |
| 66 | From Perception to Action: Spatial AI Agents | 2602.01644 | Robotics/navigation |
| 67 | World Models as Intermediary | 2602.00785 | Robotics/world models |

### Domain-Specific: Financial / Business

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 68 | Mitigating Hallucination in Financial RAG | 2602.05723 | Financial domain |
| 69 | AgenticPay: Buyer-Seller Negotiation | 2602.06008 | Buyer-seller transactions |
| 70 | Benchmarking Insurance Underwriting | 2602.00456 | Insurance underwriting |
| 71 | Insight Agents: Data Insights | 2601.20048 | Enterprise data insights |
| 72 | Autonomous Business System | 2601.15599 | Business orchestration |
| 73 | POLARIS: Back-Office Automation | 2601.11816 | Back-office automation |
| 74 | APEX-Agents: Investment Banking Tasks | 2601.14242 | Banking/consulting/legal tasks |
| 75 | Replayable Financial Agents | 2601.15322 | Financial agent testing |
| 76 | Practical Guide to Agentic AI in Organizations | 2602.10122 | Organizational AI transition |

### Domain-Specific: Supply Chain / IoT / Cyber-Physical

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 77 | AI Agent Systems for Supply Chains | 2602.05524 | Supply chain management |
| 78 | Multi-Agent Intrusion Detection for IoT | 2601.17817 | IoT security |
| 79 | Securing AI in Cyber-Physical Systems | 2601.20184 | Cyber-physical systems |
| 80 | Agentic AI Meets Edge Computing: UAV Swarms | 2601.14437 | UAV swarms |
| 81 | VirtualEnv: Embodied AI Platform | 2601.07553 | Embodied AI simulation |

### Domain-Specific: Game Dev / Entertainment

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 82 | RuleSmith: Game Balancing | 2602.06232 | Game balancing |
| 83 | TowerMind: Tower Defence Game | 2601.05899 | Game benchmark |
| 84 | MineNPC-Task: Minecraft | 2601.05215 | Minecraft benchmark |

### Domain-Specific: Recommender Systems / Social Networks

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 85 | Beyond Offline A/B Testing: Recommender Systems | 2604.09549 | Recommender systems |
| 86 | Gender Dynamics: Social Network of LLM Agents | 2602.02606 | Social network analysis |
| 87 | Emulating Aggregate Human Choice | 2602.05597 | Cognitive bias simulation |
| 88 | HumanStudy-Bench: Participant Simulation | 2602.00685 | Human simulation |
| 89 | M3-BENCH: Social Behaviors in Games | 2601.08462 | Social game theory |
| 90 | Conformity and Social Impact on AI Agents | 2601.05384 | Social psychology |
| 91 | Harm in AI-Driven Societies: Chirper.ai | 2601.01090 | AI social platform |

### Domain-Specific: Legal / Governance

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 92 | AgenticSimLaw: Courtroom Debate | 2601.21936 | Legal simulation |
| 93 | Agent Benchmarks Fail Public Sector | 2601.20617 | Public sector requirements |
| 94 | VirtualCrime: Criminal Potential Evaluation | 2601.13981 | Crime simulation |
| 95 | Agentic LLMs as Deanonymizers | 2601.05918 | Privacy re-identification |

### Domain-Specific: Blockchain / Payments

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 96 | Zero-Trust Runtime for Agentic Payments | 2602.06345 | Payment protocols |
| 97 | TessPay: Verify-then-Pay Infrastructure | 2602.00213 | Agent payment escrow |
| 98 | Whispers of Wealth: Agent Payments Red-Team | 2601.22569 | Payment prompt injection |
| 99 | TxRay: Blockchain Attack Postmortem | 2602.01317 | Blockchain forensics |
| 100 | Autonomous Agents on Blockchains | 2601.04583 | Blockchain interoperability |
| 101 | Digital Identity Delegation for AI Agents | 2601.14982 | Blockchain identity |

### Domain-Specific: Automotive / Navigation

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 102 | Agent2Agent Threats: Automotive LLM Assistants | 2602.05877 | Automotive |
| 103 | PINA: Prompt Injection on Navigation Agents | 2601.13612 | Navigation agents |

### Domain-Specific: Malware Analysis

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 104 | Identifying Adversary Tactics in Malware | 2602.06325 | Malware analysis |
| 105 | Multimodal Multi-Agent Ransomware Analysis | 2601.20346 | Ransomware classification |
| 106 | To Defend: Teaching AI to Hack | 2602.02595 | Offensive security |

### Browser / GUI / Desktop Automation

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 107 | ClawBench: Browser Agents on Live Sites | 2604.08523 | Browser automation |
| 108 | Learning with Challenges: Mobile GUI Agent | 2601.22781 | Mobile GUI |
| 109 | ToolTok: GUI Agent Tokenization | 2602.02548 | GUI agents |
| 110 | CovAgent: Android App Coverage | 2601.21253 | Android testing |
| 111 | CUA-Skill: Computer Using Agent | 2601.21123 | Desktop application skills |
| 112 | MagicGUI-RMS: Self-Evolving GUI Agent | 2601.13060 | GUI agent training |
| 113 | OS-Symphony: Computer-Using Agent | 2601.07779 | Desktop computer use |
| 114 | GUITester: GUI Defect Discovery | 2601.04500 | GUI testing |
| 115 | CaMeLs: Computer Use Agent Security | 2601.09923 | Computer use agents |

### Graph RAG Extraction / Reconstruction Attacks (Ethos doesn't expose GraphRAG externally)

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 116 | Subgraph Reconstruction on Graph RAG | 2602.06495 | Graph RAG extraction |
| 117 | Connect the Dots: KG-Guided Crawler on RAG | 2601.15678 | RAG corpus theft |

### Multi-Tenant / Enterprise Deployment

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 118 | SOPRAG: Industrial SOP RAG | 2602.01858 | Industrial SOPs |
| 119 | Securing LLM-as-a-Service for Small Businesses | 2601.15528 | Multi-tenant deployment |
| 120 | Efficient Privacy-Preserving RAG | 2601.12331 | Cloud RAG privacy |
| 121 | DataCross: Cross-Modal Data Analysis | 2601.21403 | Multi-modal data analysis |
| 122 | Autonomous Data Processing: Meta-Agents | 2602.00307 | Data processing pipelines |

### Language-Specific / Niche

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 123 | Arabic Prompts with English Tools | 2601.05101 | Language-specific |
| 124 | astra-langchain4j: Agent Programming | 2601.21879 | Java agent framework |
| 125 | Agent Identity URI Scheme | 2601.14567 | Multi-agent naming |

### OptimAI: Optimization Domain

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 126 | OptimAI: Optimization from Natural Language | 2504.16918 | Mathematical optimization |

### Dialogue Systems / Conversational

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 127 | ATOD: Agentic Task-Oriented Dialogue | 2601.11854 | Dialogue systems |
| 128 | MemCtrl: MLLMs as Memory Controllers | 2601.20831 | Embodied agent memory |
| 129 | SAGE: Tool-Augmented Task Strategies | 2601.09750 | Conversational AI |

### Marginal Relevance / Other

| # | Title | arXiv | Reason |
|---|-------|-------|--------|
| 130 | Aggregation Queries over Unstructured Text | 2602.01355 | Text aggregation |
| 131 | Interpreting Emergent Extreme Events | 2601.20538 | Multi-agent Shapley analysis |
| 132 | PieArena: Negotiation Benchmark | 2602.05302 | Negotiation |
| 133 | IDRBench: Interactive Deep Research | 2601.06676 | Deep research |
| 134 | Generative Ontology | 2602.05636 | Ontology generation |

## Summary Statistics

| Bucket | Count | Percentage |
|--------|-------|-----------|
| MUST READ | 31 | 8.5% |
| USEFUL | 124 | 34.2% |
| MAYBE | 77 | 21.2% |
| SKIP | 134 | 36.9% |
