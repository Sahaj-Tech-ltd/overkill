# PostgreSQL upgrade and failure matrix

This matrix advances original audit item VALIDATE-01. It does not certify every historical database shape or close the broader database acceptance gate.

The tests use the real PostgreSQL fixture selected by `PG_TEST_URL` and a new isolated schema for each case (`internal/testpg`). `OVERKILL_REQUIRE_TEST_DB=1` makes missing configuration fail instead of skip. The predecessor fixtures below remove only columns covered by explicit production upgrade statements; they are supported shape tests rather than archived databases from every release.

| Case | Executed assertion |
| --- | --- |
| Sessions before metadata/token/children additions | Existing identity/title/folder survive two constructor runs; defaults load; save then save succeeds and persists the second title. |
| Cost records with INTEGER token columns | Existing three token values and cost survive widening; all token fields accept values above 2³³ after two constructor runs. |
| Memory before content-hash addition | Existing content survives; missing legacy hashes remain NULL; new repeated content deduplicates; two constructor runs succeed. No historical hash backfill is claimed. |
| Coordination messages before dependency/lineage additions | Payload and revision survive; dependency/agreement arrays default empty, lineage defaults empty/zero after two constructor runs. |
| Callback subscriptions before generation/backoff and partial uniqueness | Cursor and signing secret survive; generation=1 and epoch backoff default; disabling an old subscription permits its replacement; simultaneous active duplicates remain rejected. |
| Shared suspension schema, late index failure | An intentionally incompatible JSON session_id makes the final btree-index statement fail; all earlier column additions roll back; original state remains; explicit fixture repair then two retries succeed. |
| Coordination, late usage foreign-key failure | A deliberately incompatible preexisting usage table is preserved; earlier peers/messages/contracts/review tables roll back; renaming the incompatible table then retrying twice succeeds. |
| Cost records, second widening failure | An incompatible TEXT output-token column makes startup return no tracker and a contextual error. The earlier input-token widening rolls back, original malformed receipt/cost remain, and later indexes are absent. Explicit operator fixture repair permits retry and a wide-token insert. |
| Cost startup contention | Eight concurrent fresh constructors succeed under the schema advisory lock, followed by a usable tracker and wide-token insert. |
| Cost missing database | Constructor returns an error and no tracker rather than panicking. |

The first seven cases are in `internal/dbschema/migration_matrix_test.go`. The cost failure/concurrency/missing-pool cases are in `internal/cost/migration_test.go`.

The cost constructor previously logged widening failures and continued. Its migration now uses one transaction for creation, all three widening operations, and indexes; a schema-specific advisory lock serializes startup, and a ten-second context bounds database work. PostgreSQL rejects incompatible types without a destructive automatic cast. Failure returns a step-specific error; the operator retains the original values for repair.

## Verification

From the repository root:

```sh
PG_TEST_URL='postgres://overkill_test:local-audit-only@127.0.0.1:55432/overkill_test?sslmode=disable' \
OVERKILL_REQUIRE_TEST_DB=1 \
.cache/toolchains/go/bin/go test -race -json \
  ./internal/dbschema ./internal/db ./internal/session ./internal/agent \
  ./internal/cost ./internal/learning ./internal/cron ./internal/daemon \
  ./internal/memory ./internal/gateway ./internal/coordination \
  -count=1 -timeout=5m
```

The focused final cost/schema run passed with race detection (`.cache/cost-migration-failclosed.log`), and `go vet ./internal/dbschema ./internal/cost` passed. A subsequent 11-package mandatory run (`.cache/postgres-migration-matrix-suite-current.jsonl`) passed all ten new cases and ten packages, with zero skipped tests. Its coordination package had one unrelated receipt-comparison failure: `TestReviewRequestAtomicBudgetReceiptRestartAndWorkerClaim` compared `time.Location` values named `America/Los_Angeles` and `Local` after a restart. This run is not recorded as a fully passing suite. The root lane owns that test and its follow-up validation.

