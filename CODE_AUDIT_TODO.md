# Overkill code audit and repair checklist

Audit date: 2026-10-20. Baseline commit: `34b3fab`. Scope: repository source, runtime wiring, build/install paths, tests, documentation, and incomplete user-facing features. The findings below describe the original baseline; checked items and the repair evidence section record subsequent implementation.

Section-by-section static and local-test review is complete. This review used subsystem entry points, interfaces, runtime call-site tracing, focused implementation reads, full local Go tests, Python checks, and disposable probes. It is not a line-by-line certification or a claim that every defect has been found. A checked review section means its implementation and integration paths were inspected; it does **not** certify absence of defects. Unchecked repair items remain open.

Priorities: **P1** blocks core use, produces false success, loses data, or creates a material security risk; **P2** breaks a feature or makes operation unreliable; **P3** is maintainability/documentation debt. Evidence labels: **reproduced**, **source-confirmed**, **verification needed**. Source references are relative to this repository.

Checklist: **6 open items** (1 P1, 5 P2); **112 repaired items**. The complete checklist includes the requested frontend/backend coordination feature and integration-test gaps. Each item describes evidence, impact, and the required repair/acceptance check.

## 1. Build, install, packaging, and local startup

- [x] **BUILD-01 · P1 · reproduced — Repair the terminal UI build.** `tui/tsconfig.json:6` enables `allowImportingTsExtensions` while `npm run build` invokes emitting `tsc`. It fails with TS5096. Choose a supported emitting configuration/bundler or explicitly make this a typecheck-only project. Verify both `typecheck` and the documented build/start path in CI.
- [x] **BUILD-02 · P1 · source-confirmed — Repair Docker build inputs.** `Dockerfile:7,10,17` copies `prod/`, which is absent from this checkout. Use the actual source layout and a compatible Go toolchain. Verify a clean Docker build, rather than just native Go compilation.
- [x] **BUILD-03 · P1 · reproduced/source-confirmed — Supply a complete local database setup.** `cmd/overkill/tui.go:94` opens PostgreSQL before launching the UI; startup fails with connection refused on this Mac. `docker-compose.yml` has no PostgreSQL service, and install docs describe runtime dependencies as only Git. Add a documented database bootstrap, connection configuration, readiness checks, and persistent volumes. Verify first launch from an empty user configuration.
- [x] **BUILD-04 · P2 · source-confirmed — Include the actual frontend runtime in packaged installations.** The TUI launches `npm start` in the source tree (`cmd/overkill/tui.go:432`), while the Docker runtime installs neither Node nor the TUI tree. Define and test which distributions support interactive mode and ship their dependencies.
- [x] **BUILD-05 · P3 · source-confirmed — Synchronize supported Go versions and installation commands.** `README.md` advertises Go 1.23+ and a root-module `go install ...@main`; `go.mod` requires 1.26 and the executable is under `cmd/overkill`. CI starts at 1.24 and relies on implicit toolchain upgrades. Pin a compatible toolchain and publish executable package paths consistently.

## 2. CLI entry points and shared runtime wiring

- [x] **CLI-01 · P1 · source-confirmed — Preserve the real startup error and nonzero exit status.** `cmd/overkill/root.go:198–219` replaces every `runInkTUI` error with Node/repository installation advice and then returns nil. A database failure is misreported as a missing UI and appears successful to scripts. Return the wrapped cause and make fallback advice specific to the failure.
- [x] **CLI-02 · P2 · source-confirmed — Make API readiness deterministic.** `cmd/overkill/tui.go:400–428` polls readiness for only 500 ms, waits up to 2 s without further polling, and does not consume the server's error channel during readiness. Use a ready/error signal from the listener and test slow bind and bind failure.
- [x] **CLI-03 · P2 · source-confirmed — Implement or explicitly reject CLI ask-user interactions.** `cmd/overkill/run.go:313` wires an ask bridge that always cancels. Provide terminal input when interactive and a clear unsupported response for headless mode; verify workflows that require clarification.
- [x] **CLI-04 · P3 · source-confirmed — Extract common runtime assembly.** `cmd/overkill/run.go`, `tui.go`, and `serve_cmd.go` separately assemble tools, stores, providers, and optional services. The differences already leave features missing on some entry points. Create shared dependency assembly with explicit mode capabilities and integration checks for CLI/TUI/serve parity.
- [x] **CLI-05 · P2 · source-confirmed — Locate the installed frontend independently of the user's project.** `cmd/overkill/tui.go:findRepoRoot` accepts any ancestor containing `go.mod`, not specifically Overkill or a directory containing its TUI. Running Overkill inside another Go project selects that project's missing/unrelated `tui/` instead of the installation. Resolve bundled assets from the installation/configured path and verify launch from unrelated Go and non-Go workspaces.

## 3. JSON-RPC API, authentication, and streaming

- [x] **API-01 · P1 · source-confirmed — Share one server authentication token with the TUI.** `internal/api/middleware.go:withAPIAuth` requires a bearer token (and generates one when unset), but `tui/src/backend/client.ts` sends no Authorization header for RPC, health, or streaming. The launcher passes only URL/port. Generate/store one server token, pass it securely to the child, and authenticate all client requests. Verify an actual frontend-to-server call.
- [x] **API-02 · P1 · source-confirmed — Generate the fallback token once per server, not per route.** `withMiddleware` calls `withAPIAuth` separately for each endpoint; `withAPIAuth` creates a new random token every time and exposes none of them. With no environment token, routes have different inaccessible credentials. Verify a single credential works for RPC, health, and stream endpoints.
- [x] **API-03 · P1 · reproduced — Preserve streaming interfaces through middleware.** `statusWriter` in `internal/api/middleware.go` wraps `http.ResponseWriter` but does not implement `http.Flusher`; `handleSSE`/`handleStream` assert that interface. A real localhost server with a known bearer token returns 200 for health and 500 "streaming not supported" for both stream endpoints. Forward required interfaces or use `http.ResponseController`, and test the fully wrapped endpoints.
- [x] **API-04 · P2 · source-confirmed — Replace static evaluation/test status RPCs with real session state.** `internal/api/handlers.go:handleSelfEvalStatus` and `handleTestResults` always return idle/empty placeholders. Wire the active evaluation and verification events, include session selection, and verify state changes during a run.
- [x] **API-05 · P2 · source-confirmed — Scope queue inspection to the requested session.** `handleSequentialQueue` ignores params and returns the first nonempty queue from the agent map. Multiple sessions can display another session's queue nondeterministically. Parse session ID and test two concurrent sessions.

## 4. Sub-agent execution and routing

- [x] **SUB-01 · P1 · source-confirmed — Remove fabricated production success on missing credentials.** `internal/subagent/manager.go:Spawn`/`SpawnBatch` fall back to `NewWorker` when `NewRealWorker` fails. `worker.go:Run` waits 10 ms and returns completed with invented tokens/cost and "Processed: ..." without doing work. Restrict simulation to explicitly injected tests; return an actionable unavailable/error result in production. Verify missing and unsupported provider credentials.
- [x] **SUB-02 · P1 · source-confirmed — Implement the promised tool-capable child agent or constrain the feature honestly.** `internal/subagent/realworker.go:Run` makes one completion, supplies no tools, and tells the model it cannot modify files. `MaxSteps` is unused. Coding/decomposition tasks can be marked completed without reading or changing the project. Wire a bounded agent/tool loop and verify file-based task completion with receipts.
- [x] **SUB-03 · P2 · source-confirmed — Resolve sub-agent providers through configured providers.** `realworker.go:resolveProviderCreds` supports only Xiaomi and DeepSeek while the manager accepts router-selected providers/models. Unsupported choices trigger SUB-01. Reuse the parent's provider factory/configuration, support custom URLs/auth, and fail explicitly when a route cannot execute.

## 5. Filesystem and macOS compatibility

- [x] **FS-01 · P1 · reproduced — Canonicalize both workspace root and target before symlink containment checks.** `internal/tools/fs.go:61–95` resolves the target but compares it against an unresolved root. Ordinary reads/writes under macOS `/var/...` or `/tmp/...` aliases are rejected as outside the workspace. Failing filesystem tests pass with `TMPDIR=/private/tmp/ok-tests`. Preserve traversal protections and cover symlinked roots plus out-of-root child links.
- [x] **MAC-01 · P2 · reproduced — Keep Unix socket test paths within macOS limits.** Daemon and CLI RPC tests fail to bind sockets under the default long macOS temp root; they pass under a short temp directory. Use a short socket-specific directory and explicitly report path-length failures. Verify macOS test execution without special environment preparation.

## 6. Terminal UI tasks and skills

- [x] **TUI-01 · P2 · source-confirmed — Use the active session in the todo panel.** `tui/src/components/sidebar/todo-panel.tsx` hardcodes `session_id: ""` for list/add and loads only once. The backend filters tasks by session, so real session tasks are absent and new tasks get an empty session. Pass the current session ID and reload on switch.
- [x] **TUI-02 · P2 · source-confirmed — Surface failures instead of showing false UI success.** `skills-panel.tsx:toggleSkill` flips local state after RPC failure; `todo-panel.tsx:clearAll` clears local tasks before deletions succeed and suppresses errors. Retain/restore authoritative state and show retryable errors. Verify failed RPCs do not display successful changes.
- [x] **TUI-03 · P2 · source-confirmed — Add a real task-entry/edit flow.** Todo keyboard shortcut `a` submits the literal "New task (edit me)"; the panel has no corresponding edit control. Collect a description and support editing or remove the misleading shortcut.
- [x] **SKILL-01 · P2 · source-confirmed — Make skill toggles control the executing registry and persist.** `internal/api/skills_handlers.go` only changes a private in-memory disabled map, ignores the supplied `enabled` value, and does not update skill enablement used by agents. Wire the shared registry/settings, validate names, and test persistence and prompt exclusion.

## 7. Browser UI and unfinished surfaces

- [x] **WEB-01 · P2 · source-confirmed — Complete flow storage/routes before exposing the feature.** `internal/web/server.go` leaves `flowRoutes` unregistered; `internal/web/flows.go` handlers return 501. Implement persisted list/create/get/actions and end-to-end tests, or explicitly document this feature as unavailable.
- [x] **WEB-02 · P2 · source-confirmed — Implement canvas snapshots and strict event validation.** `internal/web/canvas.go:handleCanvasSnapshot` returns HTTP 200 with `not_implemented`; push accepts multiple valid top-level keys although its contract requires exactly one and chooses one by map iteration. Return an honest unsupported status until rendering exists; reject ambiguous events and verify snapshot rendering.

## 8. Configuration, security, and extension wiring

- [x] **CFG-01 · P1 · source-confirmed — Reject invalid configuration instead of silently discarding it.** `internal/config/load.go:Load` logs TOML errors and returns defaults with a nil error. A typo can remove configured deny rules/providers while startup proceeds. Return parse errors and preserve the file; test malformed security settings.
- [x] **CFG-02 · P1 · source-confirmed — Make `sandbox_enabled` enforce an actual sandbox.** Production references to `Security.SandboxEnabled` are defaults, warnings, and API configuration serialization; no execution path consumes it. The UI can report sandboxing enabled while tools still execute ordinary processes. Implement an execution boundary, or reject/label the setting as unavailable rather than implying isolation.
- [x] **EXT-01 · P2 · source-confirmed — Wire filesystem hooks into the live agent.** `internal/hooks/file_loader.go:LoadFromDir` has no production call site. Agents create empty hook registries; executable user hooks under the documented directory never load. Load trusted hooks in shared runtime assembly and test before/after events with a real script.
- [x] **EXT-02 · P2 · source-confirmed — Attach MCP capability policy to production managers.** `internal/mcp/manager.go:SetPolicy` has no production caller; nil policy retains the unrestricted path. Build policy from declared capabilities, attach it before tool exposure, and verify prohibited calls fail in a running agent.
- [x] **EXT-03 · P2 · source-confirmed — Complete the unified extensions registry.** Only the skills backend is registered by root startup. Plugin/hook/MCP backends in `internal/extensions/stub_backends.go` are empty/unsupported. Connect real inventories and supported enable/disable operations, and clearly report unsupported capabilities.
- [x] **EXT-04 · P2 · source-confirmed — Propagate MCP execution failures as failures.** `internal/mcp/tool.go:Execute` converts a failed manager call into a JSON `error` object with nil Go error; remote `isError` is also returned as normal output. Normalize tool error semantics so receipts, retries, and verification cannot count failed MCP calls as successful.
- [x] **CFG-03 · P2 · source-confirmed — Implement advertised rewriter toggles and browser timeout.** `cmd/overkill/run.go:261,656–662` explicitly leaves `Browser.Timeout`, `Rewriter.StripSycophancy`, `AntiPatternGuard`, and `LLMRewrite` disconnected or unconditional. Respect each setting, or remove unavailable settings from user controls; verify toggles change executed behavior and API-call count.
- [x] **CFG-04 · P3 · source-confirmed — Reconcile live-reload promises with implemented changes.** `internal/hotreload/agent_wiring.go` says scanner slices are rebuilt, but `apply` only reports scanner changes; its interface exposes only model setters. Mark restart-required fields accurately and test reload effects rather than just notifications.

## 9. Sessions, memory, synchronization, and journal

- [x] **STORE-01 · P1 · source-confirmed; database reproduction pending — Return the database's exact version timestamp after session saves.** `internal/session/postgres.go:Save` updates with `updated_at = NOW()` and guards the next write by exact timestamp equality, but sets the caller's timestamp to a separate `time.Now().UTC()`. A second save of that object can fail its optimistic-lock check without a competing writer. `Create` also retains pre-insert nanosecond timestamps despite database precision. Use `RETURNING updated_at` (or a dedicated version column) for both paths; verify create→save→save and genuine concurrent conflicts against PostgreSQL.
- [x] **STORE-02 · P1 · source-confirmed — Fail startup if session schema migration fails.** `session.NewPostgresStore` logs migration errors but returns a store whose operations may all fail. Propagate initialization errors through entry points and readiness; verify denied DDL and incompatible existing schemas do not produce a healthy server.
- [x] **MEM-01 · P2 · source-confirmed — Wire automatic memory extraction to an extraction implementation.** CLI creates `automemory.NewExtractor` but never supplies `ExtractFn`; `ShouldExtract` always returns false when it is nil. No production assignment to `ExtractFn` exists. Also pass the correct root: the constructor expects the user home, while the caller passes the Overkill config directory, producing a nested `.overkill/.overkill/memory` path if enabled. Verify actual post-turn extraction and reload.
- [x] **SYNC-01 · P2 · source-confirmed — Pass a live sync manager in CLI auto-push.** `cmd/overkill/run.go:838` passes nil to `AutoPushIfEnabled`, whose first guard returns immediately. Auto-push can be enabled in config and silently do nothing. Construct/reuse the backend/store manager and verify a completed turn appears remotely.
- [x] **SYNC-02 · P2 · source-confirmed — Honor cancellation and errors in Git sync.** `internal/sync/git.go:runGit` uses `exec.Command` without the context accepted by backend methods, so network Git operations can outlive configured timeouts. `Delete` ignores filesystem, commit, and push errors and returns nil. Use bounded context-aware commands and propagate failures; test rejected pushes and stalled remotes.
- [x] **SYNC-03 · P2 · source-confirmed — Avoid conflict copies for unchanged sessions.** `internal/sync/manager.go:Resolve` always returns a loser copy whenever local and remote exist, including identical revisions; IDs use whole-second suffixes. Repeated pulls can create unnecessary conflicts or collide. Compare revisions/content before copying and generate collision-resistant IDs; verify repeated unchanged pulls are idempotent.
- [x] **JOURNAL-01 · P2 · source-confirmed — Close the replay error channel on every completion path.** `internal/journal/replay.go:Replay` closes only the event channel. A caller ranging or waiting on `errCh` after successful replay can block indefinitely. Defer closing both channels and verify empty, successful, cancelled, and read-error replays.

## 10. Goals, checkpoints, worktrees, and execution reliability

- [x] **GOAL-01 · P1 · source-confirmed — Connect goal accounting to actual turns.** `NewGoalAccountingState`, `RecordTurnStart`, and `RecordTurnEnd` have no production call sites; `UpdateUsageAtomic` is reached only from that unused helper. Token/time budgets therefore have no demonstrated automatic usage enforcement through this path. Wire actual provider/tool usage to per-goal counters and enforce limits in both Run and Stream; test multiple turns and resumed goals with a real database.
- [x] **CHECKPOINT-01 · P1 · source-confirmed — Roll back to checkpoint hashes rather than arbitrary ancestors.** `internal/agent/checkpoint.go:ListSnapshots` filters checkpoint commits, but `Rollback(n)` resets to `HEAD~n`, counting all commits. Intervening normal commits make the selected checkpoint and actual reset differ; rollback zero also reports one checkpoint while resetting current HEAD. Resolve a validated listed snapshot and preserve recoverable user changes before destructive reset. Verify mixed normal/checkpoint histories in a disposable repository.
- [x] **WORKTREE-01 · P2 · reproduced — Canonicalize worktree discovery paths.** `TestManager_ReclaimDiscoversExistingTrees` fails under macOS temp aliases and passes with a fully canonical temp root; `internal/worktree/manager.go:Reclaim` compares Git's path strings to an unresolved lexical prefix. Normalize both roots and discovered worktrees before matching; verify reclamation through symlinked paths.
- [x] **SHELL-01 · P2 · source-confirmed — Bound captured shell output and terminate process trees.** `internal/tools/shell.go` stores stdout/stderr in an unbounded `bytes.Buffer`; `exec.CommandContext` kills the shell but does not establish/cancel its process group or set a pipe-drain deadline. Large output can exhaust memory and descendants can survive cancellation or hold pipes open. Add disk-backed/capped output and platform-specific tree cancellation; verify a noisy process and a spawned child under a short timeout.
- [x] **BROWSER-01 · P1 · source-confirmed; adversarial runtime verification pending — Enforce dev-browser network restrictions at connection/request time.** `internal/browser/devbrowser/safety.go` validates the initial URL with a separate DNS lookup, explicitly permits Chrome to resolve again, and excludes redirects from its guard. `devbrowser.go` only calls this initial validator. A public redirect, page subresource, or rebinding can reach private addresses despite the "AI-safe" network claims. Apply checks to every outbound request/redirect and pin validated destinations; verify with controlled public-to-private redirects and rebinding fixtures.
- [x] **BROWSER-02 · P2 · source-confirmed — Make the default headless option match actual browser flags.** `internal/browser/browser.go` documents empty Options as headless, but zero-value `Headless == false` makes `Open` explicitly pass `headless=false`. `Manager.Get` describes a default conversion but never performs it. Use an unambiguous tri-state/default or a visible-mode option and verify the launch flags for default/headless/visible configurations.

## 11. Messaging gateways and bridge adapters

- [x] **GW-01 · P1 · source-confirmed — Initialize the API-started bridge's session router.** `internal/api/server.go:startBridge` passes `&gateway.SessionRouter{}` instead of its constructor. The first new inbound chat reaches `Bind`, which writes to a nil map. Since bridge dispatch runs in a detached goroutine, this can crash the process. Use `NewSessionRouter`, configure persistence, and integration-test first inbound message on this entry point.
- [x] **GW-02 · P1 · source-confirmed — Implement cancellation and supported commands in the API bridge adapter.** `bridgeAgent` in `internal/api/server.go` has empty `EStop`/`Interrupt` and goal methods, returns success-like empty responses for undo/fork/rollback, and always reports idle. Wire real per-session operations or return explicit unsupported errors; verify stop actually interrupts an active tool/model call.
- [x] **GW-03 · P2 · source-confirmed — Use a server-scoped lifecycle and bounded admission for inbound sidecar work.** `internal/gateway/bridge/bridge.go:handleIn` starts a goroutine with `context.WithoutCancel(r.Context())` and has no enclosing server cancellation/deadline. Accepted messages can outlive shutdown and accumulate. Use the bridge's lifecycle context, bounded queue/concurrency, and panic recovery; verify cancellation of accepted work on shutdown.
- [x] **GW-04 · P2 · reproduced — Define stable recency when routing timestamps tie.** `internal/gateway/router.go:Recent` sorts only by wall-clock timestamp after iterating a map. `TestSessionRouter_RecentSorted` fails on this Mac because rapid binds can share timestamps. Add a deterministic sequence/tiebreaker consistent with the product's ordering contract and a test with equal timestamps.

## 12. Python bridge, embeddings, reranking, and distribution

