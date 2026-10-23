# Agent config reload behavior

`WireAgent` observes `user.yaml`; it does not initialize the agent from that file. Its initial read establishes the comparison baseline. Call the returned stop function when the agent shuts down.

Currently only `basic.model` is applied through `Agent.SetModel`. A changed model affects subsequent provider request construction, possibly another step within an existing Run. It does not replace a request already submitted to the provider. It does not switch provider credentials or verify that a provider supports an arbitrary model ID. Clearing the override, including file removal, restores the model observed when wiring was attached, rather than guessing a provider default. If that original model was empty, clearing is left unapplied and reported as requiring restart/reassembly.

Every other changed YAML setting is observed but left unapplied. This includes scanner toggles, persona, system-prompt fields/variables, tool policies, MCP servers, providers, memory, hooks and profile changes. This adapter does not rebuild scanners, install persona/prompt callbacks or reconcile servers. A restart-required result identifies the need to recreate the relevant consumers; it does **not** guarantee that every user.yaml field has a supported startup consumer. Startup/hierarchy validation and actual live scanner replacement remain separate integration work. Live scanner support should eventually atomically replace the scanners used by the running agent, with a feature-specific behavioral test, before being labeled applied here.

`ReloadResult` distinguishes:

- `Changed`: settings whose decoded values differ from the preceding valid snapshot.
- `Applied`: only settings actually passed to the live adapter; currently `basic.model`.
- `RestartRequired`: outstanding unapplied changes relative to the wiring-time snapshot. Warnings survive subsequent edits, and reverting a pending change clears it.
- `Err`: a read/parse error, or `*RestartRequiredError` for a valid file with unapplied settings.

A reporter that also implements `ResultReporter` receives this full result. Existing `Reporter` implementations receive `OnReload(Changed, Err)`; they can use `errors.As` to inspect `RestartRequiredError.Fields` and `.Applied`. Reports contain field names, not configuration values/secrets. Callbacks must return promptly. `NewLogReporter(log.Printf, sessionID)` emits one JSON log entry per outcome with `event`, `session`, `status`, `changed`, `applied`, `restart_required` and optional `error_kind`. It allows only known YAML field names and classifies errors without logging raw text, because parser errors can include secrets. The CLI and API use this reporter when their existing reload subscription is wired; CLI shutdown unsubscribes and API runtime cleanup retains its unsubscribe. The caller supplies an opaque session identifier. `DiscardReporter` deliberately hides outcomes and remains an explicit opt-out. The root command now starts and owns the shared watcher before readiness, and joins it even when command execution returns an error. It does not add a desktop notification sink.

A malformed file leaves the last valid snapshot and live model unchanged. Saving identical decoded values produces no outcome; comparison uses bool values and map/slice contents, not pointer identity. Filesystem notification/debounce remains best effort; this is not transactional configuration deployment.

Tests exercise a controlled local HTTP provider for Run and Stream: a held old-model request remains submitted unchanged while reload applies, and the next request uses the new model's output/context limits. Separate tests cover pending/reverted restart settings, fresh-decoded pointer equality, unhandled settings, legacy/detailed reports, invalid files, an unknown model fallback, classified structured logging and omission of model/credential/prompt/parser-error values. No external provider is contacted.

An additional real-watcher acceptance uses atomic YAML replacement, both Run
and Stream, a native shell positive control and a model-generated shell call
that matches an installed custom deny rule. Changing the model and requesting
that scanner be disabled applies only the model, reports scanners/persona as
requiring restart, and leaves the real pre-tool scanner active. The denied call
never reaches the execution guard or creates its file, and its actual error is
returned to the provider. This verifies the reporting boundary; it does not add
live scanner replacement or promise a startup consumer for every override.

The bus exposes `Start(ctx)` as a readiness barrier and terminal, concurrent-safe `Stop()` which joins watcher and debounce callbacks. It watches file parents and configured directory trees, registers missing ancestors when created, and avoids following nested symlinks. Atomic editor saves and repeated directory/file replacements remain observable. The application serializes watcher-owner transitions; agents own subscriptions only. The actual CLI process fixture exercises repeated atomic `user.yaml` changes against a local HTTP provider, followed by graceful SIGTERM. Notifications remain best effort; unapplied settings still require their own consumers.
