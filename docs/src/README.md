# Overkill

The vibe-coding agent that actually has discipline.

Overkill is a Go coding-agent backend with a terminal interface and a development desktop workspace. It streams tool calls, applies configured approval policies, manages context with compaction and session forking, and uses PostgreSQL for persistent sessions. Standalone CLI mode can also run without a database, with reduced persistence. It connects to model providers, ACP, MCP servers and configured language servers.

## What makes Overkill different

- **ReAct loop with discipline** — think, act, observe. Tool execution follows the configured risk and approval policy.
- **TUI built for developers** — Ink/React terminal UI with syntax highlighting, themes, mouse support, virtual scrolling.
- **Provider-agnostic** — OpenAI, Anthropic, Gemini, DeepSeek, Ollama, OpenRouter, and any OpenAI-compatible endpoint.
- **Multiple entry points** — CLI, Node-based terminal frontend and a development Electron desktop. See [installation](install.md) for their different runtime requirements.
- **Extensions** — loaded skills, trusted file hooks and MCP tools. Plugin discovery and SDK code exist, but subprocess plugin execution is not wired into the shared agent runtime; see [extension runtime behavior](../extensions-runtime.md).
- **Context that doesn't rot** — LCM-style compaction, session forking, Postgres-backed persistence.

See the [architecture and storage map](architecture.md) for local file state,
service dependencies and unfinished experimental surfaces, and the
[gateway capability matrix](../gateways.md) for messaging setup and limitations.

## License

Dual-licensed under MIT or Apache-2.0 at your option.