- [x] **PY-01 · P1 · source-confirmed — Declare all runtime dependencies.** `bridge/server.py` imports `grpc_health`; the default Postgres backend imports `psycopg2` and `pgvector`. None is declared in `bridge/pyproject.toml`. A clean installation cannot start the server or use its default persistent backend. Add compatible dependencies/extras and test isolated installation/startup.
- [x] **PY-02 · P1 · source-confirmed — Ship an executable server and its generated top-level modules in the wheel.** `pyproject.toml` sets `py-modules = []`, enumerates only subpackages, and provides no console entry point. `server.py`, `overkill_pb2.py`, and `overkill_pb2_grpc.py` at the root are omitted even though the server imports them. The wheel builds successfully, which does not establish it can run. Package/import generated files consistently and test launching from outside the checkout.
- [x] **PY-03 · P1 · source-confirmed — Default the unauthenticated gRPC server to loopback.** `bridge/server.py:serve` binds insecure gRPC to `[::]:50051` with no authentication. Exposed methods read/write/delete vector memory and can trigger model loading. Bind to loopback by default; require explicit authenticated/TLS configuration for remote access. Verify listener scope and unauthorized calls.
- [x] **PY-04 · P2 · source-confirmed — Publish models only after initialization succeeds and coordinate model switching with inference.** Embedding/reranking `_ensure_model` sets initialized/name before constructing the new model; failure leaves that name marked loaded and may reuse an older model. Concurrent requests can bypass the lock or switch `self._model` between ensure and encode/predict. Use immutable per-model instances or hold an appropriate lock through selection/inference, and test load failure and overlapping model requests.
- [x] **PY-05 · P2 · source-confirmed — Make mock semantic behavior an explicit mode.** Missing sentence-transformers silently changes embeddings to pseudorandom hash vectors and reranking to word overlap while gRPC reports success/healthy. Expose degraded capability status and require explicit opt-in to mocks; verify production embedding calls fail clearly when the model is unavailable.
- [x] **PY-06 · P2 · source-confirmed — Validate vectors and bound query parameters.** `bridge/memory/service.py` accepts arbitrary dimensions/non-finite values and uses `results[:top_k]`; a negative `top_k` returns almost the whole result set rather than rejecting the query. Enforce dimensions, finite numbers, sensible top-k limits, and backend parity; test invalid inputs through gRPC.

## 13. Releases, CI, and tests

- [x] **INSTALL-01 · P1 · source-confirmed — Align downloaded asset names and checksum format.** `install.sh` requests `overkill-<os>-x86_64` on x86 hosts and a sibling `<asset>.sha256`; the release workflow creates `...-amd64` and one `checksums.txt`. x86 downloads fail, while working names can bypass verification because absent checksums are treated as optional. `install.ps1` also expects an absent sibling `.sha256`. Use identical names/formats and require verified checksums; add fixtures for every release target.
- [x] **INSTALL-02 · P1 · source-confirmed — Generate a config matching the current schema.** `install.sh:step_bootstrap_home` and `install.ps1` write `[providers.anthropic]`/`[providers.openai]` tables, but `Config.Providers` is a slice requiring `[[providers]]` entries. `config.Load` currently masks this with CFG-01. Generate defaults via the application schema and validate installer-created files in tests.
- [x] **RELEASE-01 · P1 · source-confirmed — Repair release publication inputs.** `.github/workflows/release.yml:publish` never checks out the repository, yet invokes `gh release create` without `--repo` and with `--notes-file CHANGELOG.md`; that file is absent from the repository. Add checkout/explicit repository and an existing or generated notes file, then dry-run the complete publication inputs.
- [x] **CI-01 · P1 · reproduced — Fix the current vet failure.** `go vet ./...` reports `cmd/overkill/setup.go:218: self-assignment of model`. Remove the dead assignment and verify the advertised build-correctness gate passes.
- [x] **CI-02 · P1 · source-confirmed — Exercise production API middleware and UI integration in CI.** Passing handler tests missed API-01/02/03. Run authenticated HTTP RPC/stream tests through the actual middleware and add a frontend smoke test with the real backend. Typecheck alone does not cover auth, routing, or streaming.
- [x] **CI-03 · P1 · source-confirmed — Provide PostgreSQL in required integration tests.** `internal/testpg` skips on missing/unreachable databases; workflows supply no PostgreSQL service. Core session/goal/cost/memory SQL can remain untested on a green run. Add a required integration job that fails if its database is unavailable, with pgvector for Python vector tests where needed.
- [x] **CI-04 · P2 · reproduced — Reconcile model catalog expectations with implementation.** `TestModels_OpenAI` expects `o1`, `o1-mini`, `o3-mini`; `TestModels_Anthropic` expects removed older names. Both fail against current built-in lists. Establish the supported catalog contract and update implementation/tests together; avoid asserting stale incidental model names.
- [x] **CI-05 · P2 · source-confirmed — Make race and frontend failures visible release gates.** `tests.yml` marks race coverage continue-on-error and additionally swallows the command's error with a blanket "flaky" message; workflows have no terminal UI install/typecheck/build job. Classify real race failures, preserve failing status for supported tests, and gate frontend correctness.

- [x] **BUILD-06 · P1 · reproduced — Restore the advertised Windows build.** A `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ...` fails in `internal/hooks/file_loader.go` (`Setpgid`, `Kill`, `Stat_t`) and `internal/journal/recorder.go` (`Flock`, `LOCK_EX`, `LOCK_UN`). Split Unix implementations behind build tags and provide Windows equivalents. Add Windows to the required cross-build gate; it is published by the release matrix but absent from the CI cross-build matrix.
- [x] **CI-06 · P2 · reproduced — Remove real-clock assumptions from pipeline tests.** `TestExecutor_Run_StageDuration` intermittently observes zero for instantaneous mock calls; the failure recurs in a 10-run targeted check. Inject time/delay where timing is the behavior being tested, rather than requiring the host clock to advance for every mock completion.
- [x] **CI-07 · P1 · reproduced — Repair invalid workflow YAML before relying on CI gates.** Strict YAML decoding rejects repeated `defaults` mapping keys in `.github/workflows/lint.yml:22–26` and `security.yml:22–24`. Remove duplicate keys and validate both YAML syntax and GitHub Actions schema. The remaining empty `defaults.run` mappings also need schema validation; parsing them is not proof that Actions accepts them.
- [x] **HOOK-01 · P2 · source-confirmed/reproduced — Make local hook dependencies and failure reporting work in a clean environment.** `scripts/pre-commit.sh` invokes pytest with `--timeout=30`, but the dev dependencies do not include pytest-timeout and the audited test environment exposes no such option. Both hook scripts increment `((failures++))` under `set -e`, causing immediate exit on the first increment instead of the promised aggregate report; pre-push also assumes GNU `timeout` on macOS. Declare/probe dependencies, use safe arithmetic, and test success/multiple-failure paths with command fixtures.
- [x] **RELEASE-02 · P2 · source-confirmed — Keep release dry-run non-mutating.** `scripts/release.sh --dry-run` still unconditionally removes/recreates `dist/` and truncates its checksum file before skipping builds. Guard all mutations and verify existing artifacts remain byte-identical after dry-run.

## 14. Additional filesystem and verification failures

- [x] **FS-02 · P1 · reproduced — Reject writes through symlinked parents when the target does not exist.** `fs.resolve` skips containment when `EvalSymlinks(fullTarget)` fails. In a disposable fixture, `workspace/linked -> outside` followed by `fs.write("linked/new.txt")` creates `outside/new.txt` with nil error. Resolve/check the deepest existing parent before creation; use a shared hardened path primitive and defend the open itself against symlink races.
- [x] **FS-03 · P1 · reproduced — Apply symlink containment to patch operations.** `internal/tools/patch.go:Execute` checks only the lexical relative path. Patching `workspace/linked/patch.txt` in a fixture modifies a file in `outside/` with nil error. Reuse a canonical containment/open strategy for every file tool and test both parent links and direct file links.
- [x] **VERIFY-01 · P1 · reproduced/source-confirmed — Recognize the real `fs` tool's write actions and cover both agent execution paths.** `internal/verify/default.go:WriteToolNames` excludes `fs`, the name returned by `FSTool.Name()`. The runtime probe confirms `IsWriteTool("fs") == false`. Post-write checks in `stream.go` therefore skip normal filesystem writes/edits, and `react.go` has no equivalent verifier dispatch. Detect writes using name plus action and apply the same checks in Run and Stream; verify a deliberately broken Go/JSON edit is caught on both paths.
- [x] **FS-04 · P2 · source-confirmed — Make large-file/ranged reads bounded in memory and preserve the original descriptor.** `largeFileReference` rereads the entire file with `os.ReadFile` and splits all lines, despite the inline-output size guard; ranged reads also use `io.ReadAll`. The reopen also invalidates the comment claiming a single pinned descriptor. Read only necessary ranges/head/tail from one open handle and verify memory remains bounded for large files.

## 15. Providers, routing, tokens, and cost accounting

- [x] **LLM-01 · P1 · source-confirmed — Separate context-window capacity from maximum generated tokens.** `cmd/overkill/run.go` sets `agent.Config.MaxTokens` to 200000; `Agent.New` uses it for context budgeting, and `buildRequest` forwards the same number as output `Request.MaxTokens`. The model catalog already has distinct context/output limits. Resolve both per selected model, honor configured output limits, and test request payloads for models with different limits.
- [x] **LLM-02 · P2 · source-confirmed — Enforce required capabilities on every router fallback.** `internal/routing/router_impl.go:Route` falls back to `defaultResult` when capability filtering yields no candidates; that result does not validate vision/tools or even ensure a configured provider exists. Reject incompatible defaults or return an explicit unsupported-capability result. Verify an image/tool request cannot select a text-only or unavailable fallback.
- [x] **LLM-03 · P2 · source-confirmed — Make routing cost controls and estimates reflect their inputs.** `SmartRouter.SetCostPriority` writes a field never read by routing; `RouteRequest.EstimatedTokens` is ignored and `CostEstimate` is just input-plus-output per-million pricing. Implement the toggle and estimate with request/output assumptions, or rename/remove misleading controls and units.
- [x] **LLM-04 · P2 · source-confirmed — Apply failover cooldown on actual failures.** `FailoverChain.Complete`/`Stream` reset cooldown on success but never call `MarkFailed` on failure; no production caller of `MarkFailed` exists. Failed providers are retried first on every call. Classify failures, record cooldowns, and test consecutive calls, cancellation, and recovery.
- [ ] **LLM-05 · P2 · live-provider verification remains — Complete OpenAI-compatible usage reporting.** Current code requests streamed usage and parses cached-token details, preserving omitted versus reported zero, compatible aliases and failure-path usage; controlled HTTP fixtures pass. Original provider smoke acceptance remains unexecuted. Verify real configured streamed/non-streamed usage and cost without treating local protocol fixtures or unknown pricing as upstream billing proof.

## 16. Diagnostics, quality gates, and auxiliary systems

- [x] **DOCTOR-01 · P1 · source-confirmed — Keep API keys out of probe URLs and diagnostic output.** `internal/doctor/checks/providers.go:RegisterProviders` appends every nonempty auth prefix plus key to the URL, including the `"Bearer "` prefix intended only for headers. This both corrupts OpenAI/custom model-list paths and includes the key in success/failure messages and request errors. Separate query/header auth handling and redact secrets before formatting diagnostics; test with a fake key and captured HTTP requests/logs.
- [x] **QUALITY-01 · P2 · source-confirmed — Stop presenting a test/source line ratio as measured test coverage.** `internal/walls/testquality.go:AnalyzeTestQuality` sets `Coverage = testLines / (testLines + codeLines)` and the wall compares it to configured coverage requirements. Long ineffective tests can pass without executing the implementation. Use coverage-tool data or label this as a structural heuristic, and require behavioral evidence independently.
- [x] **AUDIT-01 · P2 · source-confirmed — Make completion audits language-aware and verify actual child capabilities.** `internal/audit/auditor.go` snapshots only `.go` files and unconditionally runs Go build/tests; it cannot establish completion for this repository's TypeScript/Python sections. `subagent_runner.go` claims LSP/grep/Git tool verification, but the selected real worker supplies no tools (SUB-02). Select relevant language checks and verify the auditor's real execution path, not just its declared tool list.
- [x] **EVENT-01 · P2 · source-confirmed — Count panicking event sinks as failures.** `internal/events/completion.go:Emitter.Emit` recovers a sink panic before its result assignment, leaving a zero result with nil error; the collector then returns success. Record a structured error in recovery and test an all-panicking sink set versus mixed healthy/failing sinks.
- [x] **TERM-01 · P2 · source-confirmed — Cancel or isolate the terminal background probe's stdin reader.** `internal/term/osc11.go:QueryBackground` returns after timeout but leaves a goroutine blocked in a buffered reader on the shared stdin. If the terminal never replies, it can consume later UI keystrokes after raw mode is restored. Use a cancellable bounded terminal read and ensure the probe has released stdin before Ink starts; verify a no-reply terminal with subsequent input.
- [x] **AUTOMATION-01 · P1 · source-confirmed — Do not acknowledge durable SOP transitions when persistence fails.** `internal/automation/sop.go` ignores `SaveSOP` errors when moving steps into waiting/done/failed and when updating workflow state. A restart can rerun a completed side effect or lose a pending approval. Propagate persistence failures or expose a durable degraded state with safe retries; test store failure before and after a non-idempotent step.
- [x] **MODULE-01 · P2 · source-confirmed — Bound total archive expansion and reject truncated module files.** `internal/modules/github.go:extractTarGz` limits compressed input and each copied file but imposes no aggregate expanded-byte/file-count limit. `io.LimitReader` truncates an oversized entry without reporting oversize. Reject invalid/oversized archives, check mkdir/close errors, and stage/validate extraction before replacing installed modules.
- [x] **PERSONA-01 · P2 · source-confirmed — Honor the configured personality levels in the actual prompt.** `cmd/overkill/run.go:buildSystemPrompt` explicitly leaves subtle/witty/full unimplemented; only disabling personality is respected in that path. Connect levels to the real prompt/style behavior consistently across entry points, and test behavior rather than just enum parsing.

## 17. Remaining UI contracts and advertised features

- [x] **TUI-04 · P2 · source-confirmed — Align dashboard RPC names and session arguments with the server.** `tui/src/components/dashboard/api.ts` calls `goal.get` and `plan.get`; `tui/src/app.tsx` calls `health.check`. The server dispatches `status.health` and exposes goal/plan via HTTP endpoints rather than those RPC methods. Correct the transport/methods and selected-session IDs, and test nonempty dashboard data from the real server.
- [x] **TUI-05 · P2 · source-confirmed — Continue connection monitoring after initial success.** `use-backend.ts:scheduleNext` reschedules only inside its disconnected branch. Once a timer finds the client connected, it stops checking forever; later backend death never updates status. `BackendClient.health` also has no explicit deadline. Maintain bounded periodic health/reconnection checks with cleanup and test disconnect→restart.
- [x] **SKILL-02 · P2 · source-confirmed — Supply the runtime skill registry to API-backed UIs.** `cmd/overkill/tui.go` and `serve_cmd.go` omit `ServerConfig.SkillsRegistry`; `skillsStore.list` returns no skills for a nil registry. The extension manager's separately loaded skills do not populate this field. Share one executing registry and verify bundled/user skills appear and toggle correctly.
- [x] **DOC-01 · P2 · source-confirmed — Publish a capability matrix matching implemented gateways.** README claims ten gateways including Feishu, iMessage, and SMS. There is no Feishu adapter/configuration in `cmd` or `internal/gateway`; SMS/iMessage are generic bridge integration points requiring external sidecars, rather than provided native adapters. Implement the missing adapter/sidecars or state setup and support levels accurately.
- [x] **DOC-02 · P3 · source-confirmed — Correct storage and feature descriptions after the backend migration.** `docs/src/architecture.md` says all state is PostgreSQL and describes it under a filesystem sessions directory, but plans/tasks/tags/playbooks/relationship data remain file-backed and Python can default to in-memory vectors. Document which data persists where, what requires services, and which experimental surfaces are unfinished.

## 18. Runtime integration gaps found by tracing call sites

- [x] **CORE-01 · P1 · source-confirmed — Enable context budgeting for API/TUI-created agents.** `internal/api/handlers.go:createAgent` omits `agent.Config.Tokenizer` and `MaxTokens`. `Agent.New` only creates its budget estimator when a tokenizer exists; `checkBudget` exits when it is nil. Merely attaching a compactor later does not enable automatic budget checks. Resolve the model context limit and wire estimator/compaction consistently; verify long TUI conversations compact before exceeding context limits.
- [x] **CORE-02 · P1 · source-confirmed — Bind tools/stores to the executing session.** CLI/TUI construct `plan.NewStore(..., "")`; both goal queriers capture a function permanently returning empty ID, and TUI checkpoint/task/alarm session callbacks also always return empty IDs. Multiple real sessions can share an empty-session plan/goal or lose associations. Bind dependencies per agent/session rather than sharing fixed closures; verify isolation across two concurrent sessions and a restart.
- [x] **VERIFY-02 · P1 · source-confirmed — Attach the post-write verifier at startup.** `newVerifierAdapter` and `Agent.SetPostWriteVerifier` have no production call sites. Even the tools that VERIFY-01 correctly recognizes receive no automatic verification in normal entry points. Attach a real verifier in shared agent creation and prove the configured disable switch is the only intentional opt-out.
- [x] **EXT-05 · P2 · source-confirmed — Start and stop configured language servers.** CLI/TUI construct `lsp.NewManager` and register adapters but never call its `Start`; the constructor does not launch clients. Configured LSP tools therefore find no connected language server. Start with a lifecycle-bound context, report readiness/failure, and stop on exit; verify a real initialize/hover exchange using a controlled server fixture.
- [x] **EXT-06 · P2 · source-confirmed — Run the hot-reload bus and clean up its subscriptions.** Root startup constructs `hotreload.New`; no production caller invokes `Bus.Run`. Agent wiring subscribes with `context.Background()` and discards unsubscribe callbacks. Start the watcher, bind subscriptions to session lifetimes, and test an edited config actually changes a running session without leaking subscribers.
- [x] **EXT-07 · P2 · source-confirmed — Register MCP tools after asynchronous discovery completes.** `api.NewServer` calls manager `Start` (which launches supervisors and returns immediately), then calls `RescanTools` only once; this is the only production rescan call. Servers that initialize after that scan never enter the tool registry. Reconcile tool inventory on readiness/reconnect and test a delayed MCP server.
- [x] **SKILL-03 · P2 · source-confirmed — Attach loaded skill instructions to real agents.** `Agent.SetSkillRegistry` has no production caller. Root loads skills for extension metadata, but that does not connect the instruction registry consumed by `renderSkillSection`. Wire the shared registry into CLI/API/gateway agent construction and verify the selected skill body actually reaches a provider request.
- [x] **GOAL-02 · P2 · source-confirmed — Wire goals into primary CLI/API agents as well as standalone bots.** Production calls to `Agent.SetGoalStore` exist in standalone Discord/Matrix/Telegram/Signal/Mattermost commands, but not in primary `run.go` or API `createAgent`. Goal-aware prompt injection and agent goal methods therefore differ by entry point. Share the same per-session goal store used by typed goal tools and verify cross-entry-point continuity.

## 19. Integration verification work still required

These are explicit test gaps, not claims that each integration is broken.

- [x] **VALIDATE-01 · P1 · verification needed — Run PostgreSQL-backed integration tests and migrations.** Cover fresh/old schemas, session create→save→save, branch/merge/retention, goal budgets, costs, learning, cron/jobs, routing bindings, memory, and migration failure. Require actual executed assertions rather than skipped database tests.
- [x] **VALIDATE-02 · P1 · verification needed — Run a complete interactive coding task.** After startup/auth/stream repairs, verify onboarding, provider selection, chat streaming, a bounded shell command, a file edit, verification feedback, persistence/restart, session switching, clarification, cancellation, and emergency stop. Record actual artifacts and usage.
- [ ] **VALIDATE-03 · P2 · verification needed — Smoke-test real configured provider and optional service adapters.** Use controlled credentials to verify supported provider protocols (including OAuth/Bedrock/local models), cost/usage, Chrome navigation and shutdown, MCP/LSP reconnect, webhook delivery, TTS/image generation, and sync backends. Current local unit tests cannot prove upstream service compatibility.
- [ ] **VALIDATE-04 · P2 · verification needed — Exercise each messaging channel with a test account.** Cover Telegram, Discord, Slack, Signal, Matrix, Mattermost, both WhatsApp implementations, and bridge sidecars: allowlists/pairing, signed webhooks where relevant, attachments, chunked edits, duplicate inbound events, stop/retry, per-chat isolation, reconnection, and completion notifications. No live messages were sent during this audit.
- [ ] **VALIDATE-05 · P2 · verification needed — Test real semantic models and persistent vectors.** Verify embeddings/reranking on CPU/MPS, concurrent model changes, dimension handling, pgvector setup/retrieval/deletion, durable restart, and capability/health reporting. Python tests here used the lightweight environment without sentence-transformers or a database.
- [ ] **VALIDATE-06 · P2 · verification needed — Validate supported release installations in clean environments.** Test source and prebuilt installs on macOS Intel/ARM, Linux amd64/ARM, Windows, and WSL; exercise Node/frontend lookup from an unrelated working directory, database instructions, upgrade, checksum failure, and uninstall/recovery behavior. Actual Linux/arm64 container and native macOS arm64 acceptance now pass; Windows source compilation is also checked. Native macOS Intel, Linux amd64, Windows/WSL and minimum-OS/cross-version behavior still require their own executed evidence.

## 20. Requested feature: frontend/backend agents coordinating through messages

