# Providers

Overkill supports any OpenAI-compatible API. First-class adapters exist for:

- **Anthropic** — Claude models via API key or OAuth
- **OpenAI** — GPT models via API key
- **Google Gemini** — via API key
- **DeepSeek** — via API key
- **Ollama** — local models via `http://localhost:11434`
- **OpenRouter** — unified API for 200+ models
- **Custom** — any OpenAI-compatible endpoint

Set provider credentials via environment variables (auto-detected) or in `config.toml`.

```sh
overkill model    # interactive model picker
```
