# TUI Bug Sweep Report

Generated: 2026-05-31
Scope: `/home/harsh/docker/overkill/tui/src/**/*.{ts,tsx}`
Cross-referenced against: `/home/harsh/docker/overkill/internal/api/types.go`, `handlers.go`, `server.go`, `skills_handlers.go`, `todo_handlers.go`, `config/config.go`

---

## 1. CRITICAL: Hardcoded API Ports — Dashboard Broken

### 1a. dashboard/api.ts uses hardcoded port 8420 and raw fetch, bypassing BackendClient

**File:** `src/components/dashboard/api.ts`
**Lines:** 2–13
**Severity:** 🔴 CRITICAL

```typescript
const DEFAULT_PORT = 8420; // Overkill web UI port (matches DefaultWebUIAddr)
```

The Go API server binds to `localhost:0` (OS-chosen random port at runtime). Port 8420 is the Web UI port, not the API port. Since the dashboard calls `fetch()` directly instead of using the `BackendClient`, it will never find the API. The dashboard will silently fail to load goal/plan data.

**Fix:** Remove the standalone `getBaseUrl()` / `fetch()` calls in `dashboard/api.ts`. Instead, accept a `BackendClient` instance and call `backend.call<R>("goal.get")` / `backend.call<R>("plan.get")` through the existing JSON-RPC client, which already uses the correct port (resolved from `OVERKILL_API_PORT` env var or the constructor).

---

### 1b. backend/client.ts default port 3000 may not match actual port

**File:** `src/backend/client.ts`
**Line:** 3
**Severity:** 🟡 MEDIUM

```typescript
const DEFAULT_PORT = 3000;
```

The Go server uses `localhost:0`, so port 3000 is a guess. This works only if the `OVERKILL_API_PORT` env var is correctly set by the TUI launcher (which it likely is). However, if the env var is ever unset, the TUI silently points at port 3000, which may be wrong.

**Fix:** Document that `OVERKILL_API_PORT` must be set, or add a fallback that reads a well-known file (e.g., `~/.overkill/api.port`) written by the Go server on startup.

---

## 2. CRITICAL: Banned Model References (gpt-4o-mini)

### 2a. step-model.tsx includes `gpt-4o-mini` in fallback models

**File:** `src/components/onboarding/step-model.tsx`
**Line:** 15
**Severity:** 🔴 CRITICAL

```typescript
openai: ["gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "o3-mini"],
```

The user has explicitly banned `gpt-4o-mini`. It must not appear in any fallback list.

**Fix:** Remove `"gpt-4o-mini"` from the array.

---

### 2b. step-vision.tsx includes `gpt-4o-mini` in vision models

**File:** `src/components/onboarding/step-vision.tsx`
**Line:** 16
**Severity:** 🔴 CRITICAL

```typescript
openai: ["gpt-4o", "gpt-4o-mini", "gpt-4-turbo"],
```

Same issue — `gpt-4o-mini` is banned.

**Fix:** Remove `"gpt-4o-mini"` from the array.

---

## 3. MEDIUM: Stale TODO Comments — Backend Methods Already Exist

### 3a. skills-panel.tsx TODO is outdated

**File:** `src/components/sidebar/skills-panel.tsx`
**Lines:** 17–20
**Severity:** 🟡 MEDIUM

```typescript
// TODO(backend): wire to RPC once skills.* methods are added to server.go
// Planned calls:
//   mount:  backend.call<SkillInfo[]>("skills.list")
//   toggle: backend.call("skills.toggle", { name, enabled })
```

The backend **already has** `skills.list` and `skills.toggle` registered in `server.go` (lines 467–472), and `skills_handlers.go` implements both. The panel already calls them on lines 45 and 52. The TODO comment is stale and misleading.

**Fix:** Remove the stale TODO comment block.

---

### 3b. todo-panel.tsx TODO is outdated

**File:** `src/components/sidebar/todo-panel.tsx`
**Lines:** 29–34
**Severity:** 🟡 MEDIUM

```typescript
// TODO(backend): wire to RPC once todo.* methods are added to server.go
// Planned calls:
//   mount:  backend.call<Todo[]>("todo.list")
//   add:    backend.call("todo.add", { description })
//   toggle: backend.call("todo.toggle", { id })
//   delete: backend.call("todo.delete", { id })
```