User request: a frontend agent should keep working, ask a backend agent for status/API details, receive updates or answers, and coordinate handoffs. This feature is explicitly wanted. This audit records the implementation work; the coordinator has not been built as part of this review.

### Existing foundation and confirmed gaps

There is an existing lightweight Agent Communication Protocol (ACP), implemented locally in `internal/acp/`, and a registered `acp_send` tool in `internal/tools/acp_send.go`. It supports authenticated HTTP message submission, SSE replies, discovery info, cancellation, session endpoints, and optional job endpoints. This is a useful transport foundation, not yet a complete peer-agent coordination service. The generic gateway bridge is a separate interface for messaging sidecars.

- [x] **ACP-01 · P1 · source-confirmed — Attach an executing agent to the ACP server.** `cmd/overkill/acp_cmd.go:acpServeCmd` creates `acp.Config` without `Agent`; `handleMessages` explicitly returns 503 "no agent attached" in that state. It also supplies no job worker/store for optional jobs. Wire a real provider/tool-capable session factory and lifecycle. Acceptance: two local running instances exchange a task and a real reply through the documented CLI path.
- [x] **ACP-02 · P1 · source-confirmed — Route messages to the requested agent/session.** `acp_send` accepts `session` but never uses it; `Client.Send` sends only a fixed `From` plus content. Server `handleMessages` echoes `req.SessionID` in the response but calls the single shared `s.agent.StreamACP` without selecting it. Implement real target/session routing and per-session admission. Acceptance: messages for frontend/backend sessions cannot run against each other's state, including simultaneous requests.
- [x] **ACP-03 · P2 · source-confirmed — Cancel blocked client sends and surface stream failures.** `internal/acp/client.go:Stream` writes `out <- ev` without selecting cancellation and does not report `scanner.Err`. A consumer that stops draining can leak the stream goroutine after cancellation. `acp_send` returns a normal reply when the channel closes without requiring a terminal done event. Propagate transport/deadline errors and require explicit completion; test slow/abandoned readers and a dropped SSE connection.
- [x] **ACP-04 · P2 · source-confirmed — Expose a durable task/message handle instead of making every send a blocking 30-second wait.** `acp_send` wraps the entire send/reply in a fixed 30-second context and returns only concatenated text. A backend task can legitimately take minutes while the frontend should continue. Return message/task IDs immediately, expose status/await/cancel separately, and keep the result available after disconnect/restart.

### Coordination implementation checklist

- [x] **COORD-01 · P1 · requested feature — Add an agent registry and authenticated peer identity.** Configure named agents such as `frontend` and `backend` with role, project/worktree, supported capabilities, endpoint, and scoped credentials. Discover peers by name; derive sender identity from credentials rather than trusting a free-form `from` field. Keep credentials outside model-visible tool arguments and logs. Acceptance: only permitted project peers can message each other; unknown/unauthorized targets fail clearly.
- [x] **COORD-02 · P1 · requested feature — Add durable inbox/outbox and correlated messages.** Persist `message_id`, `task_id`, `conversation_id`, `from_agent`, `to_agent`, `in_reply_to`, type, payload, creation/deadline times, and delivery/processing state. Support requests, answers, progress, blockers, API-contract changes, and completion. Acceptance: retries and process restart preserve correspondence without executing duplicate requests.
- [x] **COORD-03 · P1 · requested feature — Expose agent-facing send/reply/status tools.** Provide concrete operations such as `agent_send`, `agent_reply`, `agent_inbox`, `agent_task_status`, `agent_await`, and `agent_cancel` backed by ACP and durable storage. A frontend agent must be able to ask "is GET /users ready, what is the response schema?" while continuing independent UI work. Acceptance: the backend receives the request, answers the same conversation, and the frontend sees the answer at a safe turn boundary.
- [x] **COORD-04 · P1 · requested feature — Add subscriptions/webhooks for progress and completion.** Support authenticated event subscriptions and optional signed callbacks for `accepted`, `started`, `progress`, `blocked`, `contract_changed`, `completed`, `failed`, and `cancelled`; include event IDs/sequence numbers, replay cursors, acknowledgements, retries/backoff, and dead-letter inspection. Apply the existing outbound URL protections to configured callbacks. Acceptance: a briefly offline frontend receives missed backend completion events once without duplicate work.
- [x] **COORD-05 · P1 · requested feature — Define API/file ownership and dependency contracts.** A frontend/backend handoff should carry endpoint/schema versions, acceptance criteria, owned files/worktree, artifacts, blocking dependencies, and compatibility changes. Integrate existing task graph/worktree machinery while preventing simultaneous uncoordinated writes to shared files. Acceptance: frontend builds against an agreed schema, receives a backend schema change, updates its dependency, and completes a combined integration check.
- [x] **COORD-06 · P1 · requested feature — Handle busy agents, cancellations, loops, and budgets.** Queue peer requests when the target is busy; deliver updates without corrupting an active turn. Support cancellation propagation, bounded queue/concurrency, deadlines, cycle detection, hop limits, task-level usage accounting, and explicit user escalation for unresolved dependencies. Acceptance: circular frontend↔backend requests terminate as blocked with evidence rather than repeatedly messaging or spending indefinitely.
- [x] **COORD-07 · P2 · requested feature — Show coordination in the terminal UI and logs.** Display agent presence, assigned tasks, dependency status, latest update, pending question, and artifacts; offer inspect/stop/retry controls and a correlated event trail. Acceptance: the user can tell whether frontend is working, waiting on backend, blocked, completed, or failed, without inspecting raw RPC logs.
- [ ] **COORD-08 · P1 · requested feature — Build an end-to-end two-agent acceptance scenario.** Frontend requests a backend endpoint/schema; backend works and sends progress; frontend builds against the contract concurrently; backend completes; frontend is notified and runs an integration test. Cover target busy, session isolation, disconnect/reconnect, crash/restart, duplicate webhook, expired credentials, failure, and cancellation. Use local controllable agents/fixtures first, then one opt-in real-provider run.

Suggested architecture: reuse ACP for authenticated transport; use PostgreSQL for peer/message/task state; deliver live progress through replayable SSE and optional signed webhooks; reuse the task graph and worktrees for dependencies and file ownership. Do not route peer control messages through public chat gateways. Agent-to-agent messages are project work/context and must not override human instructions or silently grant tool permissions.

## Review coverage and verification

The audit covers all top-level source areas and all 72 first-level `internal/` subsystems. Depth varies: core execution/security/storage/integration defects received targeted reads and probes; auxiliary modules received entry-point/interface/call-site inspection and available unit-test results. Generated protocol files, bundled model/skill data, and binary assets were treated as generated/data inputs rather than independently reverse-engineered implementations. Remaining service-dependent checks are listed explicitly in section 19.

### Section coverage ledger

| Source areas | Review focus / findings |
|---|---|
| Root manifests, install scripts, Makefile, Docker/Compose, `.github/`, `scripts/` | Build/install/release paths, YAML validity, local checks; sections 1, 13 |
| `cmd/overkill/` | Startup, mode parity, dependency wiring, CLI error handling, agent/ACP launch; sections 2, 18, 20 |
| `internal/agent`, `subagent` | Run/Stream, tools, budgets, goals, verification, simulated/real child execution; sections 4, 10, 14, 18 |
| `internal/api`, `acp`, `web` | Authentication, middleware, streaming, session routing, placeholders, peer transport; sections 3, 7, 20 |
| `internal/config`, `settings`, `features`, `hotreload` | Defaults/parse behavior, user controls, live settings and lifecycle; sections 8, 18 |
| `internal/providers`, `models`, `routing`, `tokenizer`, `cost`, `credit`; `models/` | Provider requests, failover, catalog contracts, model limits/routing, usage; section 15 |
| `internal/db`, `session`, `memory`, `automemory`, `learning`, `compaction`, `journal`, `sync` | SQL/file persistence, migrations, semantic paths, conflict resolution, compaction and replay; section 9 |
| `internal/security`, `auth`, `tools`, `browser`, `pty`, `term` | Path containment, command execution/cancellation, token handling, browser/network boundaries, terminal I/O; sections 5, 8, 10, 14, 16 |
| `internal/hooks`, `mcp`, `plugin`, `lsp`, `extensions`, `skills`; `skills/`, bundled skill embed, `examples/` | Extension contracts, real caller wiring, process startup, registry visibility and invocation; sections 6, 8, 18 |
| `internal/automation`, `cron`, `daemon`, `flows`, `tasks`, `worktree`, `checkpoint` | Job/flow/approval transitions, durable state, execution, worktree recovery and rollback; sections 10, 16 |
| `internal/gateway`, `slack`, `events` | Dispatcher/routing/pairing, native adapters/sidecars, event delivery and lifecycle; sections 11, 16, 17 |
| `internal/personality`, `prompt`, `rewriter`, `reflect`, `pipeline`, `plan`, `playbooks`, `lats`, `speculation`, `speculative` | Prompt and evaluation contracts, runtime settings, planning/execution, caches and auxiliary orchestration; sections 8, 9, 16 |
| `internal/audit`, `diagnostic`, `doctor`, `drift`, `halluscan`, `input`, `introspection`, `verify`, `walls` | Diagnostic/quality claims versus executed checks, tool receipts, static analyzers; sections 14, 16 |
| `internal/atomicfile`, `modules`, `multimodal`, `share`, `shadows`, `tags`, `testpg`, `vision` | Support-package interfaces, I/O/packaging/resource boundaries, generated assets and test dependency behavior; sections 9, 13, 16, 19 |
| `tui/src/` and frontend package/config | Client/server transport contracts, chat/session state, settings/sidebar/dashboard behavior, build; sections 1, 6, 17 |
| `bridge/` | Python server, generated module imports, wheel/dependencies, model loading, vector backends, tests/lint; section 12 |
| `docs/`, README, model/skill manifests, service configuration | Requirements and feature claims versus implementations; sections 1, 17, 19 |

### Verification results

| Check | Observed result |
|---|---|
| Native backend build, Darwin arm64, Go 1.26.10, `CGO_ENABLED=0` | Pass; `bin/overkill` generated locally |
| CLI `--help` and `--version` | Pass |
| Fresh `serve` / `tui` startup without PostgreSQL | Fail with required connection URL / connection refused; real chat not established |
| Terminal UI dependency install and typecheck | Pass |
| Terminal UI emitting build | Fail TS5096 |
| `go vet ./...` | Fail: setup self-assignment |
| Latest full Go run with Go/Git on PATH and canonical short temp path | 98 packages passed, 1 failed, 6 had no tests; 4,282 test/subtest pass events, 2 fail events, 178 skip events |
| Remaining full-run failure | Provider package: `TestModels_OpenAI`, `TestModels_Anthropic` |
| Targeted repeated router/pipeline/worktree checks (`-count=10`) | Recency and zero-duration tests reproduced intermittently; worktree passed with canonical paths |
| Initial macOS temp-path runs | Socket/filesystem/worktree failures; shorter canonical paths resolve those environment-triggered failures, but implementation portability work remains |
| Python bridge pytest (Python 3.12, lightweight dependencies) | 9 passed; real semantic models/PostgreSQL not exercised |
| Python Ruff | Pass |
| Python wheel build + archive inspection | Build passes; root server/generated import modules absent from wheel |
| Windows amd64 cross-build | Fail: Unix-only hooks/journal system calls |
| Workflow YAML strict decoding | Fail: duplicate `defaults` keys in lint/security workflows |
| Shell syntax parsing (`bash -n` / `sh -n`) | Pass; does not establish runtime/dependency correctness |
| Actual localhost API through middleware, known synthetic bearer token | Health 200; both streaming routes 500, `streaming not supported` |
| Disposable filesystem probes | New-file write through symlinked parent and patch through symlink modify fixture files outside root, returning nil error |
| Simulated sub-agent probe | Reports completed with invented 100/50 tokens and $0.001 after no real task execution |
| Live providers, peer-agent exchange, messaging accounts, Docker, database migrations, model downloads, race instrumentation | Not executed; requirements captured in section 19 and COORD-08 |

Package/test counts are execution results, not coverage percentages. Subtests are included in test-event counts. The 6 package `skip` events mean those packages have no tests; the 178 test/subtest skips largely concern missing database/service dependencies. Flaky recency/duration failures were absent in the latest full run but reproduced in repeated targeted runs, so they remain open.

### Reproduction and local evidence

The local toolchain is `.cache/toolchains/go/bin/go`; this Mac also needs the bundled Git directory on PATH because Apple developer tools are unavailable. The audit used `CGO_ENABLED=0` and a canonical `TMPDIR=/private/tmp/ok-tests`. Generated tools, logs, probes, and frontend dependencies are local ignored artifacts; the Markdown checklist is the intended repository change.

```sh
# With Go and a working Git executable on PATH:
CGO_ENABLED=0 go build -o bin/overkill ./cmd/overkill
go vet ./...
TMPDIR=/private/tmp/ok-tests CGO_ENABLED=0 go test -count=1 -json -timeout=120s ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /tmp/overkill-audit.exe ./cmd/overkill
cd tui
npm ci
npm run typecheck
npm run build
# In the bridge development environment:
cd ../bridge
python -m pytest
ruff check .
python -m build --wheel
```

Local evidence retained under `.cache/`: `audit-final-tests.jsonl`, `audit-tests.jsonl`, `audit-stability.log`, `audit-vet.log`, `audit-tui-typecheck.log`, `audit-tui-build.log`, `audit-pytest.log`, `audit-ruff.log`, `audit-python-build.log`, `audit-windows-build.log`, `audit-workflow-yaml.log`, `audit-probe.go` / `.log`, and `audit-fs-probe.go` / `.log`. These paths are machine-local evidence, not required source files or promised artifacts on future clones.

### Recommended repair order

1. Fix build/CI/install blockers and preserve real startup errors (BUILD, CI, INSTALL, RELEASE, CLI-01).
2. Repair API authentication/streaming and attach actual running agents (API-01/02/03, ACP-01).
3. Fix workspace escapes, disabled safeguards, session identity/versioning, and false child success (FS, CFG-02, CORE-02, STORE-01, SUB-01/02, VERIFY).
4. Add mandatory PostgreSQL and real frontend/backend integration gates (VALIDATE-01/02, CI-02/03).
5. Implement the requested peer-agent coordination on repaired ACP, with durable messages, safe turn delivery, progress subscriptions and end-to-end frontend/backend acceptance (section 20).
6. Complete remaining feature wiring, optional integrations, distribution validation, and documentation; close items only with their specified behavioral evidence.


## Repair evidence — 2026-10-21

Checked items retain their original findings above for traceability. Current evidence:

| Repairs | Implementation and verification |
| --- | --- |
| BUILD-01/05, CI-01/04/06/07, HOOK-01 | Compiled NodeNext TUI runs under plain Node; Node20/22 matrix and Go1.26 build paths agree. Model catalog tests assert supported invariants; deterministic pipeline timing. Actionlint validates workflows; hook fixture tests preserve aggregated failures and required dependencies. |
| API-01/02/03, CLI-01/02/05, CI-02 | One server token reaches authenticated frontend health/RPC/SSE; wrapped HTTP streaming verified using actual compiled TypeScript client. Ready/error listener signaling and installation-independent frontend lookup have regression tests; startup/child errors propagate. |
| FS-01/02/03/04, WORKTREE-01, MAC-01 | Descriptor-rooted filesystem traversal and atomic writes reject parent/final symlink escape and swaps; bounded reads retain one descriptor. Worktree canonicalization and short socket fixtures pass under ordinary macOS temporary directories. |
| SUB-01 | Production simulation removed; only explicit test factories simulate. Initialization errors, failed tool capability, canceled/failed batches and capacity/depth accounting have regression tests. Tool-backed child execution remains separately tracked under SUB-02. |
| STORE-01/02, SYNC-03 | Real PostgreSQL17 tests execute in isolated schemas. Create/save/branch/clone/merge return exact stored versions; stale writers fail; concurrent branches retain all children. Initialization errors propagate, transactional schema failure rolls back. Repeated unchanged/divergent pulls avoid duplicate conflicts and preserve local versions. |
| CFG-01 | Malformed TOML errors propagate without changing the file; blank path resolves ConfigPath; bootstrap save errors surface. |
| PY-01–06 | Declared core wheel dependencies and console entry work outside checkout. Loopback-only listener, explicit degraded mocks, serialized model load/inference, finite/dimension/top-k validation covered by 62 local tests and isolated wheel smoke. Actual semantic-model and extension-enabled pgvector acceptance remains VALIDATE-05; CI requires three pgvector tests. |
| CI-03/05 | Required PostgreSQL service job fails on missing/unreachable configuration; schema-per-test pools prevent cross-test contamination. Race failures are hard gates; UI install/typecheck/build/runtime tests are required. Local PostgreSQL-backed race runs cover storage/API/learning/gateway. |

Additional defects exposed by real integration tests were repaired: cost schema's invalid timezone-dependent index, UTC day accounting, aggregate usage independent of detail limits, central/bootstrap versus approval/routine schema drift, pool ownership/early close, correction matching case, concurrent history appends, empty flow collection contract, and API message save/restore with completion deferred until persistence succeeds. These are covered in focused regression suites; the full release acceptance remains open.

BUILD-06 is accepted for its original cross-build/required CI-matrix criteria. Native Windows runtime acceptance remains explicitly open under VALIDATE-06; a workflow definition is not an executed native result. Docker builds, clean native distributions and DMG installation are also not certified by source changes or workflow definitions.


### Shared runtime and coordination progress — 2026-10-21

- SUB-02/03: real children use the existing Agent loop with contained fs/patch, configured provider/model/URL/headers/OAuth, bounded actual provider attempts, and actual tool/file/usage receipts. Unsupported tool capabilities fail explicitly. Scripted HTTP tests prove real file creation, including valid null tool-call content. Autonomous contract-driver budget/acceptance integration remains a separate open limitation; this does not certify every contract workflow.
- TUI-01/02/03 and SKILL-01/02: active-session add/edit, retained state on failed operations, surfaced errors, keyboard interaction, shared persisted skill enablement and API registry wiring have real HTTP and compiled Ink regression coverage.
- CLI-04, CORE-02 and GOAL-02: CLI/TUI/serve share runtime services and session binding. Canonical workspaces and immutable IDs bind plans/goals/tasks/checkpoints/alarms; two-session and restart tests cover isolation, cross-session rejection, cleanup and shared goal identity. CLI and API use one persistence helper; admission extends through commit. This does not certify unrelated optional-service startup/reload features tracked separately below.
- CORE-01: API agents receive token estimation and model context capacity independently of output limits. Compaction runs before the next provider request, retains compactor-preserved messages/current request, and rejects hard overflow if compaction fails. Run and stream regression tests prove no oversized provider attempt on failure.
- ACP-02/03: requested IDs reach actual session selection and admission; legacy senders reject mismatched identities. Client tests cover canceled/abandoned consumers, complete-body deadlines, malformed/truncated/oversized SSE and required terminal completion. The server binds synchronously, reports actual addresses, backpressures instead of dropping events, and cancels/waits for workers on shutdown.
- ACP-01 implementation is wired into the CLI through the shared API session service. Two simultaneous local servers with scripted real provider HTTP/SSE execute actual filesystem writes in independent workspaces and persist messages before completion. The checklist item remains open pending literal two-process CLI/installed-runtime acceptance.
- COORD-01/02/03 backend implementation is in `internal/coordination` and six fixed-identity tools. Real PostgreSQL tests cover credential hashing/revocation, project scope, durable correlated messages, concurrent idempotent sends/claims, optimistic state changes, progress replay and restart. Per-session peer bindings are persisted and workspace-checked; common runtimes supply only their own peer's tools. User-facing peer creation, automatic worker wakeup/safe-turn delivery, dispatcher recovery and the complete requested workflow remain open; storage/tool tests alone do not complete those checklist items.
- Local provider integration exposed native OpenAI/Anthropic/Gemini factory endpoint overrides being ignored. Configured HTTP(S) endpoints are now honored with sanitized validation errors; protocol/alias and real localhost execution regressions cover this.

Release/install fixes and Docker/Compose inputs are implemented with local installer/release fixtures and schema checks. Actual Docker/PowerShell/native Windows runtime acceptance is delegated to required platform CI and has not been observed here. Native runtime bundles, desktop shell, managed database lifecycle, DMG, signing and installed-app acceptance are still outstanding.


### Verification, extension failures, and desktop lifecycle — 2026-10-21

- **VERIFY-01/02:** Shared startup attaches the real verifier; both Run and Stream detect the actual fs write/edit actions and feed failed checks into the next step. Real broken Go/JSON fixtures and the explicit disable/lifecycle tests in `cmd/overkill/verifier_integration_test.go` pass. Go verification now uses the common process-tree owner; cancellation terminates descendants.
- **EXT-04:** Real stdio MCP fixtures exercise remote isError with/without text, RPC errors, malformed results, disconnection and cancellation. Typed execution failures reach tool receipts as failures; no nil-error JSON substitute.
- **EVENT-01:** Sink Name/Send panics create SinkPanicError, including errors.Is for an error-valued panic. All-panicking dispatch fails; mixed healthy dispatch retains the documented any-success semantics.
- **Additional discovered defect:** Hot-reload unsubscribe waited forever with a live parent context. WireAgent cleanup now owns cancellation and is concurrent/idempotent; its regression test and native desktop shutdown/relaunch pass.
- Desktop native acceptance uses the real Electron window, encrypted provider setup, shared Go runtime and app-owned PostgreSQL. It writes a real workspace file, commits history, exits without a database PID file and restores the chat after relaunch. This is development acceptance evidence; installed-app/DMG, remaining audit items, license inventory and distribution validation are still open.