The 11-package suite also executes the existing session branch/merge/retention/router cases, goals and budgets, costs and usage, learning upsert/eviction, cron job persistence, daemon queue behavior, memory retention/dedup, and coordination recovery tests. Its test/subtest count includes non-database unit cases and must not be described as a count of distinct database scenarios.

## Remaining limits

- No full archived-release dump matrix has been assembled. The fixtures cover explicitly supported predecessor shapes and selected incompatible shapes.
- Central `db.Migrate` now applies its shared suspension/routine upgrades and central statements atomically, taking the same owner locks as standalone constructors. Independent store constructors remain separate transactions; there is no one global transaction across all application startup. See [central bootstrap safety](bootstrap-migration-safety.md).
- Cost, learning, cron, daemon and memory constructors now have bounded transactional/advisory-locked migrations with selected actual failure/rollback/concurrency fixtures. Those fixtures do not validate every incompatible historical shape, existing same-name index definition, older-binary/manual-DDL interleaving, or other stores. See [store](store-migration-safety.md), [daemon](daemon-migration-safety.md) and [memory](memory-migration-safety.md) safety.
- The pre-dedup memory upgrade preserves NULL hashes instead of backfilling or consolidating historical duplicates.
- Recreating constructors over one durable database proves constructor idempotency; this matrix does not claim OS-process crash/power-loss, backup/restore, PostgreSQL major-version upgrade, or every permission/extension configuration coverage.
- A single local PostgreSQL server and macOS host do not prove multi-platform release installation or pgvector/real embedding acceptance.

## Aggregate follow-up

After correcting the coordination receipt test to compare its persisted JSON
rather than Go timezone object internals, the root's complete required race run
passes all 107 packages, with eight no-test packages and no failures/races:
`.cache/request-review-full-race.log`. Vet and the Windows source cross-build
also pass. This supersedes the coordination failure in the earlier 11-package
run described above, without broadening the migration matrix's historical,
transactional or platform coverage.

## Central bootstrap and memory follow-up

Real pre-fix reproductions found a central late-DDL failure committed earlier shared-store upgrades, concurrent central bootstraps collided, memory late-index failure committed its added hash column and concurrent memory constructors collided. Both paths now use bounded transactions and schema/store owner locks. Central acceptance proves late-failure rollback across shared upgrades and central tables, legacy state/steps/bookmark preservation, explicit repair/retry, sixteen concurrent bootstraps, eighteen mixed bootstrap/standalone constructors, and held-lock deadline/retry. Both concurrency cases passed ten repetitions. Memory acceptance proves late-failure rollback, duplicate-hash preservation, legacy NULL-hash behavior, explicit repair/retry, held-lock deadline and sixteen concurrent constructors. See `.cache/bootstrap-atomic-final.log`, `.cache/bootstrap-concurrency-repeat.log`, `.cache/memory-migration-safety-final.jsonl`, `.cache/memory-migration-concurrency-repeat.log`.


## Goal and gateway migration follow-up

Actual old-shape/failure fixtures now additionally cover bookmarks with a JSON label (late btree-index failure) and goals with malformed historical seconds (late accounting-backfill failure). Both preserve original values, roll back earlier DDL and allow explicit operator repair followed by two successful startups. Central and standalone constructors use the same owner locks/SQL; twenty-four simultaneous constructors per lane complete without the reproduced PostgreSQL type-creation collision. Goal identity, historical nanoseconds and budget updates survive reconstruction. Gateway bookmark, history and failed-delivery records survive mixed startup/reconstruction. All four owner lock deadline/retry cases and nil-pool errors have required PostgreSQL acceptance. See [goal safety](goal-migration-safety.md) and [gateway safety](gateway-migration-safety.md). Focused four-package race tests plus ten repetitions per lane pass. Historical/archive, platform and broader transaction limits above remain unchanged.
