# Overkill

The vibe-coding agent that actually has discipline.

Overkill is a terminal coding agent that lives in your shell. It streams tool calls, gates risky actions behind explicit approval, manages its own context with a compact/fork model, and keeps every session in a Postgres store. It speaks to a dozen LLM providers, ACP, MCP servers, and the language servers you already have installed.

## What makes Overkill different

- **ReAct loop with discipline** — think, act, observe. Every tool call gated behind risk-classified approval.
- **TUI built for developers** — Ink/React terminal UI with syntax highlighting, themes, mouse support, virtual scrolling.
- **Provider-agnostic** — OpenAI, Anthropic, Gemini, DeepSeek, Ollama, OpenRouter, and any OpenAI-compatible endpoint.
- **Multi-platform** — Linux, macOS, Windows (native), WSL2. One binary, no runtime dependencies beyond `git`.
- **Plugin system** — JSON-RPC subprocess plugins in any language. Go SDK included.
- **Context that doesn't rot** — LCM-style compaction, session forking, Postgres-backed persistence.

## License

Dual-licensed under MIT or Apache-2.0 at your option.
