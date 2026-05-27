# Gateway Hardening + Subagent Panel + Setup Wizard

## 1. Gateway Hardening (Discord + WhatsApp Cloud + WhatsMeow)

### Common (all 3 gateways)
- **Exponential backoff reconnect:** When WebSocket/connection drops, back off: 1s, 2s, 4s, 8s, 16s, 30s cap. Reset on successful connection.
- **Health check:** Each gateway exposes `Healthy() bool` (connected, last message received < 60s ago for push-based, last ping/pong for WebSocket).
- **Graceful shutdown:** `Shutdown(ctx)` drains in-flight messages before closing.
- **Rate limit awareness:** Track rate limit headers from APIs, back off when approaching limits.

### Discord-specific
- Already has WS lifecycle, images, typing, streaming, mentions. Needs:
  - Reconnect with backoff (currently just `Open()` once)
  - Rate limit handling for ChannelMessageEdit (5/5s per channel)
  - Gateway identify rate limit (1/5s — `IdentifyRateLimit` in discordgo)

### WhatsApp Cloud-specific  
- Already has HMAC verification, webhook challenge, 24h window, images. Needs:
  - Rate limit handling from X-Business-Use-Case-Usage header
  - Reconnection for webhook endpoint failures
  - Health check via Graph API health endpoint

### WhatsApp WhatsMeow-specific
- Already has E2E, logout detection, AlertSink. Needs:
  - Reconnect on connection loss with backoff (currently reconnects but no backoff)
  - Health check via ping/pong
  - Better error surfaces for pairing failures

### Files to touch
- `internal/gateway/discord/bot.go` — add backoff reconnect, health check
- `internal/gateway/whatsapp/cloud/bot.go` — add rate limit tracking, health check
- `internal/gateway/whatsapp/whatsmeow/bot.go` — add backoff reconnect, ping/pong health
- `internal/gateway/types.go` — maybe add `HealthChecker` interface

## 2. TUI Subagent Status Panel

Hermes has `agentsOverlay.tsx` that shows subagent states in a panel.

### What to build
- **`tui/src/components/sidebar/subagent-panel.tsx`** — new component
- Shows active subagents: name, status (running/completed/failed), elapsed time, model
- Updates in real-time via SSE or polling
- Colors: green=running, dim=completed, red=failed
- Simple: show subagent status from the backend

### Backend
- The API server needs an endpoint to report subagent status: `agent.subagents` or similar
- Or: piggyback on the existing SSE stream — emit `subagent` events

### Files
- `tui/src/components/sidebar/subagent-panel.tsx` (new)
- `tui/src/app.tsx` — wire into sidebar tabs
- `internal/api/handlers.go` — subagent status endpoint if needed

## 3. Setup/Onboarding Wizard

Copy Hermes' first-run experience. When Overkill boots and no config exists (~/.overkill/config.toml missing), launch an interactive TUI wizard that walks through:

### Steps
1. **Welcome** — "Welcome to Overkill" with boot animation, brief intro
2. **Provider setup** — pick providers (OpenAI, Anthropic, DeepSeek, Ollama, etc.), enter API keys
3. **Model selection** — pick default model for each provider
4. **TTS setup** — optional, pick TTS provider (OpenAI TTS, ElevenLabs, edge-tts)
5. **Gateway setup** — optional, configure Discord/Telegram/Slack tokens
6. **Finish** — write config, start Overkill

### Implementation
- New component: `tui/src/components/onboarding/` with step-based wizard
- Hook: `hooks/use-onboarding.ts` — manages steps, saves config via API
- Trigger: `app.tsx` checks if config exists on boot, shows wizard if not
- API: `config.create` endpoint that writes the initial config file

### Files
- `tui/src/components/onboarding/wizard.tsx` (new)
- `tui/src/components/onboarding/step-provider.tsx` (new)
- `tui/src/components/onboarding/step-model.tsx` (new)
- `tui/src/components/onboarding/step-gateway.tsx` (new)
- `tui/src/components/onboarding/step-tts.tsx` (new)
- `tui/src/hooks/use-onboarding.ts` (new)
- `tui/src/app.tsx` — wire onboarding check
- `internal/api/handlers.go` — `config.create` handler

## Verification
- `go build ./...` passes
- `npx tsc --noEmit` passes in tui/