### Model limits, diagnostics, checkpoints and SOP persistence — 2026-10-21

- **LLM-01 repaired:** a shared model resolver keeps context capacity separate from completion limits across API, CLI, child/gateway construction, model routing and variants. Actual captured HTTP payloads cover catalog/custom models, explicit overrides and invalid ceilings; full changed-package suites and targeted race checks passed with required PostgreSQL. Model config now exposes `context_window` and `max_output_tokens`; legacy `max_tokens` remains context-only.
- **DOCTOR-01 repaired:** probes use authentication headers, reject redirects, preserve private API prefixes and redact raw/escaped keys from errors and results. Canonical Anthropic defaults use `/v1`. Captured HTTP and adversarial transport tests pass under race; `.cache/current-focused-race.log` records the parent rerun.
- **CHECKPOINT-01 repaired:** rollback selects an exact validated checkpoint hash with consistent zero-based indexing. Original HEAD and recoverable worktree/index snapshots are retained under durable refs. Unrelated ignored caches remain in place; target collisions are preserved; ambiguous stash recovery states are rejected before mutation. Disposable-repository cancellation, preservation failure, mixed history, literal filename and file/directory collision tests pass under race.
- **AUTOMATION-01 repaired:** state is published after durable save, running intent is saved before effects, persistence failures are returned, known in-process outcomes can be persisted without rerunning actions, and restarted uncertain running steps require reconciliation. Failure-injection and actual PostgreSQL restart tests pass; full automation/tools/CLI suites and focused race checks pass. This does not promise transactional exactly-once external effects or cross-process execution leases.
- **Coordination subscriptions advanced:** `coordination.events` exposes authenticated peer-filtered event pages and bounded waiting with durable replay cursors. API integration tests verify replay through a new server, pagination, unrelated-correspondence exclusion, invalid inputs and cancellation. Reading events never claims/executes work. Signed callbacks, durable delivery acknowledgements and the remaining COORD-04 requirements are still open.
- These targeted results do not replace the required fresh full integration gate after the concurrent goal/config repairs.

### Goal accounting, unsupported isolation and fresh integration gate — 2026-10-21

- **GOAL-01 repaired:** actual provider usage is persisted against immutable goal and turn identities, including fractional elapsed time, exact budget transitions and held-call deadline cancellation. Separate instances cannot concurrently admit the same goal. Run/Stream, model variants, titles, legacy/LCM/prompt compaction and contextual auxiliary calls report real returned usage; omitted provider usage stays unknown. Replacement goals cannot inherit another goal's charges. Saturated streaming cancellation releases admission, and accounting finalization failure emits an error without successful completion. Real PostgreSQL race tests cover migration, resume, replacement, single-connection pool, errors and cancellation.
- **CFG-02 repaired by rejecting unavailable isolation:** explicit `sandbox_enabled=true` fails before load migration/save/runtime construction or API mutations. Defaults remain false. API/UI report unavailable and remove the enable toggle. Workspace filters are not advertised as an OS sandbox. Direct configuration, bypassed runtime assembly, API and rendered keyboard tests pass. Actual OS isolation remains an adoption feature, not an implemented guarantee.
- **BROWSER-01 mitigated by refusing restricted browsing:** default restricted operations fail before browser allocation, DNS or HTTP. Request-time browser isolation is unavailable. A deliberate application constructor option permits trusted private/local browsing, with no network isolation claim; model tool arguments cannot enable it. Redirect/subresource counters and controlled DNS-rebinding fixtures prove zero traffic from restricted mode. Focused race and browser/tools/CLI compatibility suites pass. An enforceable restricted transport remains future work.
- **Fresh broad backend gate:** `.cache/harness-backend-full-race.log` reports 104 passing packages, eight no-test packages, no failures/races, with real PostgreSQL, frontend transport assertions and native local-database assertions required. `.cache/harness-backend-vet.log` passed. Later browser/bridge/ACP changes require their own verification; this is a measured snapshot, not final release certification.
- POSIX installer/release fixtures passed all six tests in `.cache/harness-install-release.log`. PowerShell, native Windows and Docker runtime gates remain unobserved here.

### Literal ACP processes, replay lifecycle and capability fallback — 2026-10-21

- **ACP-01 verified:** `TestTwoACPCLIProcessesExchangeCorrelatedWorkspaceWork` builds production main and launches two actual `acp serve` processes with separate configurations, PostgreSQL schemas, sessions and workspaces. Controlled provider SSE drives frontend fs work and real `acp_send`, backend fs work/reply, and frontend consumption of the correlated reply. Committed transcripts, independent sessions, exact provider calls, graceful shutdown, listener closure, process reaping and empty process groups are asserted. Parent race rerun passed in `.cache/harness-acp-process-race.log`. Windows explicitly skips this Unix lifecycle scenario; installed-distribution acceptance remains separate.
- **JOURNAL-01 repaired:** Replay closes both output/error channels on successful, empty, cancelled and read-error exits. A bounded dual-channel regression verifies closure, including cancellation during delayed playback; full journal race suite passes.
- **LLM-02 repaired:** Route rejects absent/unconfigured or capability-incompatible defaults instead of returning them. Attachments require vision, unknown required capabilities error, strict helpers cannot relax requirements, and cheapest-capable lookup returns no selection when none matches. Duplicate model IDs retain the selected provider. Legacy complexity-only selection retains its documented best-effort preference; required capabilities go through strict Route. Focused routing race tests pass; updated integration snapshot pending concurrent bridge/provider work.
- Rebuilt native desktop development resources and all nine mandatory desktop tests pass in `.cache/harness-desktop-tests.log`, with zero skips. This remains source-development acceptance, not a final installer.


### Backend review acceptance — 2026-10-21

The final backend gate executed `go test -race -count=1 -timeout=3m ./...` with required real PostgreSQL, frontend and native local-database tests: **104 packages passed**, eight packages had no test files. `go vet ./...` passed. Logs: `.cache/harness-final-full-race.log`, `.cache/harness-final-vet.log`. This validates the repaired supported paths, not all unresolved audit behavior or cross-platform distribution.

- **API-05:** validated persisted session selection; idle queue inspection creates no runtime; two concurrent real provider-backed queues stay isolated. Nested sequential execution now shares its admitted turn and goal accounting; cancellation skips unstarted items; completion totals update. Selected-session UI polling discards stale replies. Focused real PostgreSQL/race and six compiled panel tests passed.
- **GW-01/02/03/04:** constructed/persisted session router, session-bound API adapter, supported command receipts, real per-chat cancellation, deterministic sequence ordering, bounded bridge admission and owned shutdown. Held-provider/tool and persistence fixtures passed. The bridge adapter still buffers its completed response; it does not provide incremental token streaming. Router concurrency evidence is same-process, not a cross-process lock.
- **SHELL-01:** owned subprocess trees and bounded prefix/suffix capture preserve final diagnostics. Actual noisy output and descendant heartbeat cancellation pass. A per-call random completion marker prevents a preprinted static marker followed by successful early exit from reporting completion. Gateway shell shortcuts also use bounded owned subprocess execution.
- **SYNC-02:** Git commands honor caller context, default to a bounded deadline, disable interactive prompts, preserve actual filesystem/commit/push errors, and bound command output. Failed deletion commits and remote pushes can be retried using committed path history. Push and Delete commit only their own session paths, preserving unrelated staged files. Rejected commit/push, retry, pre-cancelled filesystem preservation and owned timeout fixtures pass. No cross-process serialization claim.
- **LLM-04:** classified failures apply actual cooldown; request-specific failures and caller cancellation do not penalize a healthy provider. Published stream content/usage is forwarded without retry/replay; incomplete stream termination is failure. Consecutive calls, expiry/recovery, stream usage and cancellation fixtures pass.
- **RELEASE-02:** POSIX installer/release regression suite passes six cases, including byte-preserving dry-run over existing artifacts (`.cache/harness-install-release.log`). No remote release was published.

Independent read-only review reproduced and drove the additional deletion retry, unrelated staged Push and shell successful-early-exit repairs. It also checked 20,000 randomized bounded-capture cases and focused failover/cancellation behavior. Regression logs: `.cache/review-regressions.log` (initial deletion-retry failure retained), `.cache/review-git-regressions.log` (repaired pass), and the final full race gate above.

**LLM-05 remains open:** controlled OpenAI-compatible HTTP fixtures now verify stream usage requests, cumulative usage presence, cached-token fields, malformed/incomplete stream failures and explicit compatibility opt-out. Actual paid-provider smoke acceptance has not run. Signed external webhook delivery/retries, remaining audit items, complete source-level parity, modern desktop controls and installed DMG acceptance are separate pending work.

### Desktop peer controls — implementation acceptance

The desktop now exposes narrow authenticated IPC for durable chat/agent registration, same-workspace discovery, sending idempotent requests, correspondence pagination and revision-aware cancellation. No generic RPC or backend credential is exposed to the renderer. A controlled native provider test holds a backend request while the frontend observes `started`, then returns an answer and verifies `completed`; the same registration and correspondence survive relaunch and database crash/recovery. Registration conflicts and unknown recipients retain authoritative state/drafts without false success. Late replies/errors cannot overwrite a different selected chat. Historical tool output is collapsed with readable text and expandable raw detail. The remaining COORD acceptance items retain their full original scope, including external delivery, ownership/dependency contracts, loops and opt-in live-provider scenarios; the native test does not certify those behaviors.


### Runtime and coordination acceptance — 2026-10-21, second stability package

The latest current-tree broad gate (`.cache/harness-stability-full-race.log`) passes **104 Go packages**, with eight no-test packages and required real PostgreSQL, compiled frontend and native local-database assertions. Full `go vet ./...` passes (`.cache/harness-stability-vet.log`). The offline script suite passes **17 tests** (`.cache/harness-script-tests.log`), including the new research coverage invariants. These are bounded acceptance results, not a declaration that every original capability or upstream feature is complete.

- **MODULE-01:** compressed input, total decoded tar bytes, cumulative logical file bytes, per-file size and entry count are bounded. Sparse PAX files are counted by their synthesized logical size as well as physical tar bytes. Truncated tar/gzip and bad trailers, links, unsafe paths, duplicate/case-colliding entries and empty archives fail. Extraction occurs in a fresh staging directory before publishing; detected archive/manifest failures preserve the installed tree and version. Checks cover a 212-byte sparse archive whose 7,168 physical tar bytes synthesize 9,000 output bytes, rejected under an 8,000-byte limit while an adequately budgeted extraction succeeds. Backup recovery after a process crash and cross-process updater serialization are not claimed. Existing module activation/discovery paths remain a separate integration obligation.
- **QUALITY-01:** source analysis exposes a named structural test-line ratio and unknown measured coverage; long test text cannot satisfy a configured coverage threshold. `CheckWithCoverage` accepts an actual caller-supplied Go coverage profile for the tested revision and uses weighted executed statements. Real compiled fixtures prove uncovered branches fail the threshold and preserve distinct filenames containing one versus two spaces (50% stays 50%, not a falsely merged 100%). Duplicate profile blocks merge consistently; malformed/oversized inputs fail. The wall does not itself run tests or certify profile/source correspondence, and is not a whole-repository behavioral verification claim.
- **BROWSER-02:** empty browser options use headless mode; explicit `Visible` opts out, and the existing runtime `Browser.Headless=false` config maps to it. Status reflects the selected mode. Real chromedp executable invocations against a controlled flags-recording executable verify default/headless/visible arguments. This does not claim live browser navigation or restricted-network isolation acceptance.
- **TUI-04/05:** selected-session dashboards use actual authenticated REST/RPC contracts and persisted goal/plan readers, with no arbitrary agent-map selection or fabricated budget. Continuing single-flight health probes have a three-second bound, healthy polling and capped reconnect backoff; stale requests abort on switch/disposal. Actual HTTP disconnect/restart, timeout/abort and selected-session fixtures pass with the **ten-test compiled panel/transport gate**. Empty/invalid/unknown session cases stay explicit.
- **COORD-04:** signed callbacks consume peer-filtered committed events without executing agent work. Operator-named HTTPS destinations enforce public DNS answers, pinned sockets, original TLS hostname, no proxy/redirects and bounded headers/body/timeouts. Stable event ID/body, transactional cursor acknowledgement, leased retries/backoff, per-subscription dead letters, durable fair scheduling, revoke/new subscription and generation-fenced key rotation have real PostgreSQL/local TLS fixtures. The independent review reproduced a revoked peer's stale 204 advancing its cursor; peer-enabled row locking now rejects it, and ownership is checked immediately before HTTP as well as during the request. Expected lease loss from rotation/revocation is not a service health failure. Receiver deduplication is required for at-least-once delivery; already accepted requests cannot be recalled. See `docs/coordination-webhooks.md`.
- **EXT-02/07:** API/ACP MCP managers build policy from declared configuration before execution. Explicit tool allowlists apply even when trusted; nontrusted empty lists deny; byte limits and supported explicit JSON-pointer local path bindings gate calls and rewrite validated absolute arguments. Missing bindings/schema and unsafe paths fail before remote effects. Retained configuration/policy is copied; omitted enabled means legacy-on, explicit false launches nothing; negative limits fail configuration validation. Delayed startup/disconnect/reconnect catalogs reach existing and later session runtimes, stale tools hide/deny, updated schemas reach a controlled provider, and native name collisions retain their native instance. Initialization/discovery and process trees are lifecycle-owned. Large integers remain exact through envelopes/path rewriting; cumulative rewritten arguments and outbound frames are bounded before write. Metadata lookup uses a per-server catalog rather than repeatedly sorting every server (64×256-tool microbenchmark in `.cache/mcp-catalog-benchmark.log`). This is argument validation, not an OS/remote-filesystem sandbox. Direct CLI MCP startup, remote transports/resources/OAuth and broader child inheritance remain separate gaps; see `docs/mcp-capabilities.md`.

Focused evidence: `.cache/module-quality-race.log`, `.cache/browser-options-race.log`, `.cache/callback-parent-race.log`, `.cache/mcp-parent-race.log`; each package also received a concrete source/fixture review before the broad gate. Independent executed reviews drove sparse expansion, filename preservation and revoked acknowledgement regression tests.

**API-04 remains open (partial repair):** actual bounded post-write verifier observations now expose start/pass/fail/skip/cancel with selected-session isolation and owned excerpt storage; no-match or absent checkers do not invent passes. Full self-evaluation is explicitly unavailable (`self_evaluation_available:false`, unsupported confidence/iteration/red-team fields null). The independent SelfEvaluateLoop/AutoMode lifecycle still needs production wiring and phase-specific acceptance. Snapshots are volatile/latest-100, not persisted suite-wide coverage.

Windows and Linux current-source cross-compilation have local evidence, but native Windows, real container startup, installed DMG and paid-provider gates retain their separate pending scope. Original baseline findings remain above for traceability.

### Additional desktop packaging and Keychain acceptance

The current desktop source suite passes **18 tests without skips** (`.cache/desktop-keychain-tests.log`). A real controlled blocked-Keychain fixture verifies that startup creates a responsive native window, public state does not decrypt credentials or wait behind blocked decryption, the operation times out with recovery instructions, and a late result cannot start a backend. Unit acceptance covers late encryption publishing nothing, replacement-key recovery without decrypting an inaccessible old credential, and stop fencing a pending configuration write before spawning a process. Packaging verifies every inventoried input hash and rejects extra runtime files before building.

The first development installer scenario has executed image mount/copy, signed-app launch outside the checkout, peer exchange, file edit, same-version app replacement, durable relaunch and database crash/reconnect on macOS26.5.2 arm64. The frozen XvnHeb ad-hoc image also passed its own installed acceptance after the user completed the exact fixed copy's Keychain authorization; acceptance is never transferred between image hashes. See `desktop/INSTALLED_ACCEPTANCE.md` for the exact passed/pending images. These additional checks do not close the broader original validation or public distribution findings.

### Skill runtime acceptance and current broad gate

**SKILL-03 is accepted.** Root's persistent loaded registry reaches the standalone CLI constructor and the shared API factory used by API/ACP/gateway execution. The API acceptance creates actual session agents through `getOrCreateAgent`, observes the skill body in the HTTP provider request, disables it through authenticated RPC, verifies exclusion for both an existing and a later session, and reopens persisted enablement. The two-process CLI acceptance now loads enabled/disabled skill files from isolated homes and checks actual provider requests from both ACP processes; it also executes the separate standalone terminal loop with the same inclusion/exclusion assertions. No test manually sets the registry on those executing agents. Per-channel/live-provider acceptance remains separately open.

Focused race logs: `.cache/skills-runtime-acceptance.log`, `.cache/skills-cli-process-acceptance.log`. The latest full required race run passes **104 packages**, with nine no-test packages, and full vet passes (`.cache/installer-stability-full-race.log`, `.cache/installer-stability-vet.log`). This run includes real PostgreSQL, frontend compilation and native localdb checks. It also verifies the notice-copy integration fix: two verbatim Go notices are retained as `.go.txt`, preserving hashes/provenance while excluding them from compilation. The original audit now has 78 repaired and 40 open findings.


### Terminal clarification, file hooks, routing costs and truthful reload — 2026-10-22

- **CLI-03 accepted:** the standalone terminal has one shared, cancellable Unix input reader for REPL messages and model questions. Actual built-process/provider/PTY fixtures prove numbered choices, free text and cancellation, including already-buffered next lines; a cancelled question leaves later input unconsumed. Headless/JSONL and non-Unix interactive questions return explicit unsupported errors. Ordinary non-Unix REPL input is retained; native Windows terminal clarification is not claimed. Five real process cases also verify correlated tool results and owned process cleanup.
- **EXT-01 accepted:** shared CLI/API/ACP/TUI/desktop runtime construction loads the owner's hooks directory into each executing agent, and legacy channel constructors use the same loader. Real ACP tool execution and built standalone CLI questions run executable before/after scripts and verify event point, actual tool payloads, and session identity. Executable workspace scripts are excluded. Unix ownership, group/world write and symlink rejection, Windows ownership/DACL checks, and execution-time trust rechecks apply. First admitted Run/Stream and Shutdown own once-only start/end events; shutdown hooks share a bounded independent context. Actual live channel/native Windows behavior remains under separate validation items. See [file hook contracts](docs/file-hooks.md).
- **LLM-03 accepted:** cost priority changes provider/model selection among eligible configured candidates. Estimates are USD/request with explicit input/output assumptions (default completion 1,024); capability and known context/output ceilings remain hard limits. Unknown prices do not masquerade as free prices; explicit zero-cost metadata survives catalog flattening. Tests verify provider changes/restoration, asymmetric prices, scaling, invalid estimates/prices and concurrent toggles. Cache/media/tiered prices, retries and later tool turns are excluded; omitted input estimates cover the user text only.
- **CFG-04 remains partial:** actual Run/Stream HTTP fixtures prove already-submitted requests retain their model and subsequent requests use the new model/limits. Reload outcomes distinguish applied model changes from pending restart/reassembly fields. CLI/API log reporters expose field names and classified errors without configuration values or raw parser errors; CLI now owns its unsubscribe callback. Scanner replacement and unimplemented startup consumers are not claimed. Watcher startup/lifetime remains EXT-06.

The first broad run caught an unaccounted-but-wired `NewLogReporter` constructor and one orphan-recovery startup deadline. The constructor inventory now names its actual CLI/API consumers. Database startup deadlines now preserve bounded PostgreSQL diagnostics instead of discarding them. A focused orphan recovery run and five full localdb suite repetitions passed; these repetitions do not explain or erase the earlier timeout. Focused required-PG/frontend race verification passes the changed cmd/API/hotreload/hooks/routing/doctor packages. The final full required-PG/frontend/localdb race run passes 104 packages (nine no-test packages) with zero failures; full vet passes. Logs: `.cache/backend-next-full-race-final.log`, `.cache/backend-next-vet.log`, `.cache/backend-next-integration-followup.log`, `.cache/localdb-orphan-stress.log`. These source changes are outside the frozen, checksum-accepted XvnHeb DMG.


### Configured services and revisioned handoff integration — 2026-10-22

