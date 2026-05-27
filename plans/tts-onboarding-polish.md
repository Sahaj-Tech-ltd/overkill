# TTS Tools + Onboarding Polish

## 1. TTS Tool (Go) — `internal/tools/tts/`

New tool: `tts.speak` — converts text to speech audio file.

### Providers to support (in order):
1. **edge-tts** (free, no key) — shell out to `edge-tts --text "..." --write-media out.mp3`
2. **KittenTTS** (free, local) — shell out to Python: `python3 -c "from kittentts import TTS; tts=TTS(); tts.generate('text')"` → save wav, convert to ogg with ffmpeg
3. **OpenAI TTS** — POST to `https://api.openai.com/v1/audio/speech` with API key from config
4. **ElevenLabs** — POST to `https://api.elevenlabs.io/v1/text-to-speech/{voice_id}`

### Tool interface:
```go
type TTSTool struct {
    Provider string // "edge", "kittentts", "openai", "elevenlabs"
    APIKey   string
}
func (t *TTSTool) Name() string { return "tts.speak" }
func (t *TTSTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
```

Input: `{"text": "...", "provider": "edge", "voice": "default"}`
Output: `{"audio_path": "/tmp/overkill-tts-xxxx.mp3", "format": "mp3", "duration_ms": 3200}`

### File: `internal/tools/tts/tts.go` (new package)

## 2. Onboarding Wizard Polish

### Add missing providers
Add to `AVAILABLE_PROVIDERS` in step-provider.tsx:
- `custom` — custom OpenAI-compatible provider (user enters name + base URL + API key)
- `together` — Together AI
- `fireworks` — Fireworks AI
- `perplexity` — Perplexity
- `cohere` — Cohere

### Add missing TTS providers
Add to `TTS_PROVIDERS` in step-tts.tsx:
- `kittentts` — KittenTTS (local, free)
- `play.ht` — Play.ht

### Provider smart detection
In step-provider.tsx, detect provider from API key prefix:
- `sk-` → suggest OpenAI
- `sk-ant-` → suggest Anthropic  
- `sk-deepseek` → suggest DeepSeek
- `gsk_` → suggest Groq
- When user pastes a key, auto-highlight the matching provider

### Gateway test button
In step-gateway.tsx, after entering tokens, add a `[Test]` option that calls `gateway.test` API endpoint to verify the token works before saving.

### Progress bar
In wizard.tsx, show step progress: `[███░░░] Step 3/5 — Choose Models`

## 3. API Endpoints

### `gateway.test`
Tests a gateway token without saving. Accepts `{gateway: "discord"|"telegram"|"slack", token: "..."}`, returns `{ok: bool, error?: string}`.

### `tts.speak` (registered as tool)
Already covered by the tool — registered in the tool registry.

## Verification
- `go build ./...` passes
- `npx tsc --noEmit` passes in tui/
- `go test ./...` passes
