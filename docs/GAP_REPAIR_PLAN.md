# Security, recovery and memory repair plan

Started 2026-10-23. Source requirements: [uploaded-document comparison](/Users/abc/Documents/overkill/docs/zip-docs-code-comparison.md). The current dirty worktree is the baseline; preserve existing work and do not publish/deploy implicitly.

## Order and ownership

1. **Security admission first.** Parallel lanes repair structured shell detection/environment/workspace boundaries and LSP canonical paths/untrusted output provenance. Root closes hard read-only planning and credential persistence gaps. Snapshot storage safety is reviewed concurrently, before installing its hooks. No destructive scanner payload is executed.
2. **Automatic checkpoints.** Install session/workspace-bound filesystem snapshots in the shared CLI/API runtime. Capture actual pre-write data, modes and absence; handle patch/rename/delete and shell effects, refuse unsafe paths and incomplete capture. Failed required capture must prevent the mutation. Do not auto-commit or modify the user's Git index. Prove restoration with real temporary files and the native app.
3. **Durable resume.** Persist exhausted/timed-out work with original session/workspace/model/history. Atomically lease resume attempts, recover after process restart, reject stale/duplicate completion, and honor explicit abort/estop without accidental wakeups. Route continuation through normal authenticated runtime/admission/verification rather than a global cheap-model shortcut. Prove a real backend restart and concurrent claim attempt.
4. **Memory correctness and wiring.** Connect the intended DAG compactor, bounded correction retrieval and reviewed skill evolution to actual Run and Stream. Preserve current constraints, report unavailable capability honestly, protect secrets and require review before applying generated executable guidance. Add isolated per-item file context and capability inventory backed by the enabled tools. Prove cross-session/restart retrieval, bounded dataset handling, real model/vector behavior and no unapproved skill application.
5. **Proof and product gaps.** Durable anchored receipt export/independent verification, measured context savings and unified onboarding remain tracked requirements from the comparison. Cloud-first needs an explicit architecture/tenant isolation design and local acceptance; this plan does not authorize deploying a shared service or claim a compliance certification. Keep market/benchmark claims unverified until measured.
6. **Actual app and distribution acceptance.** After security integration, exercise the desktop against a controlled local provider with real PostgreSQL: safe planning, rejected shell/LSP escapes, checkpointed edit/restore, exhausted flow/restart/resume, and memory reuse. Run the appropriate race/frontend/native suites. Package a new development DMG only after its source gates pass; bind its contents to the tested source. Distinguish source/native fixture tests from installed Keychain and paid-provider acceptance.

## Completion evidence

Each lane must retain a reproducer for the original defect, a regression proving the behavioral repair, current source hashes, actual command output and explicit limits. A constructor inventory, static search, function name or generated receipt alone does not establish end-to-end wiring. Security errors must not become success acknowledgements; no plaintext credential fallback is allowed for newly secured storage. Tests use private fake credentials and owned disposable workspaces/databases.

## Status

- [x] Structured shell classification, environment and canonical workspace boundaries. Original 12 non-executing probes now block/deny; aggregate permission grants cannot clear unrelated findings. Static detection remains defense in depth, not an OS sandbox.
- [x] LSP request/result path containment and untrusted output handling in Run/Stream. Actual owned LSP subprocess and next-model-request tests pass; annotation remains advisory.
- [x] Hard read-only `/plan`, secured onboarding/persistence paths, immutable configured readonly. Injected-vault acceptance passes; native OS-vault testing remains separate.
- [x] Safe filesystem checkpoint implementation and production hooks; native edit/restore acceptance passes within documented capture scope.
- [x] Durable automatic resume with cancellation/restart/duplicate fencing; real PostgreSQL/process-death and native app restart acceptance passes.
- [x] DAG runtime and skill evolution wiring, bounded records and explicit reviewed application/rollback. Correction retrieval remains bounded keyword matching; the proposed correction graph is still open.
- [x] Isolated item context and actual capability inventory in both execution modes; actual provider, PostgreSQL resume and native tests pass.
- [ ] Durable signed/anchored audit export and independent verification.
- [ ] Measured savings report and remaining onboarding/cloud architecture work.
- [ ] Native desktop acceptance and source-bound development image.

The full objective remains active until the required behaviors and their matching acceptance evidence exist. Parallel preparation does not bypass the security integration gate.

## First integration checkpoint

Security source gates pass the focused scanner/bash/tool race suites and 35 external-boundary checks. `/plan` now has an independent read-only restriction; interactive mode switches cannot clear configured readonly. Missing approval clients fail closed for safe mode and dangerous YOLO calls. Actual Run/Stream tests exercise file writes, not only a mode flag.

CLI/TUI onboarding now stages named credentials in the OS vault and persists references. Generic saves preserve environment/keyring templates after runtime resolution. HTTP/TOML/restart/failure tests use an injected vault and pass; real OS-vault/platform acceptance remains pending. [Storage contract](/Users/abc/Documents/overkill/docs/config-credential-storage.md).

Filesystem checkpoints are wired in shared CLI/API sessions. Storage/restore now uses confined file operations, checks manifests and content hashes, preserves modes, rejects incomplete captures, and retains pre-restore recovery. Required capture failures block the tool in Run and Stream. Shell snapshots explicitly cover Git-visible files in Git projects; ignored files and Git metadata are excluded, while explicit file targets can be captured individually. This limitation is not a claim of arbitrary shell rollback.

Native acceptance has exercised actual Electron, the prepared backend, private PostgreSQL and a controlled provider: planning denial, reordered shell flag denial against an owned sentinel, automatic pre-write capture, restore of original bytes/mode, and durable continuation across a full app restart without duplicate writes or original input. Desktop continuation polling refreshes saved history while retaining the draft. Fake safeStorage is explicit; this is not installed Keychain or paid-provider acceptance. Receipts/logs are under `.cache/security-checkpoint-native/`, `.cache/flow-resume-native/` and `.cache/gap-native-app-acceptance-*.log`. The first combined run exposed an approval/plan interaction; the final corrected four-scenario native gate passes, as does the complete 93-test desktop suite.

Durable continuation and persistent DAG memory have real PostgreSQL/provider-adapter regressions. Shared-runtime, loaded-plan, UI and broader integration checks pass. Skill evolution and isolated per-item context are now wired and exercised through the development app. Durable proof exports, measured savings, the proposed correction graph, remaining onboarding and tenant architecture are still open. No new DMG has been packaged for this checkpoint.

Final integration: [application acceptance](/Users/abc/Documents/overkill/docs/skill-context-app-acceptance.md). The required Go race suite passes 107 packages; all 93 desktop tests pass with no skips; vet and Windows compilation pass. The independently rebuilt backend matches the exact native-tested binary. These results establish the documented local behavior, not compliance or permanent learning guarantees.