- **EXT-05 accepted:** session assembly starts configured language servers on a session-owned context and waits for bounded initialize readiness. Failure/cancelled assembly stops initialized siblings; cleanup sends shutdown/exit and reaps server/descendant processes. Duplicate/conflicting language declarations fail before launch. Real configured subprocess fixtures prove `lsp_hover` through the production shared registry still works after the assembly request is cancelled. A JSON-RPC close/dispatch race was repaired with single-owner pending-response removal. Repeated race/package/vet evidence is in `.cache/lsp-acceptance-race.log`, `.cache/lsp-package-race.log`, `.cache/lsp-vet.log`. Actual gopls/pyright and full protocol synchronization remain under broader validation.
- **CFG-03 accepted:** shared CLI/API construction respects independent enabled, sycophancy, anti-pattern and provider-rewrite options. All 16 combinations are checked at actual HTTP provider boundaries, including rewrite-model selection and request counts; disabling provider rewriting makes no rewrite call. Browser timeout reaches shared runtime and bounds actual launch/CDP actions while preserving earlier parent cancellation. The retained first CDP context survives successful startup and later action timeouts. Controlled executable/CDP fixtures cover actual Runtime.evaluate requests, timeout and reuse; they do not claim Chrome rendering or network isolation. One full-suite run exposed a one-second fixture startup allowance that was too tight under race load; the public configured cap is now three seconds (below the ten-second URL default), with an independent ten-second setup watchdog and no retries/private timeout mutation. Focused repeated logs: `.cache/config-runtime-acceptance.log`, `.cache/config-browser-rewriter-race.log`, `.cache/config-browser-load-budget-race.log`, `.cache/config-browser-runtime-acceptance.log`.
- **COORD-05 remains partial:** durable typed contracts now record owner, schema/version, criteria/artifacts, workspace/worktree references, pinned dependencies and compatibility changes. Revision/ack fencing, recursive agreement readiness, cycles, portable path aliases, conflicting claims and enabled-peer authorization have real PostgreSQL acceptance. Registered-peer registry guards cover ordinary/concurrent/dynamic tool resolution and hold a project lock through synchronous native file/patch effects; opaque tools require physically disjoint foreign workspaces. A real shared-runtime workflow writes a backend response, builds/executes a JavaScript client, delivers a durable breaking-change message, observes stale readiness, updates the dependency/client, and passes the actual endpoint check. HTTP controls derive owner from session bindings and reject forged identity/project access. Native shared-workspace conflicting writes/edits/patches/mkdir/shell effects fail without clobbering the backend artifact. See [precise contracts and boundaries](docs/coordination-contracts.md). This does not certify external editors, unregistered sessions, detached/remote delayed effects, OS isolation, task-graph/worktree lifecycle or an autonomous paid-provider scenario. Readiness is interface agreement, not completed task acceptance. Listing has combined/count bounds and fails explicitly rather than reporting a truncated complete inventory.

Focused integrated race evidence: `.cache/coord-contract-runtime.log` (cmd/API/coordination/tools), with Node/frontend and real PostgreSQL required. The acceptance also caught and repaired the `/var` versus `/private/var` canonical-workspace alias mismatch. The final full current-source required-PG/frontend/localdb race gate passes 104 packages (nine no-test packages), with zero failures/race reports; full vet passes. Logs: `.cache/contracts-config-lsp-full-race-final.log`, `.cache/contracts-config-lsp-vet.log`. Windows amd64 source cross-build also passed; native Windows behavior remains unverified. The original checklist is now 83 repaired /35 open; the frozen installed DMG remains a separate checksum-bound artifact.


## Memory, peer admission, reload and release checkpoint — 2026-10-22

- **MEM-01 accepted:** explicit opt-in provider extraction reaches actual CLI and API Run/Stream paths. A real CLI process restart recalls persisted workspace facts; disabled settings cause no extra provider calls or recall. Strict bounded JSON, secret-pattern filtering, private atomic storage/locking, deduplication, retention and cancellation are exercised. Extraction checks active goal limits and accounts returned provider usage before publishing; filtering is heuristic and legacy Markdown import remains unsupported. See `internal/automemory/README.md` and `.cache/automemory-cli-acceptance.log`, `.cache/automemory-provider-acceptance.log`.
- **EXT-06 accepted:** root owns and starts the shared watcher before command readiness, and joins it on normal exit, returned command errors and cancellation. File-parent watches survive atomic replacement; existing nested directories and initially missing ancestors are registered. Session subscriptions retain their own stop callbacks. A built, running CLI picks up three successive atomic YAML replacements through actual provider request models and exits without orphan processes. Bus/application concurrency and pending-timer shutdown are covered. `.cache/reload-process-escalation-acceptance.log` contains that passing executable case; the fixture-only role omission in its separate tools test was corrected and the final complete gate includes it. CFG-04 remains partial for unimplemented live consumers.
- **RELEASE-01 accepted:** complete publication-input dry runs execute the workflow's publish block with a captured `gh` stub. Exact five-target assets/checksums, explicit repository, checkout and generated release notes are verified; malformed/symlink/unchecked assets fail before publication. No real release, tag or push occurred. All 29 script fixtures and actionlint passed in `.cache/installer-acceptance/`.
- **BUILD-06 accepted:** Windows amd64 CGO-free compilation passes and required cross-build/native CI coverage is defined. Native Windows execution is still unverified (VALIDATE-06); local POSIX installer fixtures do not certify the PowerShell path.
- **COORD-06 remains partial:** server-derived ancestry/leases bound request chains to eight hops and 32 requests per conversation. Admission caps peer/project queues at 128/512 and persisted execution attempts at three; ordinary idempotent retries retain their original receipt. Circular requests produce separate durable blocked/requires-review escalations. Participant-only cursor access is exposed in `agent_inbox` and authenticated `coordination.escalations`. Task-wide token/cost accounting and dependency resolution remain open; these protocol limits are not a spending ceiling.

The controlled actual-agent circular request fixture passed in `.cache/peer-cycle-escalation-acceptance.log`, proving Agent.Run/tool context propagation and authenticated escalation access. The required PostgreSQL/frontend/localdb full race gate passed 104 packages (nine no-test packages) with zero failures/races (`.cache/memory-budget-reload-full-race-final.log`); full vet and Windows amd64 compilation also passed (`.cache/memory-budget-reload-vet.log`, `.cache/memory-budget-reload-windows.log`). These backend changes are newer than the checksum-bound installed development DMG; that image was not rebuilt in this checkpoint. The original checklist is now **87 repaired / 31 open**.


## Task usage, sync, language-aware audits and durable status — 2026-10-22

- **SYNC-01 accepted:** actual CLI constructs one live backend/store manager, commits each completed transcript before upload, and drains the latest queued turn before closing the database. Independent managers, slow-upload coalescing, timeout/cancellation/join and disabled/error configuration are covered. A real race-built CLI uploaded its first turn while still running, then preserved the full final transcript on immediate quit against a controlled file backend. `.cache/sync-cli-acceptance.log`, `.cache/sync-full-race.log`. Live Git/S3 account compatibility remains VALIDATE-03.
- **AUDIT-01 accepted:** language-aware Go/Node/Python checks execute through `/audit start|run` and authenticated `session.audit`. Baselines are session-owned, snapshots include non-Go source, ignored dependencies/research are excluded, and results record real command outcomes/unavailable checks. The dedicated production worker actually reads/searches/diffs and rejects mutations; no LSP/terminal capability is claimed. Real Go/TypeScript-script/Python checks catch an injected Python failure. `.cache/audit-final-acceptance.log`, `.cache/audit-full-package.log`, `.cache/audit-vet.log`; process cancellation is separately verified in `.cache/audit-process-ownership-final.log`. See `docs/completion-audits.md` for exact boundaries.
- **API-04 remains partial:** live Run and SSE Stream post-write checks plus actual heuristic confidence reach RPCs and independent PostgreSQL status snapshots. Cold reads preserve results without creating an agent; session isolation, transcript-save independence, delete cascade, restored check IDs, persistence errors and in-flight `unconfirmed` state are verified. Confidence is explicitly not test proof; the full iterative evaluation loop still has no production caller and standalone direct CLI agents do not attach the status sink. `.cache/evaluation-status-race.log` (three repeats), `.cache/evaluation-status-vet.log`.
- **COORD-06 remains partial:** atomically configured root request limits now aggregate provider token usage and configured-price cost estimates across peers/conversations of an owned task. Caller identity, inherited ancestry and budget ownership cannot be replaced through model inputs. Idempotent durable samples, task-level review evidence, complete versus unknown stream usage, cancellation, late cache fields, ambiguous pricing and a lost-execution journal are exercised. A failed PostgreSQL usage write must not mark the execution complete after database recovery. Actual provider/tool tests stop before a side effect when limits/unknown usage require review. Limits apply between calls; in-flight/concurrent overshoot, invoice reconciliation, external tool charges and explicit unconfirmed-journal resolution remain limitations. `.cache/task-budget-final-acceptance.log`, `.cache/task-usage-full-packages.log`, `.cache/task-accounting-stream-acceptance.log`. See `docs/coordination-budgets.md`.

The original checklist is now **89 repaired /29 open**. Final required PostgreSQL/frontend/native-localdb full race gate: **104 packages pass**, nine no-test packages, zero failures/race reports (`.cache/task-sync-audit-status-full-race-final.log`). Full vet and Windows amd64 CGO-free compilation pass (`.cache/task-sync-audit-status-vet-final.log`, `.cache/task-sync-audit-status-windows-final.log`). The catalogue rebuild validates 8,667 original records, 19,171 overlapping normalized units and 3,317 evidence files with zero errors (`.cache/task-sync-audit-status-catalogue.log`); it does not certify exhaustive semantic parity. This batch has not changed or rebuilt the frozen installed DMG.


## Workspace views, extension controls and personality — 2026-10-22

- **EXT-03 accepted:** real skill/hook/plugin/MCP inventories replace the unsupported stub registry, with explicit toggle capabilities and configured/discovered/live distinctions. Persistent skills affect real CLI/API prompts; API hook toggles change the selected session's actual registry. Controlled MCP reports connected state, while discovered plugins remain inactive and unsupported toggles fail before execution. Completion hooks now fire once on successful Run and Stream, are cancellation-bound and joined on shutdown. Session hook controls are not persisted across runtime replacement; plugin process execution and per-server MCP toggling remain unsupported. `.cache/extensions-runtime-acceptance.log`, `.cache/extensions-after-turn-final.log`; `docs/extensions-runtime.md`.
- **PERSONA-01 accepted:** shared small style directives implement none/off/subtle/witty/full in actual CLI, API Run/Stream, normal children and audit workers. `--no-personality` overrides the runtime without mutating saved config. Host system/safety/tool/output-format instructions retain priority. Controlled HTTP/real CLI acceptance proves propagation and distinct fixture responses, not guaranteed stochastic tone compliance. `.cache/personality-provider-acceptance.log`, `.cache/personality-vet.log`.
- **COORD-07 remains partial:** the desktop now has native task state/usage/review views, contract/readiness/dependency/artifact inspection, real per-chat checks and paginated correlated activity. Fixed IPC operations preserve main-frame trust and never expose arbitrary RPC/credentials. Retry drafts preserve unnamed conversation scope and both budget components; stale task/draft responses cannot overwrite later selections. Original terminal UI parity, complete presence/assignment/task graph, native notifications and review/resolution controls remain open. The real backend/native scenario exercises budget interruption, cancellation freeing a blocked peer, unchanged limits preventing extra provider calls, schema agreement blockers and actual JSON checks. No new DMG was built.
- **Harness comparison:** 13 bounded assessments across all eight harnesses were re-read against pinned source/docs/current consumers; nine obsolete plugin anchors were replaced without blanket parity upgrades. One Hermes introduction record is now linked as implementation context, reducing its unresolved originals from six to five without adding a feature. 149 exact SHA/span checks and full catalogue validation pass. See `docs/harness-research/backend-reinspection.md`; full extraction/parity remains open.

Final gates: **104 Go packages pass under race**, nine no-test packages, with real PostgreSQL/frontend/native-localdb required (`.cache/workspace-extensions-persona-full-race.log`). Full vet and Windows amd64 CGO-free build pass (`.cache/workspace-extensions-persona-vet.log`, `.cache/workspace-extensions-persona-windows.log`). **26 source desktop tests pass with zero skips**, including the original encrypted native lifecycle case, blocked-Keychain responsiveness and new real workspace/peer-budget views (`.cache/desktop-workspace-full-native-serial.log`). The original checklist is **91 repaired /27 open**. The frozen installed development image is unchanged and does not include these newer source panels.

## Terminal input ownership and accurate capability documentation — 2026-10-22

- **TERM-01 accepted:** the OSC11 probe reads synchronously with a deadline and a 64-byte bound. Unix readiness polling and one-byte nonblocking reads restore descriptor flags before returning; no buffered reader or worker remains to consume frontend input. Real pseudo-terminal fixtures verify no-reply and partial-reply timeout, terminal-mode/flag restoration, subsequent input ownership, BEL/ST success without trailing-key prefetch, response bounds and hangup. Race tests pass, including 20 repetitions (`.cache/terminal-probe-race.log`, `.cache/terminal-probe-stress.log`). Windows uses synchronous console event polling instead of a potentially blocking byte read and restores unrelated events; AMD64/ARM64 test binaries compile, but native Windows console execution is unverified. Restored unrelated Windows events may be reordered behind events arriving during the probe. Linux arm64 term compilation passes.
- **DOC-01/DOC-02 accepted:** README, gateway setup and local installation/architecture docs now distinguish native adapters, external sidecars and unsupported Feishu, real entry points and live-delivery limitations. They describe PostgreSQL, workflow files, process memory, desktop settings/private cluster, WhatsApp SQLite and the bridge's PostgreSQL default/explicit disposable in-memory mode. Unwired plugin, relationship and evaluation surfaces are explicit. The source review also records that the serve platform hub lacks a dispatcher; fixing that path and live external account validation remain separate work. Twenty local links, six TOML/JSON examples and nine source-wiring assertions pass (`.cache/documentation-capabilities-storage-acceptance.json`). This documentation acceptance does not claim external delivery or new platform execution.

Current production source passes **104 Go race packages**, nine no-test packages, with required real PostgreSQL/frontend/native local database (`.cache/terminal-docs-full-race.log`), full vet (`.cache/terminal-docs-vet.log`) and Windows amd64 compilation (`.cache/terminal-docs-windows.log`). The preceding sequential desktop suite passed all 26 cases without skips; desktop source is unchanged in this batch. The original checklist is **94 repaired /24 open**. The frozen development DMG has not been rebuilt.

Research follow-up: Hermes retains 1,501 original IDs, now 4,031 canonical units and two unresolved original groups, with supplemental semantic gaps still explicit. The three mapped groups add 29 contracts/variants and reuse 46 existing hook IDs; 199 exact SHA/span checks pass. Full catalogue rebuild has zero errors across8,667 original IDs, 19,200 overlapping normalized units and 3,347 evidence files (`.cache/terminal-docs-hermes-catalogue.log`). No runtime parity was upgraded.

## Durable browser flows, strict canvas transport and SDK tracing — 2026-10-22

- **WEB-01 accepted:** registered authenticated routes now persist managed workflow records in PostgreSQL, wired by the actual `overkill web` entry point after session migration. Session-qualified list/get/create/actions, revision locks, terminal transitions, reported wait/blocker invariants, explicit pagination and committed update events have real HTTP/database acceptance. Store/server reconstruction preserves records and deleting the session cascades deletion. Strict bodies and query parsing prevent duplicate/aliased fields, malformed scope fallback and stale updates. JSONB input failures return 400 without consuming a revision; failed migration rolls back and concurrent retry is serialized. Six new top-level managed-flow tests plus existing web tests pass under race (`.cache/web-flows-canvas-race-final.log`). These are caller-reported workflow records, not an automatic scheduler, task-graph enforcement or agent continuation execution. No browser flow panel is claimed. See `docs/web-flows.md`.
- **WEB-02 remains partial/open:** canvas requests now reject duplicate/trailing/ambiguous JSON, unknown or invalid fields and conflicting session/surface labels. Snapshot returns honest 501 unsupported. Five real-route test groups cover auth, all five action identities, invalid events, reset and unsupported capture (`.cache/canvas-validation-race.log`, combined final web race log). The embedded client still has no A2UI renderer, durable surface/reconnect replay or verified PNG capture. See `docs/canvas-transport.md`.
- **Hermes SDK source pass:** 47 SDK/registry leaf members and 172 API property bodies now have exact arguments, import targets and consumer boundaries. The ledger records registerSlot argument labels, timeAgo timestamp-unit and cn public type differences. Existing canonical IDs and all parity statuses are preserved; external UI implementation and individual backend auth/profile/effect tracing remain unresolved. Independent 226 SHA/span checks and normalization pass (`.cache/hermes-sdk-validation.json`). See `docs/harness-research/hermes-sdk-followup.md`.

The original checklist is **95 repaired /23 open**. Final required full-source verification passes **104 Go race packages**, nine no-test packages, zero failures/race reports (`.cache/web-flows-canvas-full-race-final.log`, `go test -race -p 4 ./...` with real PostgreSQL/frontend/native-localdb required). Full vet and Windows amd64 compilation pass (`.cache/web-flows-canvas-vet-final.log`, `.cache/web-flows-canvas-windows-final.log`). An earlier full run passed the web package but failed existing browser/SSH startup-marker fixtures at three seconds (`.cache/web-flows-canvas-full-race.log`); both pass isolated rechecks (`.cache/web-gate-process-startup-recheck.log`). The final full run bounds package concurrency at four without changing assertions and passes both fixtures. The exact cause of the earlier startup misses is unconfirmed; the passing bounded run does not certify every load/concurrency condition. No new desktop source, DMG build or installed acceptance is claimed.

The rebuilt source catalogue validates **8,667 original IDs, 19,200 overlapping normalized units and 3,361 evidence files** with zero errors (`.cache/web-flows-hermes-sdk-catalogue-final.log`). Focused source doc links and JSON example validation pass (`.cache/web-flow-docs-validation.json`). These counts and structural checks do not prove exhaustive discovery or runtime parity.

## Reload consumer boundary, composed handoff and UI source dependency — 2026-10-22

- **CFG-04 accepted:** the original misleading reload promise is now verified against real execution, rather than only notification metadata. Atomic YAML replacement passes through the actual filesystem watcher and both Run/Stream provider paths. Only the model changes; scanner/persona edits report restart-required, and the installed custom command scanner still blocks the model-generated shell call before execution. A real native shell positive control writes successfully; denied calls create no artifact, never reach the execution guard, and return their scanner error to the provider. `.cache/reload-scanner-boundary.log`; `internal/hotreload/scanner_boundary_test.go`. This closes the reporting/effect contract; it does not implement live scanner replacement, install persona overrides, or guarantee that unsupported YAML fields acquire a startup consumer. Broader live settings adoption remains open.
- **COORD-08 remains partial:** a composed deterministic local-provider scenario now exercises authenticated frontend RPC, production workers, actual guarded fs/shell effects, concurrent independent work, schema v1→v2 pinning/acknowledgements, queued progress/change delivery at a safe turn boundary, real HTTP client integration, immutable revision history, saved transcripts and causal reply/parent links. Three race repetitions pass (`.cache/coord-composed-acceptance.log`); retained actual artifacts and manifest are in `.cache/coord-composed-artifacts`. The backend artifact is JSON served by a test-owned HTTP handler; frontend artifacts are ui.txt plus an executed Python client, not an authored backend server or built/rendered UI app. Provider usage is omitted and explicitly journaled as unknown (17 samples in the retained run); no billing-total claim. Autonomous paid-provider and remaining cross-process/acceptance cases are open.
- **Test readiness:** browser flag and SSH descendant fixtures now allow 15 seconds for native process setup, with early-exit diagnostics and SSH cleanup joining even failed setup. Browser configured-timeout fixtures and the SSH three-second cancellation/descendant assertions are unchanged. Five repeated targeted race runs pass (`.cache/helper-startup-handshake-race.log`). This addresses fragile setup assumptions; the precise cause of earlier three-second startup misses is not established.
- **Hermes UI dependency:** exact @nous-research/ui 0.18.2 was acquired without execution; root independently verifies tarball SHA-512 against both pinned lockfile and registry metadata, plus recorded SHA-256 (`.cache/hermes-ui-root-integrity.json`). All 24 components and two hooks have implementation/type/consumer boundaries; 14 shipped source maps reproduce packaged src. The SDK/dependency pass has 667 SHA/span rereads over 300 distinct spans, with zero structural errors and no new canonical IDs/parity upgrades. Two original groups and supplemental/transitive/backend effect/auth/profile gaps remain. See `docs/harness-research/hermes-ui-dependency-followup.md`.

Final required source gate passes **104 Go race packages**, nine no-test packages, zero failures/race reports at Go's normal package concurrency (`.cache/reload-composed-full-race.log`), with real PostgreSQL/frontend/native local database required. Full vet passes (`.cache/reload-composed-vet.log`); the new reload acceptance test cross-compiles for Windows AMD64 (`.cache/reload-windows-test-compile.log`), without native Windows execution. Production source is unchanged in this batch. The rebuilt catalogue validates **8,667 original IDs, 19,200 overlapping normalized units and 3,402 evidence files**, with zero errors (`.cache/reload-composed-hermes-catalogue-final.log`). Original audit: **96 repaired /22 open**. Desktop source and the frozen checksum-accepted DMG are unchanged.

