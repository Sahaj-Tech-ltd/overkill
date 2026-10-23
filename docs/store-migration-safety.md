# Learning and cron store startup safety

This follow-up narrows two real PostgreSQL startup defects. It does not certify every application migration or historical database shape.

Before the fix, sixteen simultaneous fresh learning-store constructors produced `pg_type_typname_nsp_index` duplicate-key errors despite `CREATE TABLE IF NOT EXISTS`. PostgreSQL's table existence clause does not serialize concurrent creation of associated catalog objects. The real isolated-schema reproduction is recorded in `.cache/store-migration-before.log`.

The cron reproduction supplied a preexisting log table without `run_at`. Its last index statement failed, but `cron_jobs` and preceding indexes had already committed independently. The constructor returned an error while leaving a partly initialized schema. The same before-fix log contains the failed rollback assertion.

`internal/learning/store.go` and `internal/cron/store.go` now perform startup DDL inside one transaction, acquire a transaction-scoped advisory lock keyed by the current schema and store name, and bound all pool acquisition, lock waiting, and DDL to a ten-second context. Constructors reject a nil pool with an error. Every failure returns no store; deferred rollback releases transactional changes and the advisory lock. Existing pool ownership stays with the caller.

## Executed scenarios

The required real-PG tests use `internal/testpg` to allocate an isolated schema per test, with `OVERKILL_REQUIRE_TEST_DB=1` preventing skips:

- Sixteen simultaneous learning constructors and eight simultaneous cron constructors over fresh schemas all succeed; learning then persists an upsert across repeated construction.
- A final cron index failure rolls back the earlier jobs table. The incompatible log table and its original output remain unchanged. Explicit fixture repair renames the incompatible table; two retries succeed and a job plus run log can be saved and read.
- Each store's migration advisory lock is deliberately held by a separate transaction. Construction returns an error after its startup deadline, publishes no store, and creates no table. Releasing the lock permits successful retry and persistence.
- Both nil-pool constructors return an error rather than panic.

Final race-run evidence: `.cache/store-migration-safety-final.jsonl`; focused vet evidence: `.cache/store-migration-safety-vet.log`.

```sh
PG_TEST_URL='postgres://overkill_test:local-audit-only@127.0.0.1:55432/overkill_test?sslmode=disable' \
OVERKILL_REQUIRE_TEST_DB=1 .cache/toolchains/go/bin/go test -race -json \
  ./internal/learning ./internal/cron -count=1 -timeout=2m
```

## Remaining limits

The advisory lock coordinates updated constructors for the same store and schema. Older binaries, manual DDL, or unrelated migration writers do not participate automatically. There is no global transaction spanning different application stores.

`CREATE TABLE IF NOT EXISTS` still does not validate every column, constraint, or historical shape of an existing table. No automatic repair, destructive coercion, historical deduplication, archived-release restore, process-crash, power-loss, or PostgreSQL major-upgrade acceptance is claimed. The cron test's incompatible table is repaired explicitly by the test operator, never by startup.

Daemon startup was inspected read-only: `internal/daemon/jobs.go` still executes its table creation and index creation separately, without a bounded context or the new advisory-lock protocol. Memory and other stores likewise require their own evidence. Learning operations after construction retain their existing per-instance synchronization and context behavior; this change does not establish cross-process eviction serialization or a general query deadline.
