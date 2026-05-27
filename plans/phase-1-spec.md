# Phase 1: Ink Project Scaffold

## Goal
Create the TypeScript Ink TUI project at `/home/harsh/docker/overkill/tui/` that connects to the Go JSON-RPC backend (Phase 0) and displays a "Connected to Overkill" screen with status indicator.

## Reference
Look at `/home/harsh/.hermes/hermes-agent/ui-tui/` for Ink patterns. Hermes uses:
- `ink` v6 with React 19
- `ink-text-input` for input fields
- `nanostores` for state management
- `unicode-animations` for polish
- Custom `@hermes/ink` wrapper (we use vanilla `ink`)

## Files to Create

### 1. `tui/package.json`
```json
{
  "name": "overkill-tui",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "tsx src/entry.tsx",
    "start": "tsx src/entry.tsx",
    "build": "tsc",
    "typecheck": "tsc --noEmit"
  },
  "dependencies": {
    "ink": "^6.8.0",
    "ink-text-input": "^6.0.0",
    "react": "^19.2.0",
    "nanostores": "^1.2.0",
    "@nanostores/react": "^1.1.0",
    "unicode-animations": "^1.0.3"
  },
  "devDependencies": {
    "@types/react": "^19.2.0",
    "tsx": "^4.19.0",
    "typescript": "^5.7.0"
  }
}
```

### 2. `tui/tsconfig.json`
Standard Node ESM config. Target ES2022, module NodeNext, JSX react-jsx.

### 3. `tui/src/entry.tsx`
Main entry point:
- Import `{ render }` from `ink`
- Render `<App />` to stdout
- Handle exit

### 4. `tui/src/app.tsx`
Top-level component with providers:
- `ConnectionProvider` — manages backend connection state
- `ThemeProvider` — basic theme (dark/light, start with dark)
- Status bar at bottom showing connection state
- Center: "Overkill TUI" title and "Connected" or "Connecting..." status

### 5. `tui/src/backend/client.ts`
JSON-RPC 2.0 client:
- `connect()` — connects to Go backend (default: `http://localhost:PORT/rpc`)
- `call(method, params)` — makes JSON-RPC call, returns promise
- `stream(method, params)` — SSE streaming for agent.send
- Port configurable via env `OVERKILL_API_PORT` or auto-detect

### 6. `tui/src/backend/types.ts`
TypeScript interfaces matching the Go types from Phase 0:
- `SessionInfo`, `ProviderInfo`, `ModelInfo`, `HealthResult`
- `SendMessageParams`, `SendMessageResult`
- JSON-RPC envelope types

### 7. `tui/src/hooks/use-backend.ts`
React hook that provides:
- `backend` client instance
- `connected` boolean
- `error` string
- Auto-connect on mount with retry

### 8. `tui/src/components/status-bar.tsx`
Bottom bar showing:
- Connection status (green ● connected, yellow ◌ connecting, red ○ disconnected)
- Current model/provider
- Session name

## Constraints
- **Do NOT use @hermes/ink** — that's Hermes-specific, use vanilla `ink`
- **npm install must succeed** with `npm install` in tui/
- **Must compile** with `npm run typecheck` (or tsc --noEmit)
- **Entry point must run** with `npm start` (even if it shows "connecting" since Go server isn't running)
- **Use React hooks pattern** — functional components with useState, useEffect, useContext
- **Read Hermes TUI for patterns** but don't copy-paste. Look at how they structure providers and hooks.

## Verification
After creating files, please `npm install` and `npm run typecheck` to verify compilation.