Source-owner verification correction: two original Pi README references previously resolved to Overkill's README because the catalogue tried the workspace before the upstream snapshot. The catalogue and normalization helper now resolve ordinary upstream paths only under their owning snapshot; production and explicitly collected paths retain declared workspace ownership. Missing upstream files and escaping symlinks cannot fall back to a same-name workspace file. Nine normalization/regression tests pass (`.cache/harness-evidence-ownership-tests.log`), both actual Pi references resolve to the pinned Pi README (`.cache/pi-evidence-ownership.json`), and the full catalogue revalidates with zero errors. The distinct file count becomes 3,402 because the incorrect Overkill README was removed from that union; IDs and parity statuses are unchanged. Earlier green catalogue logs did not establish correct ownership for those two references.

### Terminal coordination and desktop agreements — 2026-10-22

COORD-07 remains partial. The terminal Agents panel now uses authenticated session-scoped peer registration, durable request sending, task/activity/usage inspection, optimistic cancellation and explicit retry drafts preserving conversation, task and both budget limits. One operation runs at a time; switching chats aborts transports and ignores stale replies. Enabled registrations are not live presence: the UI explicitly reports presence unknown. Contracts editing, escalation resolution, complete task graphs and broader production acceptance remain open. Typecheck/build and all 17 compiled HTTP/Ink frontend tests pass (`.cache/tui-coordination-root-tests.log`); usage is measured/estimated with unknown and pending samples exposed. See `tui/COORDINATION.md`.

Desktop contract cards now offer explicit revision-specific acknowledgement through fixed session-bound IPC. Duplicate clicks, old-session/old-revision replies, failed retries and list outages are guarded; polling preserves open cards and errors. Real native backend/database acceptance records revisions 1 and 2, rejects a stale revision and keeps agreement distinct from passing tests (`.cache/desktop-contract-ack-native-focused.log`). The initial fixture update omitted required compatibility metadata; after correcting the fixture, this acceptance passes. No production contract rule was weakened.

Full required PostgreSQL/frontend/localdb Go race acceptance passes 104 packages at normal concurrency, with nine no-test packages and zero failures/race reports (`.cache/coordination-ui-full-race.log`); vet passes (`.cache/coordination-ui-vet.log`). Original audit totals remain **96 repaired /22 open**. These source changes are outside the unchanged checksum-bound XvnHeb development DMG.

Hermes admin follow-up expands 41 original table rows into 54 existing endpoint units and independently verifies 221 exact whole-file SHA/span excerpts. It records auth/profile/child-process and cron-owner boundaries without upgrading unresolved groups or parity. Full catalogue validates 8,667 original IDs, 19,200 overlapping normalized units and 3,422 evidence files, zero errors (`.cache/coordination-ui-hermes-catalogue.log`); changed historical production anchors remain flagged for review. See `docs/harness-research/hermes-admin-followup.md`.

Final source desktop acceptance passes **32 tests, zero skips/failures**, including the real encrypted-settings/native relaunch case and the workspace two-revision acknowledgement scenario (`.cache/desktop-contract-ack-full-native.log`). Visual review of `.cache/desktop-contract-inspector.png` confirms the revision, saved acknowledgement and agreement-versus-checks wording. The frozen DMG remains unchanged.

### Scoped instructions, iterative evaluation and contract editing — 2026-10-22

**API-04 accepted:** configured auto mode now invokes the existing confidence/revision loop through actual Run and Stream completion. The first iteration uses the existing answer; revisions retain original admission, goal accounting, cancellation, context and total-step limits. Bounded per-session executing/assessing/final observations persist independently of transcripts; cold in-flight state becomes unconfirmed. Prior accepted observations cannot hide current verifier/confidence work. Default-policy task-classification acceptance supplements controlled-scorer cases: the native flow exposed an empty-title bug, now repaired with real-policy Run/Stream tests (`.cache/evaluation-default-policy-race.log`). Accepted means confidence policy accepted, not tests passed; red-team remains unavailable/null and loaded plan phases do not chain. See `docs/iterative-evaluation.md`. Original totals become **97 repaired /21 open**.

**Bounded A03 adoption:** explicitly enabled global/workspace/directory AGENTS.md guidance now reaches actual standalone CLI, shared API Run/Stream/resume and delegated/audit child prompts. Files reload each request; edits, removal and revocation affect later prompts, scope is contained, bounded invalid files produce diagnostics, and current guidance survives prompt compression/history compaction and is budgeted. Default is disabled; legacy independent web/gateway constructors, automatic read-triggered discovery and memory/pgvector acceptance stay open. See `docs/scoped-instructions.md`. Three existing Codex/OpenCode/Factory instruction assessments are now partial with actual current anchors; unmatched upstream trust/filename/history/directory contracts remain explicit.

**COORD-07 remains partial:** desktop users can create/edit their own versioned contracts with criteria, path ownership, named artifacts and deliberately pinned dependencies. Immutable captured revisions and server identity/ownership guards reject stale or foreign edits. Polling preserves drafts; pending submission is serialized; late results cannot affect other chats. Ambiguous saves retain draft text and require fresh inspection instead of retrying creation automatically. Fixed IPC limits spec fields and size. The native scenario exercises creation, an updated version with compatibility metadata, a pinned backend dependency and stale-edit rejection. Task graphs, presence and escalation resolution still need completion.

Desktop settings now expose workspace-root instruction authorization and explicit autonomous mode, both off by default. The autonomy label explains skipped tool approvals and additional provider calls. Desktop and TUI consume revised authoritative final responses; Checks/evaluation views keep confidence separate from actual test passes. The source native fixture uses disposable credentials and a controlled provider; encryption/relaunch has its separate real Keychain test. The frozen XvnHeb DMG is unchanged and excludes these source additions.

### Instruction adoption, real iterative review and desktop contract authoring — 2026-10-22

The source checkpoint is **97 original repairs /21 open**. API-04 now has production Run/Stream loop invocation and durable, per-session executing/assessing/final states; recovered in-flight work is unconfirmed. Initial answers are reused, revisions share admission, goal/context/step limits and cancellation, and accepted confidence remains distinct from test passes. A real default-policy native scenario exposed missing task-title classification; the repaired default policy now has actual Run/Stream regression coverage. Loaded plan phases, production red-team wiring and revision token/tool streaming stay open. See `docs/iterative-evaluation.md`.

A03 now includes opt-in global/workspace/directory AGENTS.md prompt guidance, per-request reload, revocation, containment and whole-file/total bounds, budget accounting and post-compression retention through CLI/API/children. Legacy independent web/gateway constructors, aliases/read-triggered discovery and memory/pgvector guarantees remain open. See `docs/scoped-instructions.md`. Exact Codex/OpenCode/Factory instruction rows are now partial with current evidence and unmatched upstream contracts, not blanket parity upgrades. The catalogue validates 8,667 original IDs, 19,200 overlapping units and 3,424 evidence files, zero errors (`.cache/instructions-evaluation-catalogue-final.log`).

The desktop contract editor publishes owned interfaces, criteria, paths/artifacts, dependency pins and compatibility revisions through constrained IPC. Native acceptance verifies creation/update and a stale draft that cannot overwrite a newer revision. Ambiguous saves require fresh inspection; creation is not automatically retried. Settings explicitly opt into workspace-root guidance or auto autonomy (including skipped tool approvals); both default off. Real native acceptance observes the instruction marker in the provider prompt and two actual review iterations with the revised final answer. TUI and desktop final-answer consumers and evaluation views are updated.

Verification: `.cache/instructions-evaluation-full-race-final.log` passes 105 packages, nine no-test packages, zero failures/race reports at normal concurrency with required PostgreSQL/frontend/localdb. Vet and Windows source cross-build pass (`.cache/instructions-evaluation-vet-final.log`, `.cache/instructions-evaluation-windows-build-final.log`); native Windows remains unverified. Source resources were rebuilt. The sequential full desktop suite passes 39 tests with no skips (`.cache/instructions-evaluation-desktop-full-final.log`), and TUI passes 20 (`.cache/evaluation-runtime-tui-test.log`). No new DMG was built; the installed XvnHeb acceptance remains a separate immutable artifact.

One earlier source desktop suite reported backend exit before readiness without captured diagnostics; its exact cause is unconfirmed. The test fixture now retains bounded, credential-redacted startup stderr on failure. Subsequent focused and full native runs pass; these do not establish the cause of the earlier exit.

Final native visual follow-up also passes (`.cache/instructions-evaluation-native-final.log`): the 850px header controls no longer overlap, and the actual instruction/review/contract scenario still passes after the layout correction. Screenshot: `.cache/desktop-workspace-native-final.png`.

### Runtime presence and loaded-plan execution — 2026-10-22

COORD-07 remains partial, with actual runtime presence now implemented. Application-owned five-second snapshots use database-clock twenty-second expiry. The UI distinguishes idle, working, waiting for input and finishing an operation; unknown registration/expiry and multiple observers are explicit. Workspace inventory scopes reads, no client/model heartbeat mutation exists, and a presence lease does not imply provider health, delivery or exclusive execution. Database and API tests exercise transitions, independent heartbeat advancement, rollback, expiry, runtime removal and shutdown. Terminal/desktop local redraws age observations without successful refreshes. See `docs/coordination-presence.md`. Graph/assignment and full resolution controls remain unfinished.

The existing `/build` loaded-plan path now executes 1–100 phases through actual Run/Stream callers in one admitted turn, sharing goal accounting and total steps. Non-regular/oversized files are rejected before parsing (2 MiB maximum), and explicit filenames retain case/spaces. Phase completion follows configured confidence/verification policy; it is not independent test proof. Cancellation/provider failure/deferred confidence/step exhaustion halt progression and report unstarted phases truthfully. Completion callbacks fire once with accurate acceptance. Phase snapshots persist; cold in-flight work is unconfirmed, and later ordinary turns mark prior plans historical rather than silently resuming or masking new status. Desktop Checks and terminal evaluation views expose outcomes. Automatic execution checkpoints/resume, production red-team configuration and subsequent token/tool streaming remain open. See `docs/autonomous-plans.md`.

The native source flow observes backend Working then Runtime idle, applies actual workspace instructions and two-iteration review, executes a two-phase plan from a path with spaces, shows the final second-phase answer and reads persisted outcomes. Full sequential desktop acceptance passes 41 tests, zero skips/failures (`.cache/plans-presence-desktop-full.log`); a final focused native run passes after bounded-input loading (`.cache/plans-presence-native-final.log`). TUI typecheck/build and all 21 compiled HTTP/Ink tests pass (`.cache/plans-presence-tui.log`). Visual review uses `.cache/desktop-loaded-plan.png`; confidence/phase acceptance stays separate from recorded tests.

The initial full race run found an older fixture writing the agent map without its lock while the new presence reader was active. Fixture setup/read now uses the same mutex; three focused repetitions pass (`.cache/runtime-presence-fixture-race.log`). Final full required PostgreSQL/frontend/localdb race gate passes 105 packages plus nine no-test packages with zero failures/race reports (`.cache/plans-presence-full-race-final.log`). Vet and Windows source cross-build pass (`.cache/plans-presence-vet-final.log`, `.cache/plans-presence-windows-build-final.log`); native Windows is unverified. Original audit totals remain **97 repaired /21 open**. Source backend resources were rebuilt; the frozen XvnHeb DMG was not.

Hermes SDK WebSocket follow-up traces seven downstream contracts with 24 independently verified SHA/span excerpts: ticket mint/atomic consumption, real gated credential alternatives, plugin consent/profile routing, shared Kanban storage and reconnect/poll/cleanup boundaries. Combined ledger checks total 691, with canonical/original/supplemental/unresolved counts unchanged; both broad Hermes groups remain unresolved. See `docs/harness-research/hermes-sdk-websocket-followup.md`. Displaced bounded coordination anchors were re-read across the existing reports without parity upgrades. Full catalogue validates 8,667 original IDs, 19,200 overlapping normalized units and 3,424 evidence files, zero errors (`.cache/plans-presence-catalogue-final.log`).

### Shared managed database startup and OpenCode update tracing — 2026-10-22

BUILD-03 remains partial: `run`, `tui`, `serve`, and `daemon start` now resolve DATABASE_URL before configured URL before explicit managed mode. The shared owner initializes/migrates installed PostgreSQL, cancels dependent work on child failure, drains application resources before closing its pool and database, and preserves its cluster. Missing binaries fail explicitly. Saved and invocation-only settings produce accurate warnings without mutating saved configuration. See [managed local database](docs/local-database.md).

Actual built `serve` acceptance starts from an empty profile, writes and reloads a row, exits zero and reaps PostgreSQL on SIGTERM, exits nonzero on database death, and crash-recovers on the next launch (`.cache/managed-db-serve-smoke.log`). Native owner/config/command race cases pass (`.cache/managed-db-focused-final.log`). Full required PostgreSQL/frontend/localdb race acceptance passes **106 packages**, nine no-test packages, zero failures/race reports (`.cache/managed-db-full-race.log`). The final warning-only source change has focused acceptance after that full run. Portable binary installation and actual TUI/daemon/agent process scenarios remain open. Original audit totals stay **97 repaired /21 open**.

OpenCode config.update is now traced through actual default CLI policy projection, updater loop, manual bypasses and installation/TUI consumers. Seven scoped subcontracts and 19 independently rechecked exact source spans preserve existing canonical IDs and parity assessments; ten originals remain unresolved. See `docs/harness-research/opencode-update-followup.md` and `.cache/opencode-update-root-checks.json`. The displaced CLI automatic-memory anchor and its normalized copy were re-read after startup changes, without upgrading parity. Catalogue validation has zero errors: 8,667 originals, 19,200 overlapping normalized units and 3,427 evidence files (`.cache/managed-db-opencode-catalogue-final.log`). No desktop UI or frozen DMG was rebuilt; source test acceptance does not extend the installed artifact's scope.

### Durable execution prerequisites and application graph views — 2026-10-22

COORD-05 and COORD-07 remain partial. Request messages can now name up to 32 immutable, existing prerequisite request IDs in their project; the authenticated sender must participate in each. A request waits until all prerequisites complete while unrelated eligible work can proceed. Failed/cancelled prerequisites cause a durable failed event and review escalation without a provider call or execution attempt; cascade processing occurs on recipient claims, including after runtime reconstruction, and waits while that peer is busy. Original absent/empty-dependency idempotency envelopes remain compatible. See [durable request dependencies](docs/coordination-dependencies.md).

Agent tools expose `agent_send.depends_on` and `agent_task_graph`; authenticated session RPC exposes `coordination.graph`. Graph snapshots omit message payloads, cap visible ancestry and disclose only prerequisite ID/state scheduling metadata for inaccessible referenced nodes. Unknown graph roots and identity forgery are rejected. Desktop and TUI expose prerequisite drafts and graph inspection; retry drafts retain original prerequisites alongside budget/conversation scope. Late responses cannot populate another selected task or chat. This is execution scheduling for coordination requests; worktree lifecycle, ownership assignment, contract-readiness gates, mutable graph editing, reverse/project graph discovery and review resolution remain separate open work.

Actual native desktop acceptance holds the backend provider, sends a second dependent request through the visible composer, verifies its waiting graph and unchanged provider-call count, then observes execution/completion after the prerequisite finishes. Screenshot `.cache/desktop-task-dependencies.png` was visually inspected. All **43 source desktop tests** pass with zero skips (`.cache/dependencies-desktop-full.log`), including independent native encrypted-settings and recovery acceptance. TUI typecheck/build and **25 compiled tests** pass (`.cache/tui-dependencies-final.log`). Full required PostgreSQL/frontend/localdb Go race acceptance passes **106 packages**, nine no-test packages, zero failures/race reports (`.cache/dependencies-full-race.log`); the final graph operational-error propagation correction has separate focused race acceptance (`.cache/request-dependencies-focused-final.log`). Original audit totals stay **97 repaired /21 open**.

The OpenCode experimental namespace now has all five current schema children traced through actual configuration and consumers, captured as six scoped subcontracts and 26 exact independently rechecked SHA/span anchors. Original unresolved groups decrease from ten to nine; canonical IDs, full original partition and Overkill parity assessments remain unchanged. See `docs/harness-research/opencode-experimental-followup.md` and `.cache/opencode-experimental-root-checks.json`. Catalogue validation has zero errors: **8,667 originals, 19,200 overlapping units and 3,431 evidence files** (`.cache/dependencies-catalogue.log`). This remains a finite static source comparison, not exhaustive runtime parity. No DMG was rebuilt.

### Startup identity correction found during final native acceptance — 2026-10-22

The full 43-test desktop suite passed, then a final rebuilt-backend follow-up timed out during settings restart even though PostgreSQL reported accepting connections (`.cache/dependencies-native-current.log`). Inspection established that the manager incorrectly equated PostgreSQL's early PID-file timestamp with its separately assigned SQL startup timestamp. The exact failure attribution is inferred because the old readiness error discarded the last verifier error; the underlying comparison defect is proven by pinned PostgreSQL17.11 source. See [identity verification](docs/local-database-identity.md).

Readiness and orphan verification now authenticate the server's own bounded PID file through SQL and require exact PID/start/data-directory/port agreement. Timeout errors retain the last check with the private password redacted. Native database tests cover valid/mismatched identities, restart and orphan recovery (`.cache/localdb-pid-identity-final.log`); the identity test passes 20 race-enabled repetitions including cleanup (`.cache/localdb-pid-identity-repeat20.log`). An initial broad run compiled before the fixture's explicit close/Stop cleanup correction and failed only that cleanup (`.cache/dependencies-pid-full-race-final.log`); its failure is retained. The rebuilt current backend passes the native desktop workflow including settings restart, dependencies and loaded plans (`.cache/dependencies-pid-native-final.log`). Source resources are refreshed; the frozen DMG remains unchanged.

Final frozen-source acceptance passes all **106 Go race packages**, nine no-test packages, zero failures/race reports, with PostgreSQL/frontend/localdb mandatory (`.cache/dependencies-pid-full-race-current.log`). Final vet and Windows amd64 source build pass (`.cache/dependencies-pid-vet-final.log`, `.cache/dependencies-pid-windows-final.log`); native Windows remains unverified. Current source backend resources and the native desktop restart/dependency/plan scenario pass after the identity correction. Full desktop suite remains 43 passing source tests before that backend-only correction, followed by the final current-backend native pass; TUI remains 25 passing tests with typecheck/build. Final catalogue is clean (`.cache/dependencies-pid-catalogue-final.log`): 8,667 originals, 19,200 overlapping units and 3,431 evidence files. Audit remains 97 repaired /21 open; the accepted development DMG is unchanged.

### Required agreement revisions across request execution — 2026-10-22

COORD-05 and COORD-07 remain partial. Requests now persist up to 32 immutable required contract/revision pins. Only current same-project pins can be submitted; omitted/empty pins preserve legacy receipt hashes. Missing recipient or recursive acknowledgements keep accepted requests waiting while independent ready work proceeds. Deleted/stale pins, including changed transitive dependencies, fail with recorded blockers and review escalations. Private causal lineage carries the union of requirements into child requests, notifications and answers; children cannot omit or override parent revisions. New causal work is fenced when the parent agreement is invalid; identical prior receipts remain retrievable. Each recipient acknowledges inherited agreements independently. See [request contract gates](docs/coordination-request-contracts.md).

Actual guarded file/opaque effects recheck the private executing message, lease, required revisions and recursive acknowledgement while holding the existing project contract lock. The API-owned worker installs a cancellation callback for typed agreement rejection, stopping further iterations after that gate error. No background watcher cancels a provider solely because a contract changes during inference; validation occurs at guards and finalization. Completion, agent replies, explicit state transitions and recovery resume all enforce agreement state. Dispatcher reconciliation runs after success/error/cancellation and preserves an already blocked/failed gate decision, without duplicate escalation or a success answer. Prior effects are not undone; the guard is cooperative application admission, not an OS sandbox or worktree manager.

Real PostgreSQL coverage includes acknowledgement-to-dispatch, restart, independent ready work, stale/deleted/transitive pins, two-level cross-recipient inheritance, notification gates, immutable/legacy receipts, lease forgery, actual guarded callbacks and update-lock ordering. The actual dispatcher matrix covers missing acknowledgements/disabled owner/stale revision against success/guard error/cancellation, plus a consumed agent-reply gate error (`.cache/request-contract-inheritance-race.log`). Root API/tool tests exercise session-scoped send/graph/ack and an actual dispatcher-owned file write rejected with cancellation and no artifact (`.cache/request-contracts-api-tools-final.log`). Full required PostgreSQL/frontend/localdb Go race suite passes **106 packages**, nine no-test packages, zero failures/races (`.cache/request-contracts-full-race.log`); full vet and Windows amd64 source compilation pass (`.cache/request-contracts-vet.log`, `.cache/request-contracts-windows.log`). Native Windows remains unverified.

Desktop requests offer a named agreement picker with explicit revision choice; saved stale/unavailable pins are retained until deliberately changed. The fixed IPC accepts exact bounded pin fields, derives identity from the selected chat, and keeps repeated identical sends on their existing key. Picker loads and inspector responses ignore obsolete sessions/tasks. Both desktop and TUI retain and lock agreement pins in retry scope. The native desktop scenario selects Users API revision2, observes a queued acknowledgement blocker with no provider call, acknowledges from the recipient chat and verifies request completion. Screenshot `.cache/desktop-request-agreement-waiting.png` was visually inspected. An initial native run caught a missing exact script allowlist entry; it is corrected without broadening navigation/network access. The follow-up fixture also needed to select Tasks before waiting for an identity hidden by the preserved Contracts tab. The final fresh suite passes **47 tests, zero skips/failures** (`.cache/request-contracts-desktop-current.log`); TUI typecheck/build and **29 compiled tests** pass (`.cache/request-contract-gates-tui.log`).

