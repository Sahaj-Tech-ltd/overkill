# Completion checks

The CLI provides `/audit start` before a coding task and `/audit run <JSON claims>` afterward. For example:

```text
/audit start
/audit run [{"description":"Validate incoming users","files":["src/users.ts"],"build_pass":true,"tests_pass":true}]
```

The application captures its own session-local baseline. Authenticated clients use `session.audit` with `session_id`, `action: "snapshot"`, then `action: "run"` and `claims`. A caller cannot submit another session's baseline. These operations acquire the normal session admission lock. Baselines are currently in-memory and must be captured again after runtime replacement or restart.

Snapshot hashing includes supported source/config files across languages. Discovery respects Git's tracked/unignored project inventory, so ignored downloaded harness sources and dependency caches are excluded. Checks select actual Go module build/tests, declared Node build/test scripts (or an offline TypeScript check when appropriate), and Python syntax plus declared pytest or unittest discovery. Every report records the actual command, directory, output and status. No declared script, missing runtime, empty unittest discovery or unsupported manifest is unavailable/unverified, never evidence of a passing check. Commands execute only when the user requests the run operation; project scripts can have their usual build/test side effects. Cancellation terminates their owned process group and descendants.

A separate verification worker receives bounded conversation history as unverified data. Its actual tool factory permits contained filesystem read/glob/stat, bounded text search, and fixed Git status/diff/log. Write attempts and ordinary delegated mutation tools are rejected. This semantic review is textual and model-assisted; it does not claim an LSP or terminal capability. Missing/malformed worker evidence or omitted claims cannot produce a passing report. It uses the configured provider, so an explicit audit run may incur model usage. There is no automatic audit of every completed chat.

Acceptance runs real temporary Go, TypeScript build-script and Python commands, catches an injected Python failure, exercises the production worker's reads/search/diff and denied writes, and checks the authenticated API's session/baseline isolation. The TypeScript fixture proves its declared transpile build, not a separate static type checker.
