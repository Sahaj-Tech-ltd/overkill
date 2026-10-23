# Overkill desktop

The desktop window uses the shared Go API/runtime and a private native PostgreSQL cluster. It does not require a manually started database, terminal UI, system Node, or Go at application runtime. Development builds currently target macOS arm64.

From the repository root, with Node 24 and Go 1.26 available:

```sh
npm ci --prefix desktop
node desktop/node_modules/electron/install.js
node scripts/prepare-desktop.mjs
npm start --prefix desktop
```

`prepare-desktop.mjs` verifies a pinned SHA-256 for the PostgreSQL 17.11 vendor artifact, checks the extracted executable version, and builds the current Go backend. It records a resource manifest under ignored `.cache/desktop-resources`. Electron is pinned in the npm lockfile and its installer verifies the vendor checksums. The PostgreSQL bundle is sourced from Zonky's Maven artifact; its complete redistribution notices/source obligations remain a distribution gate.

```sh
npm test --prefix desktop
OVERKILL_REQUIRE_DESKTOP_TEST=1 npm test --prefix desktop
OVERKILL_POSTGRES_BIN_DIR="$PWD/.cache/desktop-resources/postgres/bin" \
  OVERKILL_REQUIRE_LOCALDB_TEST=1 go test -race ./internal/localdb
```

The mandatory native test launches the real Electron window, encrypted provider setup, Go backend and private database. A controlled local provider drives actual tool execution, workspace file creation and durable history. It verifies cancellation of a real provider stream, clean database shutdown, relaunch with restored chat, and reconnect with preserved history after an owned-database crash. The database suite also tests exclusive ownership, paths with spaces, persistent data, crash recovery, cancellation and preservation of unmanaged/corrupt clusters.

Development accepts `OVERKILL_DESKTOP_DATA_DIR` for isolated application data and `OVERKILL_DESKTOP_RESOURCES` for an alternate resource directory. Installed applications ignore these overrides. Provider credentials remain encrypted on disk and outside renderer IPC; the child removes its credential environment variable after resolving the in-memory configuration and before launching subprocesses.

Both development and installed applications accept an explicit absolute profile directory, for example `"/path/to/Overkill Development.app/Contents/MacOS/Overkill" --overkill-profile="/absolute/path/App Data"`. Each profile keeps separate settings, chats, database and instance lock. Omitting the flag uses the normal application data directory; an invalid or duplicate profile flag fails startup.

Local development packaging uses the settled dependency inventory and notices:

```sh
node scripts/package-desktop-dev.mjs
node scripts/test-installed-desktop.mjs /absolute/path/to/Overkill-0.1.0-development-arm64.dmg
```

The packager copies the pinned Electron runtime, application source, native backend/database and notices into a real app bundle, signs local copies ad hoc, checks native loader dependencies/signatures and creates a compressed DMG under `.cache/distribution`. It does not publish artifacts. The installed test mounts the image read-only, copies the app into a temporary installation path containing spaces and drives the packaged window with isolated profile data. Same-version replacement tests data preservation; it does not establish cross-version database migration or minimum-OS compatibility. These development artifacts remain `distributionReady: false`, without Developer ID signing or notarization.

Release readiness is tracked in [DELIVERY_PLAN.md](../DELIVERY_PLAN.md). A current development DMG has passed bounded installed smoke testing; real Keychain acceptance for that exact new image remains pending. Remaining runtime/coordination acceptance, dependency/source obligations, broader installations and release signing still prevent a finished public distribution.

The Agents button opens workspace coordination. Register two chats in the same workspace with distinct names (for example `frontend` and `backend`) and roles. Select a peer and send a request; the existing background worker executes accepted requests when the recipient is available. Correspondence shows persisted accepted/started/completed/failed states and replies. `accepted` means queued, not completed. Registration is immutable for that chat; use a new chat for a different identity. API credentials remain outside the renderer. These local controls do not implement signed external webhook delivery.

Native resource checks and current packaging limits are recorded in [RESOURCE_INVENTORY.md](RESOURCE_INVENTORY.md).

Actual image/install results and the current Keychain authorization boundary are recorded in [INSTALLED_ACCEPTANCE.md](INSTALLED_ACCEPTANCE.md). Source acceptance now includes a responsive startup window while Keychain waits, bounded secret operations and replacement-key recovery.

## Workspace task inspection

Current source adds Tasks, Contracts and Checks tabs to the workspace rail. Peer messages show backend states (queued, working, blocked, completed, failed or cancelled), with an inspector for participant-filtered activity replay and shared task usage. Optional task names/token limits create real root-request budgets. Pending, missing or unconfirmed usage never appears as a complete zero-cost estimate. Limits gate subsequent calls and can be exceeded by requests already running.

Review/resend prefills a draft; it does not execute work automatically. It preserves the original conversation scope for unnamed requests and both token/cost limits for existing tasks. The reused scope remains visible and bound until the user explicitly starts a new request or changes the task name. A blocked recipient waits for review/cancellation; resending does not silently cancel or resume previous work. Task details use fresh server state and bounded cursor pages, with session/task generation guards against stale asynchronous results.

Contract inspection shows schema revisions, acceptance criteria, owned paths, artifact references, pinned dependencies and actual agreement blockers. Agreement readiness is not a passing integration test. Checks show recorded observations for the selected chat, including skipped/cancelled/unconfirmed outcomes and persistence errors. Heuristic confidence is labeled separately; full iterative self-evaluation remains unavailable.

The `workspace-native.test.cjs` scenario runs the production Electron renderer/preload/main IPC, current Go backend, private PostgreSQL and controlled provider. It verifies actual JSON write/check output, asynchronous peer work, budget stops, contract readiness, task replay, draft-edit races, preserved unnamed/cost budgets on resend, and wide/narrow layouts. Its disposable credential fixture avoids Keychain interaction and makes no encryption claim. The separate original native test still verifies actual encrypted setup, cancellation, relaunch and database recovery. The sequential combined source desktop suite passes **26 tests with zero skips** in `.cache/desktop-workspace-full-native-serial.log`.

These source panels are newer than the accepted XvnHeb development DMG. No installed-app acceptance transfers to a new package automatically. The original COORD-07 remains partial: terminal UI parity, complete presence/assignment, task graph/worktree integration, native notifications and review/resolve controls are not all implemented.

The desktop test command serializes test files because the native scenarios launch and close real macOS applications. A preceding parallel run lost its blocked-Keychain window early (25 passed, one failed; `.cache/desktop-workspace-full-native-final.log`); the isolated startup recheck passed (`.cache/desktop-startup-recheck.log`). The exact cause of that early closure is unconfirmed. Serialization changes scheduling, without skipping tests or relaxing assertions.