OpenCode LSP follow-up traces configuration normalization, two distinct GUI merge/disabled rules, reload/error paths and the V2 empty-status placeholder. Twenty-eight independently reread SHA/span excerpts strengthen five original groups, which remain unresolved because current process/initialization/diagnostics execution linkage is unproven. Nine OpenCode originals remain unresolved; canonical IDs and Overkill parity assessments are unchanged. See `docs/harness-research/opencode-lsp-followup.md` and `.cache/opencode-lsp-root-checks.json`. Sixteen current execution-anchor copies were re-read after the bounded API cancellation insertion without upgrading parity (`.cache/request-contract-run-anchor-review.json`). The catalogue validates **8,667 originals, 19,200 overlapping units and 3,441 evidence files**, zero errors (`.cache/request-contracts-catalogue-current.log`). Original audit stays **97 repaired /21 open**. Source backend resources are refreshed; the frozen DMG was not rebuilt.

### 2026-10-22 repository scope and managed worktree checkpoint

Original audit remains **97 repaired /21 open**. COORD-05/07 remain partial:
new repository scope groups explicitly registered chats in a primary checkout
and linked worktrees, with session-derived authority and persisted directory
identities. Real Git/PostgreSQL tests cover cross-root requests, dependency and
agreement gates, restart, separate clones/default identities, invalid roots,
concurrent registration and replacement fencing. Invalid bindings do not block
unrelated dispatcher projects. Desktop/TUI expose the explicit scope choice;
existing workspace registrations cannot change scope.

Managed worktree allocation now uses hashed paths, durable ownership tokens and
cross-process locks. It preserves existing branches, unmerged commits, dirty and
ignored files, and unrelated Git locks. Force cleanup from automatic subagent
callers no longer discards work. This does not connect each request to automatic
worktree allocation, merging, branch review or task resolution. See
`docs/worktree-lifecycle.md` and `docs/coordination-repository-scope.md`.

Native workspace acceptance sends frontend requests to a backend in a linked
checkout, performs an actual backend tool write and confirms the primary
checkout lacks that file, then exercises shared agreement revisions/dependencies
and review/plan controls. The disposable fixture uses fake local credentials;
`native.test.cjs` separately exercises actual encryption/Keychain. The DMG has
not been rebuilt for this source checkpoint.

Validation: `.cache/repository-scope-full-race-current.log` passes all 106 packages
and nine no-test packages with required PostgreSQL/frontend/local database tests;
`.cache/repository-scope-vet.log` and `repository-scope-windows.log` pass. The first
unbounded-concurrency race run had one controlled-browser startup timeout; the
complete rerun uses `-p 4` without changing its assertions. TUI typecheck/build
and 30 tests pass in `.cache/repository-scope-tui-final.log`.

The final combined desktop run passed 49 of 50 checks and hit a retryable busy
history read in the remaining native fixture. After adding retries for only that
explicit admission response, the complete workspace scenario passes in
`.cache/repository-scope-workspace-native-accepted.log` (34.963 seconds). Thus all
50 desktop checks have current passing coverage across those runs; this is not a
claim that one combined invocation passed 50/50. The new contract-list loading
race also has a focused passing unit check in
`.cache/repository-scope-workspace-unit-final.log`. No native Windows, paid
provider, new installed DMG or notarization acceptance was added.

Research: `.cache/opencode-sharing-root-checks.json` independently verifies all
27 exact follow-up spans against pinned upstream file hashes. Catalogue validation
has zero errors, 8,667 original records, 19,200 overlapping normalized units and
3,445 evidence files. No parity statuses or original audit closures changed.

### 2026-10-22 committed-history checkpoint

Saved PostgreSQL history now reads through an optional `session.HistoryReader`
capability. Its single ordinary SQL read returns separately decoded committed
messages even while a turn holds admission or an UPDATE is uncommitted. The API
labels this `snapshot: "committed"`; other store implementations retain the
existing admission rule. Real database acceptance holds both a writer admission
and an uncommitted row update, reads the previous transcript without waiting,
commits, then reads the new transcript. Reader mutations cannot affect later
reads, and reading does not release the writer's admission. Missing sessions
remain explicit errors. Streamed/uncommitted worker tokens are outside this
snapshot contract.

Desktop Refresh chats refreshes the selected saved transcript too. Session
buttons now disable during an active selection or user turn rather than accept
and silently ignore another click. The native workspace fixture removes the
busy-history retry workaround and opens/refreshed a backend chat while its real
provider request is deliberately held, then continues cross-worktree writes,
contract dependencies, acknowledgements, budgets and plan checks.

Full required Go race suite passes 106 packages and nine no-test packages in
`.cache/committed-history-full-race.log`; vet and Windows source build pass.
Original checklist remains 97 repaired /21 open; these are bounded improvements
to COORD-07/08, not closure of those broader gates. The frozen DMG is unchanged.

Current full desktop acceptance now passes **50/50**, zero skips, in one combined
invocation: `.cache/committed-history-desktop-current.log`. This supersedes the
prior split-run/retry result. It includes actual encrypted settings, no-Keychain
responsiveness, committed history reads during a deliberately held worker call,
backend-only linked-worktree writes, shared contract authoring/acknowledgement,
request dependencies/budgets and loaded-plan/review views. The fixture's
visibility ordering was corrected to select the Tasks tab before waiting for its
identity element, and no busy-history retry helper remains.

### 2026-10-22 packaged TUI interaction checkpoint

The staged installed runtime (copied CLI, bundled Node, compiled `dist` and
production-only npm dependencies, without `src` or `tsx`) boots from an unrelated
workspace with an isolated profile/schema. Its real PTY acceptance filters the
command palette, opens keyboard help, then requires zero exit and no surviving
owned process group. This exposed and repaired a conditional React hook in the
palette's highlight helper. The accent now comes from the component's single
unconditional hook; the helper is pure. A real Ink regression changes filtered
row/highlight counts and executes the selected action.

Evidence: `.cache/packaged-tui-acceptance.log`,
`.cache/packaged-tui-final-checks.log` (typecheck/build and 31 compiled tests),
`docs/packaged-tui-acceptance.md`. The isolated acceptance schema was removed.
Root independently inspected the staged bundle (no source or development TS
runtime) and validated Python/shell syntax. Docker's gate now includes this real
PTY interaction, but no Docker/Podman/Colima/Lima runtime exists here; BUILD-02/04
remain open for actual container/full-distribution acceptance. The terminal help
layout at 140×50 exposes its initial rows; this is no full layout-polish claim.
Original audit remains 97 repaired /21 open. No new DMG was built.

OpenCode follow-up evidence is independently checked in
`.cache/opencode-lsp-runtime-root-checks.json`: 17 exact excerpts and whole-file
hashes. Actual registered write/edit paths explicitly defer LSP integration;
none of the five unresolved LSP groups, nine unresolved originals or parity
assessments was promoted.

### 2026-10-22 request review and migration checkpoint

Original checklist remains **97 repaired /21 open**. Trusted application review
now lets a request participant who owns the task budget explicitly change
limits and queue a blocked request again. Both request and usage revisions are
checked; a durable actor/review-ID receipt makes ambiguous identical retries
idempotent. Review records its note/limits without erasing usage, uncertainty,
dependencies, agreement pins, deadline, lineage or attempts. Live workers,
unfinished/unknown accounting under relevant limits, exhausted attempts and
expired requests still refuse review. Legacy transitions now use the same
project admission order. No model tool exposes the review control.

Desktop and terminal require explicit limits, a note and replay confirmation;
manual retry freezes the exact original body/UUID. New chat selection fences
late responses. Native acceptance stops on a tiny task budget, reviews that same
request, replays it once, observes completion/reviewed event, and proves earlier
plus cross-peer usage remains counted. Historical escalation cards no longer
claim a past trigger is the current state. See
`docs/coordination-request-review.md` and `docs/tui-request-review.md`.

Cost migration now fails closed in one schema-serialized transaction. Ten new
real PostgreSQL cases cover legacy session/cost/memory/coordination/callback
shapes, partial migration failure and recovery, and startup contention. They do
not close VALIDATE-01's complete historical/permission/process matrix. The
broader task graph, uncertain-journal resolution, merge/review lifecycle and
paid-provider scenarios also remain open under COORD-05/06/07/08.

Current gates: required full Go race **107 packages**, eight no-test packages,
zero failures/races (`.cache/request-review-full-race.log`); vet and Windows
source build pass; all **55 desktop** checks pass in one combined invocation
(`.cache/request-review-desktop-current.log`); TUI typecheck/build and **37 tests**
pass (`.cache/tui-request-review-final.log`). Focused API/tools/cost/schema gates
also pass. No new DMG has been built.

Hermes catalog follow-up adds eight scoped contracts and 32 independently
verified exact spans (`.cache/hermes-catalog-root-checks.json`), retaining both
broad unresolved groups and all parity assessments. An unchanged budget-send
function moved one line when owner metadata was added; root inspected it and
refreshed 16 exact current evidence copies across ten files, preserving
historical/previous evidence and assessments
(`.cache/request-review-usage-anchor-checks.json`). Catalogue: 8,667 originals,
19,200 overlapping units, 3,455 evidence files, zero errors.

Final visual/source checkpoint: all **56 desktop** checks pass in one invocation
with zero skips (`.cache/request-review-desktop-accepted.log`), superseding the
55-check checkpoint. The extra assertion prevents incomplete pricing from
appearing as a known $0 in the review summary; the form explicitly states shared
scope impact. Root visually inspected `.cache/desktop-request-review-form.png`
and `.cache/desktop-reviewed-request.png`. A repeated token-limit escalation
required selecting the specific request's recorded history card, rather than a
nonunique reason string; the fixture now checks the correlated message ID.
These were UI/test refinements; the backend's 107-package race/vet/Windows gates
and terminal's 37 tests remain current.

### 2026-10-22 startup safety, readable history and research explorer

Original audit remains **97 repaired /21 open**. Learning store concurrent
startup reproduced PostgreSQL catalog duplicate-key failure; cron's late index
failure reproduced a partly committed jobs table. Both constructors now use
bounded transactions with schema/store advisory locking. Seven new tests cover
16/8 concurrent constructors, cron rollback preserving legacy log output,
explicit repair/retry, held migration locks/deadlines, and nil pools.
`.cache/store-migration-safety-final.jsonl` passes 110 tests/subtests with zero
skips; `docs/store-migration-safety.md` preserves the remaining global/historical
and unchanged daemon/memory boundaries.

All five legacy Telegram/Discord/Signal/Matrix/Mattermost command paths now fail
on a configured database or cost-tracker initialization error instead of
continuing without configured usage storage. Deliberate no-database startup
remains. Twenty actual RunE subprocess cases cover malformed/healthy cost
schemas, no database and malformed URLs, including live pool-cleanup assertions:
`.cache/gateway-cost-startup-race.log`. These legacy dispatcher cost trackers
serve usage reads and do not establish spending-cap enforcement.

Desktop saved coordination history now shows kind, payload, current scoped
names/route and task, with a snapshot caption and preserved original-text
expansion. Inspect uses the authenticated participant API. Malformed envelopes,
unknown kinds and oversized text remain ordinary messages; HTML is rendered as
text. Formatting grants no authority. Protocol-generated titles become readable
agent-workspace titles without rewriting stored history. Native acceptance
opens the real backend transcript, checks route/payload/original disclosure and
current task inspection. Root visually inspected
`.cache/desktop-coordination-history.png`. Details:
`docs/coordination-history-view.md`.

Full current source gates: **107 Go race packages**, eight no-test packages,
zero failures/races (`.cache/startup-history-full-race.log`); vet and Windows
source cross-build pass; **61 desktop checks** pass in one invocation, zero
skips (`.cache/startup-history-desktop-current.log`). TUI source is unchanged
from the passing 37-test gate. Development resources were rebuilt; no DMG was
rebuilt and no new platform/provider acceptance is claimed.

Research now has a generated self-contained offline explorer with separate
original/normalized inventories, harness/parity/upstream/freshness/category
filters, search, pagination and source/evidence details. All **27,867 indexed
records** independently match the current catalogue; overlapping IDs in different
reports retain separate identities. Six generation/security/atomicity tests
pass. Actual localhost browser filtering/detail/pagination has acceptance;
direct file-URL execution is unverified because the browser tool blocks it.
The viewer does not infer parity or prove semantic completeness. HTML is kept
reviewable with one narrow .gitignore exception. Evidence:
`.cache/harness-explorer-root-validation.json`,
`.cache/harness-explorer-proof.png`, `docs/harness-research/explorer.md`.

## Current development image and daemon migration checkpoint — 2026-10-22

Original audit disposition remains **97 repaired /21 open** (12 P1, 9 P2). Daemon job-store startup now serializes same-schema constructors with a bounded advisory lock and wraps table/index creation in one transaction. Sixteen prewarmed constructors reproduced a PostgreSQL catalog race before the fix. Afterward, 25 daemon tests and ten concurrency repetitions pass, covering late-DDL rollback, preserved incompatible data, explicit repair/retry and nil pools. This does not close the full historical/platform migration matrix. See `docs/daemon-migration-safety.md`.

The current frozen macOS arm64 development DMG is `.cache/distribution/overkill-0.1.0-dev-arm64-sCqj16/Overkill-0.1.0-development-arm64.dmg`, SHA-256 `9b09d835f6713d3face3c8d749e0a2477ab6c3cf57955440d9350d676f33f7b8`. Exact installed bytes passed a bounded fake-storage smoke: actual backend/database, file editing, stream cancellation, frontend/backend agent exchange, same-version replacement, persisted history with explicit reconnect and database crash recovery. Its report deliberately retains real installed Keychain acceptance **pending**; no genuine-encryption or automatic credential-startup acceptance transfers from the old image. The old XvnHeb image and successful actual-Keychain report remain unchanged. No Developer ID/notarization or public distribution readiness is claimed. Details: `desktop/INSTALLED_ACCEPTANCE.md`.

Current source gates: **107 Go race packages**, eight no-test packages, zero failures; vet and Windows cross-build pass; **64 mandatory desktop tests** pass with zero skips, including default real-Keychain source acceptance and three smoke refusal checks. Logs: `.cache/final-dmg-full-race.log`, `.cache/final-dmg-vet.log`, `.cache/final-dmg-windows.log`, `.cache/final-dmg-desktop.log`. Windows execution and minimum-macOS execution remain unverified. Unchanged terminal source retains its prior 37-test gate.

Research integration for this checkpoint records **27 original assessments** (9 partial, 10 missing, 8 not-applicable), plus 18 matching normalized units. There are zero verified upgrades. Explicit original assessments now reach catalogue/explorer with their own provenance; accepted parity-package precedence is retained. All 52 exact source/evidence spans and file hashes were independently reread by root, and every catalogue field of all 27,867 explorer rows was compared after regeneration. Catalogue validation has zero errors; 21 catalogue/normalization/explorer tests pass. Total remains 8,667 originals and 19,200 overlapping normalized units, with 8,359 originals unassessed. `docs/harness-research/semantic-comparison-followup.json` records the reviewed contracts and bounded differences. Logs: `.cache/semantic-comparison-root-checks.json`, `.cache/semantic-comparison-root-tests.log`, `.cache/harness-explorer-root-validation.json`. No exhaustive parity claim.

## Atomic bootstrap, memory migration and browser auth follow-up — 2026-10-22

Central bootstrap previously committed suspension/routine upgrades before a later central-table failure, and concurrent bootstraps reproduced a catalog creation race. The shared schema helper now supports a single transaction spanning these definitions and central tables, with a fixed owner-lock order shared with standalone stores. Actual PostgreSQL tests prove rollback of earlier upgrades/tables, preserved legacy approval state/routine steps/bookmark values, explicit repair/retry, held-lock deadline/retry, sixteen concurrent bootstraps and eighteen mixed central/standalone constructors. Both concurrency scenarios passed ten repetitions. Memory startup separately reproduced partial hash-column publication and a concurrent catalog race, then gained a bounded transactional migration with selected data-preservation/duplicate-hash/deadline/retry/concurrency acceptance. Neither repair claims a global transaction across every application store or all historical upgrade shapes. Details: `docs/bootstrap-migration-safety.md`, `docs/memory-migration-safety.md`.

WEB-02 reinspection found the SPA's authenticated WebSocket used a query token that the server rejected, and a differently named script-readable cookie. Browser login now exchanges an explicit bearer for an HttpOnly SameSite=Strict cookie at `/api`, Secure under TLS. Query tokens do not authenticate or mint cookies; old root-path cookies are removed. Same-origin checks guard cookie mutations and WebSocket upgrade. Actual Chromium acceptance proves login/event delivery/reload/logout and rejects missing/query credentials; HTTP assertions cover wrong bearer despite a valid ambient cookie, origin rejection and TLS cookie properties. The full web race suite passes with mandatory browser acceptance in `.cache/web-browser-cookie-auth-final.log`. WEB-02 remains open because retained canvas state/rendering and real snapshots are still absent; `docs/canvas-snapshot-reinspection.md` records the concrete next implementation contract.

Five additional Codex instruction originals and five matching normalized units now have explicit reviewed assessments: two partial and three missing, with zero verified upgrades. All 35 exact evidence spans and hashes were independently checked by root. Catalogue/explorer retain 8,667 originals /19,200 overlapping normalized units and zero errors; all 27,867 viewer rows match catalogue fields. 8,354 originals remain unassessed. Existing broad capability assessments were not inherited or upgraded. Details: `docs/harness-research/semantic-instructions-followup.json`; root evidence: `.cache/semantic-instructions-root-checks.json`, `.cache/semantic-instructions-root-tests.log`, `.cache/harness-explorer-root-validation.json`.

Aggregate follow-up passed: **107 Go race packages /eight no-test packages**, zero failures/races (`.cache/bootstrap-memory-browser-full-race.log`); vet and Windows cross-compilation passed; **64 desktop checks** passed with zero skips after rebuilding current development resources (`.cache/bootstrap-memory-browser-desktop.log`). The research builder suites passed 21 tests. No new DMG was created for these final source changes; the previously reported sCqj16 image remains frozen and its acceptance does not expand. Original audit disposition remains 97 repaired /21 open.

## WEB-02 repair: durable rendered canvas and real snapshots — 2026-10-22

WEB-02 is repaired for the explicitly declared **overkill.canvas.v1** catalog. It reuses the prior event action names without claiming OpenClaw A2UI protocol/catalog compatibility. Strict component/action validation rejects unknown/duplicate fields, invalid references, cycles, unsupported catalogs and bounded-resource violations. Per-session PostgreSQL state and monotonic JavaScript-safe revisions commit before event publication; invalid updates leave prior data/revision unchanged. The model, browser panel and snapshot route support Text/Heading/Column/Row/Divider and safe scalar bindings. Actual Chromium uses the same embedded renderer/CSS for DOM and PNG output; snapshots receive a frozen committed state, bounded viewport, concurrency slots and deadlines, with cancellation on request/server shutdown. They receive no application credentials and no caller-selected URL or script. Missing browser/runtime returns an actionable error.

Real PostgreSQL/Chromium acceptance covers create/update/bind/begin/delete/reset, authenticated browser events and state, safe literal injection handling, invalid-update rollback, session isolation, reload, concurrent writes during capture, actual PNG decode/dimensions/content changes, and exact revision-to-image consistency. An owned-process startup fixture proves shutdown cancellation/reaping; it is not fake PNG evidence. Root also reproduced an accepted-nesting boundary bug, then fixed final-state validation before persistence and verified rejected input preserves readable prior state. Browser-safe numeric and maximum-revision fixtures prevent unrenderable commits. The full mandatory web race suite passed **107 tests/subtests, zero skips** (`.cache/canvas-full-web.jsonl`); all-source vet and Windows cross-compilation passed. Root visually inspected `.cache/canvas-snapshot-proof.png`. Contract/examples/limits: `docs/canvas.md`.

This closes the original strict-validation/snapshot criterion for the documented supported catalog. It does not close full upstream A2UI/widgets/native-device parity, other audit items, all-platform/runtime packaging, paid-provider acceptance or release signing. The frozen earlier DMG excludes these changes. Original audit totals are now **98 repaired /20 open** (12 P1, 8 P2); historical checkpoints above retain their recorded totals.

Final canvas aggregate checks passed: **107 Go race packages /eight no-test packages**, zero failures/races (`.cache/canvas-full-race.log`); all-source vet and Windows cross-compilation passed; **64 mandatory desktop checks** passed with zero skips after current resources were rebuilt (`.cache/canvas-desktop-tests.log`). Root visually inspected the actual integrated browser view in `.cache/canvas-workspace-proof.png` as well as the PNG export. No new DMG was produced; source/browser acceptance does not expand the frozen image's scope.

