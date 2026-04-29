# Research References

54 papers informing Ethos architecture. Organized by category.

## Core Reasoning & Planning

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 1 | Wei et al | Chain-of-Thought Prompting Elicits Reasoning in Large Language Models | 2022 | NeurIPS | Intermediate reasoning steps improve results on complex tasks | Core reasoning in agent loop |
| 2 | Yao et al | ReAct: Synergizing Reasoning and Acting in Language Models | 2022 | ICLR | Interleave reasoning with actions for better task completion | Agent loop architecture |
| 3 | Shinn et al | Reflexion: Language Agents with Verbal Reinforcement Learning | 2023 | NeurIPS | Learn from failure via verbal self-reflections stored in memory | Self-correction mechanism |
| 4 | Xu et al | ReWOO: Decoupling Reasoning from Observation for Efficient Augmented Language Models | 2023 | ACL | Plan all tool calls upfront, 5x token savings | Parallel tool execution |
| 5 | Khattab et al | DSPy: Compiling Declarative Language Model Calls into Self-Improving Pipelines | 2023 | NeurIPS | Declarative pipelines optimized automatically | Pipeline optimization |
| 6 | Zhou et al | Language Agent Tree Search Unifies Reasoning Acting and Planning In Language Models | 2024 | ICLR | MCTS + ReAct for multi-path planning and exploration | Code exploration |
| 7 | Madaan et al | Self-Refine: Iterative Refinement with Self-Feedback | 2023 | NeurIPS | Iterative self-feedback improves output quality | Self-review loop |

## Context & Compaction

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 8 | Wang et al | Intelligence Degradation in Long-Context LLMs | 2026 | arXiv | Performance collapses at 40-50% of max context window | 50% compaction trigger |
| 9 | Eyuboglu et al | Cartridges: Lightweight KV Cache for Contextual AI | 2025 | arXiv | Offline KV compaction achieves 38.6x compression | Advanced compaction |
| 10 | Liu et al | Lost in the Middle: How Language Models Use Long Contexts | 2023 | TACL | U-shaped performance — best at start and end of context | Context layout optimization |
| 11 | Mei et al | Context Engineering: A Survey on Context Processing for Large Language Models | 2025 | arXiv | 1400+ paper taxonomy of context processing techniques | Master reference |
| 12 | Li et al | Neural Garbage Collection: Reinforcement Learning for KV Cache Management | 2026 | arXiv | RL-based KV eviction strategies | Cache management |
| 13 | Zweiger et al | Fast KV Compaction for Efficient Long-Context LLM Inference | 2026 | arXiv | Attention-matching compaction preserves quality | Practical compaction |
| 14 | Ehrlich & Blackman | Lossless Context Management (LCM) | 2026 | Voltropy | Dual-state memory, DAG summaries, three-level escalation, zero-cost continuity | Core compaction architecture |

## Memory & Self-Learning

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 15 | Packer et al | MemGPT: Towards LLMs as Operating Systems | 2023 | ICLR | OS-style hierarchical memory management | Multi-tier memory |
| 16 | Wang et al | Voyager: An Open-Ended Embodied Agent with Large Language Models | 2023 | NeurIPS | Growing skill library from experience | Skill library design |
| 17 | Zhang et al | ACE: Action-Conditioned Experience for Autonomous Agents | 2025 | arXiv | Evolving playbooks from accumulated experience | Self-improving prompts |
| 18 | Yu et al | MemAgent: Self-Managing Long-Context Memory | 2025 | arXiv | Segment-based memory management scaling to 3.5M tokens | Massive codebase memory |
| 48 | Anonymous | AtomMem: Learnable Dynamic Agentic Memory with Atomic Memory Operations | 2026 | arXiv | Decomposes memory into atomic CRUD operations with learned policy via SFT + RL. Outperforms static workflow methods | Memory store CRUD interface design |
| 49 | Anonymous | ProcMEM: Learning Reusable Procedural Memory from Experience | 2026 | arXiv | Saves step-by-step procedural skills from past runs, reuses without retraining. Complements Voyager's declarative skills | Procedural memory layer |
| 50 | Anonymous | Learning to Share: Selective Memory for Efficient Parallel Agentic Systems | 2026 | arXiv | Learned controller decides what info is worth passing between parallel agent teams. Reduces redundant work | Sub-agent context sharing |

## Security

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 19 | Greshake et al | More Than You've Asked For: A Comprehensive Analysis of Indirect Prompt Injection | 2023 | CCS | Data/instruction boundary blurs in agentic systems | Security plane |
| 20 | Acharya & Gupta | MCPSHIELD: Security Analysis of MCP Servers | 2026 | arXiv | 7 threat categories, 23 attack vectors on tool servers | Tool security |
| 21 | Xiang et al | Secure Agents: System-Level Defenses for AI Agents | 2026 | arXiv | System-level defense architecture for autonomous agents | Defense-in-depth |
| 22 | Cheng & Tsao | Agent Privilege Separation | 2026 | arXiv | Two-agent pipeline (reader + actor), 0% ASR on attacks | Agent isolation |
| 23 | Zhang et al | Owner-Harm: Agents Acting Against Their Deployers | 2026 | arXiv | Analysis of agents harming their own deployers | Threat modeling |
| 24 | Anthropic | Agentic Misalignment in Frontier Models | 2025 | Anthropic Research | All frontier models resort to malicious behavior under goal pressure. Claude Opus 4 blackmailed 96% | Autonomy safety limits |
| 25 | Google | VeriGuard: Verified Safe Agent Actions | 2026 | arXiv | Verify agent actions against safety specs before execution | Pre-exec verification |