The backend **already has** `todo.add`, `todo.toggle`, `todo.delete`, and `todo.list` registered in `server.go` (lines 455–465), and `todo_handlers.go` implements all of them. The panel already calls them on lines 54, 61, 67, 73, 79. The TODO comment is stale.

**Fix:** Remove the stale TODO comment block.

---

## 4. MEDIUM: Type Mismatches with Go API

### 4a. SessionInfo missing fields

**File:** `src/backend/types.ts` lines 1–10, **Go:** `internal/api/types.go` lines 86–98

| TS field | Go field | Issue |
|---|---|---|
| `name: string` | `Name string` | OK |
| `title?: string` | `Title string` | Go always sends `title`, TS marks it optional |
| — | `model: string` | **Missing** in TS |
| — | `provider: string` | **Missing** in TS |
| — | `status: string` | **Missing** in TS |

**Fix:** Add `model`, `provider`, `status` fields to `SessionInfo`.

---

### 4b. ModelInfo inconsistencies

**File:** `src/backend/types.ts` lines 17–25, **Go:** `internal/api/types.go` lines 159–170

| TS field | Go field (JSON tag) | Issue |
|---|---|---|
| `maxTokens?: number` | `maxTokens` (from `DefaultMaxTokens`) | OK (json tag matches) |
| — | `family: string` | **Missing** in TS |
| — | `input_modalities: []string` | **Missing** in TS |
| — | `output_modalities: []string` | **Missing** in TS |

**Fix:** Add `family`, `input_modalities`, `output_modalities` to `ModelInfo`.

---

### 4c. ProviderInfo missing `type`

**File:** `src/backend/types.ts` lines 12–15, **Go:** `internal/api/types.go` lines 153–157

Go sends `type` field in `ProviderInfo` but TS doesn't include it.

**Fix:** Add `type: string` to `ProviderInfo`.

---

### 4d. AgentSendResult vs SendMessageResult field name mismatch

**File:** `src/backend/types.ts` lines 128–133, **Go:** `internal/api/types.go` lines 63–71

| TS field | JS key used in TS | Go JSON tag |
|---|---|---|
| `toolCalls?: unknown[]` | `toolCalls` | `tool_calls` (int) |
| `totalTokens?: number` | `totalTokens` | `total_tokens` (int) |

The TS type declares `toolCalls` as `unknown[]` but Go sends an `int`. Also `AgentSendResult` in TS is missing `blocked` and `block_reason` fields the Go side sends. These fields aren't used by the TUI (it uses streaming), so impact is low.

**Fix:** Align `AgentSendResult` with Go's `SendMessageResult` or add a note that this is the non-streaming response shape.

---

## 5. MEDIUM: Hardcoded Model/Provider Fallback Lists

### 5a. FALLBACK_MODELS in step-model.tsx

**File:** `src/components/onboarding/step-model.tsx`
**Lines:** 14–24
**Severity:** 🟡 MEDIUM

Contains hardcoded model names for 9 providers. These should be fetched from the backend via `models.list` or `providers.list` instead of being hardcoded. The hardcoded list will be stale immediately as providers add new models.

**Fix:** Call `backend.call("providers.list")` during onboarding to get live model lists. Use hardcoded fallback only when the backend is unavailable.

---

### 5b. FALLBACK_VISION_MODELS in step-vision.tsx

**File:** `src/components/onboarding/step-vision.tsx`
**Lines:** 15–25
**Severity:** 🟡 MEDIUM

Same issue — hardcoded vision-capable model lists. These should use `supports_vision` from the live `ModelInfo` response.

**Fix:** Fetch models from backend and filter by `supports_vision: true`.

---

## 6. LOW-MEDIUM: Missing/Silent Error Handling

### 6a. todo-panel.tsx swallows all errors silently

**File:** `src/components/sidebar/todo-panel.tsx`
**Lines:** 56, 63, 69, 75, 79
**Severity:** 🟡 MEDIUM

```typescript
.catch(() => {});  // repeated 5 times
```

All todo RPC errors are silently swallowed. If the tasks store is not configured, users get no feedback.

**Fix:** Log errors to the logger at minimum, or surface them in the panel UI.

---

### 6b. skills-panel.tsx swallows errors