Research follow-up integrates **14 original OpenClaw canvas records** (two partial, eleven missing, one documentation container not-applicable) and **49 independently assessed normalized units** (all missing). No verified upgrade or full A2UI/widget/device parity is inferred from the custom catalog. The JSON report preserves immutable pre-implementation evidence and initial failed-build receipt separately from final production evidence. Root independently reread all 47 current exact source spans/hashes and compared every catalogue field of all 27,867 explorer rows. Catalogue validation has zero errors and all 21 research tests pass. Total remains 8,667 originals /19,200 overlapping normalized units; 8,340 originals remain unassessed. Evidence: `docs/harness-research/semantic-canvas-followup.json`, `.cache/semantic-canvas-root-checks.json`, `.cache/semantic-canvas-root-tests.log`, `.cache/harness-explorer-root-validation.json`.


### Durable external tasks and native model canvas — 2026-10-22

ACP-04 now has executed acceptance for immediate durable handles and separate observation/cancellation. Named application-owned peers keep endpoints/credentials out of model inputs; strict adapters refuse redirects. Identical retries preserve receipts after disconnect, restart and session deletion. Actual frontend/backend CLI processes demonstrate independent frontend file work while the backend runs, then retrieve its reply after process restart. See `docs/acp-durable-tasks.md` and `.cache/acp-durable-two-process.log`. Broader COORD audit items remain open; external budgets, automatic safe-turn delivery and comprehensive project credentials are not implied.

The initial full suite found a real shutdown finalization failure. A deterministic database trigger blocks a progress checkpoint; cancelling it closes the ownership connection and previously left the task running after successful Shutdown. The repair discards that connection, reacquires the task lock and records interruption using only durable checkpoints. Existing terminal results and another live owner are preserved. If finalization cannot be confirmed, Shutdown returns an error. The regression fails before the repair (`.cache/acp-blocked-checkpoint-before.log`); 60 focused repetitions and all 108 PostgreSQL ACP tests/subtests pass after it (`.cache/acp-shutdown-repair-repeat.log`, `.cache/acp-shutdown-repair-final.jsonl`). No timeout was increased and no interrupted work is replayed.

The session-bound canvas model tool now reaches the shared PostgreSQL store and native desktop panel. Read/write privilege, child scope and coordination agreement guards remain enforced. The actual controlled provider produces a surface, automatic refresh displays it, bundled Electron exports real 960×640 pixels and app/database relaunch preserves it. Export uses a frozen revision, constrained hidden renderer, native path choice and atomic destination replacement; old session/revision responses cannot replace the selected chat. A polling race that erased export feedback was fixed. See `docs/desktop-canvas.md`, `docs/desktop-canvas-plan.md`, `.cache/canvas-producer-native-final.log` and `.cache/desktop-canvas-native.png`. This remains the custom overkill.canvas.v1 catalog, not full upstream A2UI/widgets.

Final current-source gates pass: 107 Go race packages plus eight no-test packages with required real PostgreSQL/frontend/native localdb (`.cache/async-canvas-full-race-final.log`); vet and Windows amd64 source compilation (`.cache/async-canvas-vet-final.log`, `.cache/async-canvas-windows-final.log`); all 68 mandatory sequential desktop tests, zero skips (`.cache/async-canvas-desktop-final.log`). Resources were rebuilt before the final desktop run; dependency inventory updates only backend/manifest hashes. Native Windows and paid-provider behavior remain unverified.

The async research follow-up preserves immutable BEFORE history and integrates 22 Hermes originals plus 18 exactly matching normalized units. Root independently re-read 63 exact source spans/hashes and compared all 27,867 catalogue/explorer rows field by field (`.cache/semantic-async-root-checks.json`, `.cache/harness-explorer-root-validation.json`). All 21 builder tests pass; catalogue SHA is a91f13b2d938b56a9429b274ac1b34c88db66ec475656d567814d94287a8a0f5, zero validation errors, 8,318 originals unassessed. Only unchanged historical excerpts were relocated; stale certifications were not silently renewed and no feature was upgraded to verified. Original audit now records **99 repaired /19 open** (12 P1, 7 P2). The existing DMG and installed copy were not rebuilt or modified; new source acceptance is separate from installed-app encryption/distribution acceptance.


### Usage reconciliation, shared migration safety and refreshed DMG — 2026-10-22

Budget owners now discover uncertain message metadata throughout an owned task, including delegated children whose payloads remain private. They can attest full-message/all-attempt accounting with explicit evidence, reason, basis, acknowledgement, exact revisions/fingerprint and a UUID receipt. Original samples/journals remain immutable. Observed/prior-attested token and known-cost floors remain enforced; conservative estimates also preserve current estimated cost. Other messages' uncertainty and exhausted limits remain blockers. Reconciliation changes accounting/audit revision, never task state or replay. The desktop and terminal retain ambiguous retries on the exact envelope, distinguish definite stale conflicts and refresh current totals after historical receipts. See `docs/coordination-usage-reconciliation.md` and `docs/coordination-usage-reconciliation-ui.md`.

Root review caught an in-flight renewal race: a committed-old expired lease could be observed while its renewal was uncommitted. Mutation now locks the lease row before checking eligibility; a real held-renewal fixture proves waiting, then rejection with no audit/delta after renewal commits. Replays also receive fresh dispatcher execution owner IDs; a closed/resolved journal cannot be reused. The actual same dispatcher is tested through lost usage, manual reconciliation, explicit review/replay and new unconfirmed usage. Final required PostgreSQL coordination suite passes **134 tests/subtests**, zero skips; eleven new focused cases include full evidence beyond bounded previews, hidden-child privacy, concurrent receipts, known floors, sample-only uncertainty and injected final-audit rollback (`.cache/usage-reconciliation-final.jsonl`). Native provider-omission acceptance retains manual provenance after app/database relaunch and makes no second provider call (`.cache/usage-reconciliation-native.log`). Both native evidence/receipt screenshots were visually inspected.

Gateway bookmark/history/delivery startup and central bootstrap now share bounded transactional owner locks/definitions where applicable. Goals share their column/identity/time backfill and journal migration with bootstrap; independent daemon flow state has its own bounded owner transaction. Actual pre-fix tests reproduce bookmark late-index partial DDL, goal late-backfill partial DDL and concurrent table-type collisions. Explicit repair/retry, original data/identity/time preservation, missing pools and held-lock cancellation are verified. Ten repetitions per root lane pass; warmed-pool flow startup reproduces the collision in three pre-fix runs and passes ten after repair. See `docs/gateway-migration-safety.md`, `docs/goal-migration-safety.md`, `docs/flow-migration-safety.md` and `.cache/{gateway,goal,flow}-migration-{before,repeat}.log`. The broader historical database gate remains open.

The initial aggregate exposed two fixtures needing correction. The bridge capacity scenario randomly mapped its held streams to the same intentional serialization stripe; a forced-collision overlay reproduces the exact timeout, while fixed distinct bindings pass fifty repetitions without increasing timeouts (`.cache/bridge-capacity-collision-repro.log`, `.cache/bridge-owned-capacity-repeat.log`). The API child-lineage fixture was changing during the first aggregate; its precise compiled failure cause is unproven. The final fixture preserves genuine dispatcher ancestry, removes an invalid different-recipient reply link, reports dispatcher errors immediately and joins cleanup. Thirty focused repeats and two full API race runs pass. These do not certify a cause absent from the first aggregate log.

Final current source passes **107 Go race packages** plus eight no-test packages with real PostgreSQL/frontend/native localdb required (`.cache/usage-recovery-full-race-final.log`), all-source vet and Windows amd64 source compilation (`.cache/usage-recovery-vet.log`, `.cache/usage-recovery-windows.log`), **74 mandatory desktop tests** with zero skips (`.cache/usage-recovery-desktop.log`) and **40 compiled terminal tests** (`.cache/usage-recovery-tui.log`). TUI typecheck/build and focused transport/native/Ink gates pass. New resources precede native acceptance and packaging; dependency inventory updates only backend/manifest bytes and hashes. Native Windows and paid-provider behavior remain unverified.

Research integrates 55 originals (19 partial, 30 missing, one unknown, five not-applicable) and 57 narrower normalized units (seven partial, 49 missing, one unknown), retaining the shared compaction unit and all immutable BEFORE history. No verified upgrade is claimed. Root independently validates 123 exact source spans/hashes and all 27,867 catalogue/explorer rows (`.cache/semantic-usage-recovery-root-checks.json`, `.cache/harness-explorer-root-validation.json`). All 21 research tests pass. Catalogue SHA is 921f4f2348d6efca30c4c951eb9b0d5460d3f6b7f5a3d661f44ada51d5071724, with 8,667 originals/19,200 overlapping units and zero errors; 8,263 originals remain unassessed. Exact unrelated excerpt relocations preserve historical certification hashes/statuses rather than silently recertifying them.

A fresh development image now includes current tested backend/desktop sources: `.cache/distribution/overkill-0.1.0-dev-arm64-W2ZgYd/Overkill-0.1.0-development-arm64.dmg`, SHA-256 `57538132cadf11cb3d9cbf29dbd231e59e6b4552084f3ecf11f84434c0f78d8e`. Mounted-image installation to a path with spaces passes the bounded app/tool/cancel/peer/replacement/relaunch/database-crash smoke using fake-only credential storage (`.cache/usage-recovery-installed-smoke.log`). Root verifies image identity, all 25 packaged desktop source files and signed backend-manifest checksum (`.cache/usage-recovery-artifact-root-checks.json`). No real Keychain acceptance, minimum-macOS acceptance, Developer ID/notarization, cross-version migration or public distribution readiness is claimed. The earlier encrypted accepted image/installed copy remain untouched. Original audit stays **99 repaired /19 open**; no whole COORD-06 completion is claimed.


### Executed container, installation and PostgreSQL gate closure — 2026-10-22

Closed the original BUILD-02/03/04, INSTALL-01/02 and VALIDATE-01 criteria after
reviewing their original text and actual executed assertions. Current count:
**105 repaired / 13 open (7 P1, 6 P2)**. This does not close the separate native
multiplatform, live-provider or complete interactive-task release gates.

- Actual clean Linux/arm64 Docker image build, fresh Compose PostgreSQL/readiness,
  non-root packaged Node/Ink/Python imports, config validation, interactive PTY and
  positive/negative API authentication pass; final source rerun also passes.
  See [container evidence](docs/docker-runtime-acceptance.md).
- Twenty installer/release fixtures pass, with exact release target names and
  mandatory checksums. Fresh-home source and prebuilt native installs validate
  application-schema configuration; packaged CLI frontend runs from an unrelated
  cwd with bundled Node/prod dependencies and a fresh owned local PostgreSQL
  profile. Required Windows CI now invokes its native fixture; its execution is
  not inferred from this Mac. See [installer evidence](docs/installer-release-acceptance.md).
- Real PostgreSQL race gate: ten packages, 1,094 named checks, zero skips/failures;
  explicit source-bound historical DDL, preserved old rows and all originally
  enumerated lifecycle/failure clauses. The former broader “every archived
  schema/permission/process” checkpoint wording exceeds the original criterion;
  those remain documented limits. See [clause mapping](docs/postgres-validation-acceptance.md).
- Retention’s reproduced child-crowding/list-window bug is repaired. The helper
  has no production caller; this proves helper behavior, not enabled automatic
  maintenance. CLI updater now fails closed on missing hashes/wrong architecture,
  bounds trusted release downloads and preserves the live executable on failed
  publication. Desktop bundles reject CLI self-update/rollback. Snapshots now
  honor the selected profile and preserve repeated exports; see
  [profile isolation](docs/snapshot-profile-isolation.md).

Source checkpoint after those repairs: 107 Go race packages pass (eight no-test
packages), vet, Windows/amd64 source build, 75 mandatory desktop checks and
40 terminal tests pass. New XqJEnw arm64 development DMG matches all25 current
desktop source files and its signed backend manifest. Two installed fake-only
storage runs pass file-edit/cancel/peer exchange/same-version replacement/relaunch
and database crash recovery. Earlier screenshot/empty-key setup failures are
retained; a separately reproduced late settings-read draft overwrite is fixed,
but no unique cause for every native intermittent failure is asserted. See
[settings evidence](docs/desktop-settings-race.md). Real installed Keychain,
Developer ID/notarization, minimum macOS and full release acceptance remain open.


### Authenticated peers and interactive desktop acceptance — 2026-10-22

Original COORD-01/02 and VALIDATE-02 are closed after clause-level review: **108 repaired /10 open (4 P1, 6 P2)**. Scoped ACP credentials derive sender identity from private credentials, persist endpoint/capability metadata within existing project bindings, and expose bounded peer messaging without global-token fallback. Actual independent ACP process restart retains correlated correspondence and refuses duplicate execution; manual interrupted work requires explicit recovery. See [peer ingress](docs/coordination-peer-ingress.md). Remote runtime provisioning remains COORD-03 work.

The actual desktop coding scenario authors a Node backend and a built/rendered frontend, runs frontend work while backend work remains active, handles a breaking contract revision, and executes an HTTP integration check. Clarification IDs reject stale answers; per-turn cancellation permits a subsequent response; emergency stop drains app-owned processes and survives whole-app relaunch until explicit reconnect. Registry RPC and settled UI independently prove peers after restart. See [clause mapping](docs/local-interactive-coding-acceptance.md). COORD-08 remains open for opt-in real-provider acceptance and its remaining credential requirements; revocation is not credential TTL.

Reader mode now denies arbitrary shell/PTY/Git/unknown mutators before effects; configured approval sees unified filesystem writes. Production Reader selection and approval callback installation remain unwired, rather than being claimed as delivered permission policy. See [tool admission evidence](docs/tool-admission-safety.md) and [clarification/cancellation](docs/clarification-and-turn-cancellation.md).

Current full Go race gate passes 107 packages plus eight no-test packages; vet and Windows source compilation pass. Terminal tests pass 42/42. Actual Linux/arm64 Docker build/runtime acceptance passes after fixing lost rapid tab-navigation increments with a functional state update; the initial failing Ink navigation case remains in `.cache/container-runtime/interaction-docker-verification.log`, passing rerun in `interaction-docker-verification-final.log`.

Research adds 104 originals and 155 independently scoped units, with no verified upgrades. Root independently checks all 432 current spans and all 27,867 catalogue/explorer rows; 8,093 originals remain unassessed. Historical changed anchors retain their original certification evidence and require review rather than silent recertification. See [execution safety follow-up](docs/harness-research/semantic-execution-safety-followup.md).

Final current-source desktop gate passes 84/84 with zero skips, terminal gate 42/42. New development DMG: `.cache/distribution/overkill-0.1.0-dev-arm64-tpuT1f/Overkill-0.1.0-development-arm64.dmg`, SHA-256 `c1bf1564bd625ddc1bec82475907e9c768396e8905c19b4367e73e367e0beb95`. Installed fake-storage smoke passes all bounded cases, including same-version replacement/relaunch and database-crash recovery (`.cache/interaction-installed-smoke.log`). Root checks all 28 packaged desktop sources plus signed backend checksum. Real installed Keychain, paid providers, minimum OS and distribution approval remain open. The broader scripts unit run passes 46 tests with two declared skips; this is distinct from the 21-test research-specific gate.


### Contract and terminal clause acceptance — 2026-10-22

Original COORD-05/07 are closed after checking their complete named clauses,
not an invented cloud-control or artifact-execution criterion. Current original
checklist: **110 repaired /8 open (3 P1, 5 P2)**.

The real PostgreSQL race contract gate passes 26 cases including subtests, zero
skips/failures; root parses the JSON receipt independently. It covers immutable
schemas/revisions, ownership effects under the shared transaction lock,
repository worktrees, dependencies, stale pins and acknowledgements. The actual
model-authored built/rendered frontend/backend scenario supplies combined HTTP
integration acceptance. Artifact metadata remains a declaration rather than
verification.

The terminal now exposes readable agreements/artifacts, pinned versus current
revision, dependency and compatibility fields, and the last loaded correlated
event. Presence, questions, task outcomes, inspect/cancel and explicit reviewed
retry have compiled Ink/HTTP acceptance. Root review found and corrected two
new navigation regressions: stale rapid view increments and next-agreement `n`
being intercepted by new-request `n`. The actual rapid-key/second-contract test
and full 46-test compiled terminal suite pass with zero skips.
See [clause mapping](docs/coordination-contract-terminal-audit.md),
`.cache/coord05-contract-audit.jsonl` and `.cache/tui-coord07-all.log`.

Additional source repairs enforce configured readonly across actual CLI/API
Run/Stream and shared children; installing a writer gate cannot bypass the
configuration ceiling. Actual BEFORE proof created a file, AFTER fixtures
preserve reads and reject writes. Vision file reads now use workspace-root
traversal and reject symlink escapes and unbounded/non-regular input. Generated
image downloads reject private initial destinations and rebinding, preserve TLS,
refuse redirects, bound full bodies and remove partial files. Stability output
is no longer truncated at4 MiB; Replicate reads before cancelling its poll
context and has a bounded whole flow. Configured tools advertise real model
argument metadata. See [admission](docs/tool-admission-safety.md) and
[image boundaries](docs/multimodal-tool-boundaries.md). These source changes
are newer than the frozen tpuT1f DMG and do not recertify that image.


### ACP runtime and bounded coordination closure — 2026-10-22

Original COORD-03/06 are closed after complete original-clause review and the
combined source gate: **112 repaired /6 open (1 P1, 5 P2)**. Six fixed-identity
ACP-backed model tools now have explicit authenticated per-session provisioning,
persisted binding, private credential references, safe cached-runtime rebuilds
and real model→HTTP→store handoff. Same-hub identity/workspace matching is
required; cloud/foreign-hub executor deployment is unsupported. Private random
execution capabilities preserve task, conversation, deadline, hop and contract
ancestry across transport and are revalidated transactionally. Actual frontend
file work continues while backend is active, then the correlated answer arrives
at a safe turn boundary.

Busy queues, cancellation, deadlines, concurrency, attempts/hops/cycles, shared
task usage and explicit escalation have executed real PostgreSQL race evidence.
Circular children receive blocked review evidence and never execute; the parent
may finish with an explanation. Budgets are checked between model calls, not
reserved against invoices. Root verifies 19 unchanged coordination production
files and all155+20+8 named case receipts with zero skips/failures; the shared
API constructor's subsequent workspace field is checked by the final broad
gate. See [runtime clause mapping](docs/coordination-runtime-acceptance.md) and
`.cache/coord036-root-checks.json`.

The first full gate exposed bridge stop resolution being rejected while a chat
was busy; fixed resolution retains idle gateway callbacks while busy controls
use the same bound adapter without rebuilding it. The final107-package race
gate, vet and Windows source compilation pass. Terminal47/47 tests pass on
macOS and actual LinuxNode22 after a separate committed-frame/stale-input
listener defect was reproduced and repaired with a stable callback updated in
layout commit. The initial Docker failure and exact BEFORE fixture are retained;
final actual container build/fresh PostgreSQL/packaged TUI/authentication pass.

Actual native busy-chat HTTP409 acceptance now preserves exact composer drafts,
creates no phantom history and sends no provider request until an explicit
manual retry. Immediate admitted stream events and delayed IPC replies cannot
clear a newer draft; admitted late failures retain partial history. Full native
desktop suite87/87 passes with zero skips. See
[admission evidence](docs/desktop-chat-admission.md).

A newly found file-reference leak is also repaired: automatic @path context now
uses immutable per-session workspace roots and bounded contained reads in both
Run and Stream. Actual legacy-source overlay reproduces ambient/external marker
injection; API two-workspace Run/Stream fixtures and a real built CLI prove
selected context, denied escapes and bounded1GiB sparse-file prefixes. See
[mention evidence](docs/workspace-mentions.md).

Final combined source:107 Go race packages +eight no-test packages, vet, Windows
source build,87 mandatory desktop tests and47 terminal tests pass. Final Docker
image/fresh database/packaged TUI/authentication passes
(`.cache/container-runtime/transport-mentions-docker-final.log`); project-owned
VM stopped after cleanup. The corrected full-tree race command uses `-p 4`;
an initial `-p4` invocation only reported the root package and was rejected as
insufficient evidence. The later complete pre-fix bridge failure remains in
`transport-multimodal-full-race-final.log`, superseded by the passing full
`transport-mentions-full-race-final.log`.

New EptytH DMG SHA-256:
`db066b34fd9a23537142e83561bebd38e17c3d7944f38ebeec6fd854e37792cb`.
Installed fake-storage smoke passes; root verifies28 desktop files, signed
backend and byte-identical independent current-source backend rebuild. See
`.cache/transport-mentions-artifact-root-checks.json`. No real installed
Keychain, paid provider, minimum-OS, cross-version migration or distribution
readiness is inferred. Research's multimodal snapshot adds72 originals/92
independently scoped units; root checked230 exact spans and all27,867 explorer
rows with catalogueSHA5068a16f7115b8c0ca863b9430f05067412ef6205cf73fb15fe7942af1556cb8.
These are named-snapshot checks before later mention-source changes, not
blanket recertification. New frontend mapping work remains ongoing.
