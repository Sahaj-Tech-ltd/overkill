# Durable Seahorse history compaction

The new `internal/compaction/seahorse.HistoryCompactor` generates and stores actual hierarchical summaries. It implements the existing agent `HistoryCompactor` signature. The earlier `Assembler` remains a separate historical API; its item-ID heuristics are not used by this compactor.

## Runtime binding

Construct `NewPostgresStore(ctx, db)` against the application's PostgreSQL pool, then `NewHistoryCompactor(provider, tokenizer, store, trustedSessionID, options)` for each agent/session. `HistoryOptions.SummaryModel` selects the configured summarization model; an empty value uses the current model passed to `Compact`. The constructor does not silently select an unrelated provider/model. The shared application runtime now installs this through `SessionRuntime.HistoryCompactorFactory` in both CLI and API agent assembly. Desktop/TUI and gateway paths that use this API assembly inherit the same factory. Standalone API assembly uses the same configured strategy and clones its registry before binding tools, preventing one chat’s memory adapter from entering a shared registry.

`Compact` returns a summary message carrying its durable node ID followed by the unchanged recent raw messages. The separate summary return value supports the existing agent interface, but the returned history also contains the summary because the agent publishes nonempty returned histories directly.

## Durability and failure behavior

Each raw provider message is content-addressed and stored intact, including tool names, arguments, IDs, result text and attachment bytes. Text summarization requests contain ordered JSON source data; attachment bytes are represented by metadata and their raw-byte hash in those text requests. Raw data remains available in the store.

Leaf nodes reference ordered raw source IDs. Condensed nodes reference ordered child node IDs, record their depth and descendant counts, and retain every earlier raw lineage. Node hashes include summary content, source/parent IDs and metadata. Earliest/latest timestamps represent ingestion/compaction times, not unavailable original event timestamps. Summaries are fallible model output, not original evidence.

One PostgreSQL transaction writes raw sources, new immutable nodes and the session head. A revision check under a session-row lock rejects competing branches. Provider requests run outside that transaction. Failed provider calls, denied accounting, oversized output, insufficient budgets, cancelled contexts, failed storage and stale revisions leave the supplied history unchanged. There is no truncation fallback. Exact-input retries after a crash can reload the committed output without repeating model requests; identical concurrent compactions reuse the committed winner. Different concurrent branches cannot overwrite each other.

The recent tail is retained as provider messages. The compactor uses assistant tool-call IDs and their matching results to keep complete parallel batches together; message-number adjacency is not a substitute for that check. Orphan, duplicate and incomplete tool-result batches are rejected.

## Retrieval and bounds

The runtime exposes `memory_search`, `memory_describe` and `memory_expand`, backed by `PostgresStore.Node`, `Source`, `SearchSources` and `seahorse.Expand`, as read-only memory tools. Session IDs must come from the trusted agent runtime rather than model-supplied arguments. PostgreSQL full-text search is parameterized and restricted to that session. Expansion returns exact source order and is all-or-error; bounded partial output must not be presented as complete evidence.

Default input bounds are 4 MiB and 4,096 messages. Each compaction produces at most 128 leaf nodes/256 total nodes. Fan-in defaults to four and shrinks when the selected model's input budget requires it. Requested output limits, provider-declared context/output ceilings, untouched-tail cost and conservative estimated prompt/wrapper overhead are checked. These estimates are not exact provider tokenization or billing; actual provider-reported usage is recorded through `modelusage` hooks, including unsuccessful compaction attempts that already called a model.

Each session retains at most 100,000 unique raw sources, 20,000 nodes and 512 MiB of source/node JSON. Maximum DAG depth is 64. Reaching these limits refuses additional compaction atomically; existing evidence is not evicted. These are per-session limits, not a global database quota. Expansion is limited to 1,024 node visits, depth 64, up to 4,096 messages and 4 MiB of message JSON. Search returns at most 50 previews.

Hashes detect record corruption when inspected; this is not a signed audit chain, independent compliance proof or protection against an administrator who rewrites both data and hashes. The package does not claim automatic skill evolution or a cloud/tenant boundary.

## Executed acceptance

The required PostgreSQL race gate is recorded in `.cache/seahorse-dag-race-final.jsonl`:

```sh
OVERKILL_REQUIRE_TEST_DB=1 PG_TEST_URL=<isolated-test-server> \
  go test -race -json -p 4 ./internal/compaction/... -count=1 -timeout=3m
```

The new integration tests use isolated real PostgreSQL schemas and the production OpenAI provider's HTTP request/response adapter against a loopback fixture. No paid model or external account is contacted. They verify actual leaf and condensed provider requests, configured model selection, context/output bounds, reported usage callbacks, multi-level persisted ancestry, exact raw reconstruction, tool-batch preservation, source attachment retention, reload/idempotent retry, continued ancestry, session isolation, transactional rollback, competing/identical concurrent commits, concurrent schema creation, bounded expansion/search/dataset rejection, forged markers and corrupted raw/node records.

### Shared runtime acceptance

`compaction.strategy` supports `auto` (the default), `seahorse`, `lcm` and `legacy`. Auto selects Seahorse when a durable PostgreSQL pool exists and retains LCM for a runtime without PostgreSQL. Explicit Seahorse selection fails initialization when PostgreSQL is absent/unavailable. Explicit LCM keeps the existing LCM implementation. The existing `use_lcm=false` setting remains a legacy kill switch: it disables automatic strategy compaction and leaves manual compaction on the legacy path. The summarizer uses the configured model override or the same provider’s existing cheapest-listed-model policy; it never silently selects another provider.

The new runtime acceptance is `.cache/dag-runtime-focused-final.jsonl`: 25 named race-test pass events across six packages, zero skips/failures. `TestSharedRuntimeDAGActualRunStreamMemoryToolsAndReload` starts the real authenticated application API with the shared runtime, restores large histories in two real PostgreSQL sessions, invokes both `agent.send` (Run) and `/stream` (Stream), and verifies automatic persistent DAG compaction before the main provider request. In each path the local model fixture calls the actual registered `memory_search`, `memory_describe` and `memory_expand` tools; the subsequent provider requests contain the exact recovered original raw source. Session history and DAG nodes remain readable through a fresh runtime service object. The fixture uses the production OpenAI HTTP/SSE adapters, never paid model calls.

Additional gates verify standalone API registry/session isolation, strict per-tool input schemas and trusted identity, read-only admission, selected/cheapest model policy, explicit LCM/legacy selection and fail-closed unavailable DAG selection. CLI uses the same factory closure; a separate installed desktop interaction remains the parent integration gate.

A broader regression checkpoint passed compaction, Seahorse, config, tools and API, but four actual CLI child-build tests encountered the evolution agent’s concurrent incomplete edit (`Engine.persistDraft` missing). That transient checkpoint is preserved in `.cache/dag-runtime-regression-final.jsonl`; it is not certified as a green full regression. After the missing method landed, all four affected actual CLI build/run tests were rerun successfully: 15 named test/subtest pass events, zero skips/failures, recorded in `.cache/dag-runtime-cli-regression-rerun.jsonl`. These exercise actual CLI scoped instructions, personality settings, hot reload and sync persistence with the new factory setter. The final complete-tree/installed desktop gate remains separate.
