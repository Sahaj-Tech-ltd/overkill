# Quick Start

```sh
overkill                 # launch the TUI
overkill doctor          # self-test every subsystem with fix hints
overkill --help          # CLI subcommands
```

## First launch

The onboarding wizard walks you through:

1. Pick a provider (Anthropic, OpenAI, DeepSeek, Ollama, etc.)
2. Paste an API key or do an OAuth device flow
3. Pick a model
4. Pick a theme

Then type a question and hit Enter. Type `/help` for the full surface.

## Environment variables

| Variable | Effect |
|---|---|
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `OPENAI_API_KEY` | OpenAI API key |
| `GEMINI_API_KEY` | Google Gemini API key |
| `DEEPSEEK_API_KEY` | DeepSeek API key |
| `OVERKILL_HOME` | Override config directory (default: `~/.overkill` or `%LOCALAPPDATA%\overkill`) |
| `DATABASE_URL` | Postgres connection string |
| `OVERKILL_NO_ANIMATIONS` | Disable TUI animations (`=1`) |
| `OVERKILL_CELL_RENDER` | Enable SSH-friendly cell renderer (`=1`) |

## Basic workflow

```sh
overkill                              # start a session
> Write a function that reverses a string in Go
> /model                              # switch models mid-session
> /compact                            # compress context when it gets long
> /fork                               # branch from a past message
> /sessions                           # switch between saved sessions
> /quit                               # exit
```