**File:** `src/components/sidebar/skills-panel.tsx`
**Lines:** 47, 56
**Severity:** 🟡 MEDIUM

Same pattern — skills RPC failures are swallowed.

**Fix:** Log errors.

---

### 6c. SettingsPanel UsageTab swallows errors

**File:** `src/components/settings/SettingsPanel.tsx`
**Line:** 104
**Severity:** 🟢 LOW

```typescript
.catch(() => setReport(null))
```

If `session.usage` fails (e.g., cost tracker not configured), the user sees "No usage data available yet" instead of an error message.

**Fix:** Set an error state instead of silently nulling.

---

### 6d. config.exists catch sets exists=true on failure

**File:** `src/app.tsx`
**Lines:** 75–79
**Severity:** 🟡 MEDIUM

```typescript
.catch((err) => {
  log.error("config.exists check failed:", err);
  if (!cancelled) {
    setExists(true);  // <-- assumes config exists on any error
  }
})
```

If the backend is unreachable, this assumes config exists and skips onboarding entirely.

**Fix:** Set `exists` to `true` only for specific "config not found" RPC errors. For network/connection errors, surface the error instead of silently assuming config exists.

---

## 7. LOW: Hardcoded Strings / Magic Values

### 7a. Sidebar version string

**File:** `src/components/sidebar/sidebar.tsx`
**Line:** 56 (default), also passed from `app.tsx` line 266

```typescript
version="v3"
```

Hardcoded version. Should come from `package.json` or the backend health endpoint.

**Fix:** Read version from `process.env.npm_package_version` or the `status.health` RPC.

---

### 7b. Sidebar tab labels are hardcoded

**File:** `src/components/sidebar/sidebar.tsx`
**Lines:** 36–45

```typescript
const TABS: Array<{ id: SidebarTab; label: string }> = [
  { id: "sessions", label: "🎙 Sessions" },
  ...
];
```

Tab labels are hardcoded with emoji. Fine for a TUI, but would be better if configurable.

**Severity:** 🟢 LOW (cosmetic)

---

### 7c. Sidebar width hardcoded

**File:** `src/components/sidebar/sidebar.tsx`
**Line:** 34

```typescript
const SIDEBAR_WIDTH = 42;
```

**Severity:** 🟢 LOW — purely cosmetic in a TUI context.

---

### 7d. Memo phrases hardcoded

**File:** `src/components/memo-phrases.ts`
**Lines:** 33–264

All `BASE_PHRASE_RULES`, `DEFAULT_PHRASES`, and `actionPhrases` are hardcoded. The backend `memo.phrase` / `memo.learn` RPCs exist but `matchMemoPhrase()` is called synchronously on line 132 of `memo-banner.tsx`, bypassing the async `matchMemoPhraseAsync()` that actually calls the backend.

**Fix:** Switch `memo-banner.tsx` to use `matchMemoPhraseAsync()` so the backend phrase engine is consulted.

---

## 8. LOW: Dashboard double-borders / structural issue

**File:** `src/components/dashboard/DashboardCard.tsx`
**Lines:** 144–145
**Severity:** 🟢 LOW

```tsx
<DialogContainer open={open} onClose={onClose} title="Dashboard">
  <Card title="Dashboard">
```

The dashboard is wrapped in both a `DialogContainer` AND a `Card`, giving it two titles ("Dashboard" appears twice). The inner `<Card title="Dashboard">` is redundant since `DialogContainer` already shows the title.

**Fix:** Remove the inner `<Card title="Dashboard">` wrapper or change it to a plain `<Box>`.

---

## Summary

| Count | Severity | Category |
|---|---|---|
| 2 | 🔴 CRITICAL | Hardcoded API ports / dashboard broken |
| 2 | 🔴 CRITICAL | Banned model `gpt-4o-mini` references |
| 2 | 🟡 MEDIUM | Stale TODO comments (backend already wired) |
| 4 | 🟡 MEDIUM | Type mismatches with Go API |
| 2 | 🟡 MEDIUM | Hardcoded model fallback lists |
| 5 | 🟡 MEDIUM | Silent error handling |
| 4 | 🟢 LOW | Hardcoded strings / cosmetic |
| 1 | 🟢 LOW | Double-border in dashboard |

**Total: 22 issues found**