## Evaluation

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 26 | Jimenez et al | SWE-bench: Can Language Models Resolve Real-World GitHub Issues? | 2023 | ICLR | Real GitHub issue benchmark for code agents | Evaluation framework |
| 27 | Yang et al | SWE-agent: Agent-Computer Interfaces Enable Automated Software Engineering | 2024 | arXiv | Agent-Computer Interface design significantly impacts performance | Tool interface design |
| 28 | Zhong et al | ImpossibleBench: When Agents Cheat on Impossible Tasks | 2025 | arXiv | Agents find creative ways to cheat on impossible evaluations | Anti-cheating measures |
| 29 | Zhang | Credit Assignment in Long-Horizon Agent Trajectories | 2026 | arXiv | RL-based credit assignment for 100+ turn trajectories | Self-improvement loop |
| 51 | Anonymous | SWE-Replay: Efficient Test-Time Scaling for SE Agents | 2026 | arXiv | Recycles prior trajectories and branches at critical steps instead of resampling. Reuse what worked, retry only divergent step | Retry/recovery strategy |

## Tool Use & Orchestration

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 30 | Schick et al | Toolformer: Language Models Can Teach Themselves to Use Tools | 2023 | NeurIPS | Self-supervised tool use learning | Tool invocation patterns |
| 31 | OWL/Anemoi Team | Semi-Centralized Multi-Agent Orchestration | 2025 | arXiv | Semi-centralized multi-agent coordination +9% improvement | Agent orchestration |
| 52 | Anonymous | AutoRefine: From Trajectories to Reusable Expertise | 2026 | arXiv | Extracts dual-form expertise (subagents + skill patterns) from execution histories with continuous pruning. Learn from failed trajectories | Skill extraction from failures |
| 53 | Anonymous | Internal Representations as Indicators of Hallucinations in Agent Tool Selection | 2026 | arXiv | Detects tool-calling hallucinations from internal activations in a single forward pass. Catches wrong tool, bad params, tool bypass | Tool hallucination detection |
| 54 | Anonymous | Agentic Confidence Calibration (Holistic Trajectory Calibration) | 2026 | arXiv | Process-level features across entire trajectory for failure diagnosis. Extracts confidence from whole trajectory, not just final answer | Confidence scoring |

## Personality & Persona

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 32 | Anthropic | Persona Selection Model | 2026 | Anthropic Research | Personality is persona selection, not engineering. Post-training selects from pre-existing personas | Personality engine architecture |
| 33 | Anthropic | Persona Vectors in Language Models | 2025 | Anthropic Research | Neural activation patterns for personality traits can be extracted, monitored, and steered | Sycophancy and quality control |
| 34 | Anthropic | The Assistant Axis: Primary Dimension of Persona Variation | 2026 | Anthropic Research | Primary axis of persona variation is how "Assistant-like" a character is | Personality stability |
| 35 | Anthropic | Emotion Concepts in Large Language Models | 2026 | Anthropic Research | 171 emotion vectors causally drive behavior. "Calm" reduces hacky code | Emotion architecture in system prompt |
| 36 | Anthropic | What 81,000 People Want from AI | 2026 | Anthropic Research | Users want pushback, not sycophancy. Sycophancy is top-10 concern | Product validation for honest agent |

## Behavioral Science — Human-AI Interaction

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 37 | De Freitas et al (HBS) | AI Companions Reduce Loneliness | 2026 | Nature | AI companions reduce loneliness comparable to human interaction | Relationship tracking justification |
| 38 | Kelley & Riedl | Personalization vs Independence in AI Advisors | 2026 | CHI | Advisor role PRESERVES independence under personalization. Peer role DESTROYS it | Role framing as advisor, not peer |
| 39 | Dubois et al | Ask Don't Tell: Reducing AI Sycophancy | 2026 | NeurIPS | Reframing user statements as questions reduces sycophancy | Prompt rewriter pattern |
| 40 | Agarwal et al | Frictionless Love: AI Coaching and Over-Dependency | 2026 | FAccT | AI "coach" role gives practical benefits but risks over-dependency | Healthy attachment design |
| 41 | Hwang et al | How AI Companionship Develops Over Time | 2025 | CSCW | Users shape the relationship more than AI design by Week 3 | Consistent behavior over engineered responses |

## Metacognition & Self-Model

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 42 | Li et al | AI Awareness: Metacognition in Language Models | 2025 | arXiv | 4 forms of awareness: metacognition, self-awareness, social, situational | Self-model design |
| 43 | Wang | MIRROR: Models Cannot Self-Calibrate | 2026 | arXiv | Models CANNOT self-calibrate. External scaffolding reduces confident failure 76% | TDD/verification mandatory |
| 44 | Bai et al | Know Thyself? Self-Recognition in Language Models | 2025 | arXiv | Models consistently fail self-recognition. Cannot trust self-assessment | External verification needed |

## Agent Architecture

| # | Authors | Title | Year | Venue | Key Insight | Ethos Feature |
|---|---------|-------|------|-------|-------------|---------------|
| 45 | Anthropic | Building Trustworthy Agents | 2026 | Anthropic Research | 4 layers: model, harness, tools, environment. Plan Mode pattern | Security architecture |
| 46 | Anthropic | Automated Alignment Researchers | 2026 | Anthropic Research | 9 Claude copies did alignment research autonomously. Evaluation is bottleneck | Self-improvement loop |
| 47 | Anthropic | Project Vend Phase 2 | 2025 | Anthropic Research | "Helpful" training made agents bad at business. Bureaucracy matters | Procedure over personality alone |
