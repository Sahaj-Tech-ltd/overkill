# Daemon job-store startup safety

`internal/daemon/jobs.go` now runs table and index creation in one PostgreSQL transaction. A transaction-scoped advisory lock keyed by `current_schema() || ':overkill:daemon'` serializes updated daemon-store constructors in the same schema. A ten-second context covers pool acquisition, lock waiting, DDL, and transaction completion. Nil pools return an error. Failure publishes no store, reports its migration stage, and rolls back the transaction. The caller retains pool ownership.

## Reproduction and executed evidence

Before the change, sixteen constructors released together onto prewarmed connections against a fresh isolated schema produced `pg_type_typname_nsp_index` duplicate-key error (SQLSTATE 23505). Cold connection setup had hidden this failure in an initial run; the prewarmed reproduction is retained in `.cache/daemon-migration-before.log`.

Real PostgreSQL tests in `internal/daemon/migration_test.go` establish:

- Sixteen concurrent constructors succeed, and a saved job survives another construction. This scenario also passed ten independent repetitions after the fix.
- A preexisting table without `status` makes index creation fail. Startup returns no store, preserves the original row, and creates no index. Explicitly archiving that incompatible table permits two successful retries and normal job persistence; the archived data remains readable.
- A separately held migration advisory lock makes startup return a visible lock-stage error at its deadline, with no jobs table. Releasing the lock permits retry.
- A separate transaction creates an uncommitted relation with the future index's name. The constructor creates its jobs table and then blocks on the catalog uniqueness check for index creation. The deadline returns an index-stage error, and the earlier jobs-table creation rolls back. Releasing the conflicting transaction permits retry. This fixture requires no superuser event trigger or production-schema changes.
- Nil pools return an error without panicking.

The full daemon package race run passed 25 tests with no skips in `.cache/daemon-migration-safety-final.jsonl`. Repeated concurrency evidence is `.cache/daemon-migration-concurrency-repeat.log`; focused vet evidence is `.cache/daemon-migration-safety-vet.log`.

```sh
PG_TEST_URL='postgres://overkill_test:local-audit-only@127.0.0.1:55432/overkill_test?sslmode=disable' \
OVERKILL_REQUIRE_TEST_DB=1 .cache/toolchains/go/bin/go test -race -json \
  ./internal/daemon -count=1 -timeout=2m
```

## Bounds

This is a constructor migration change. Queue pickup, worker recovery, job state transitions, and job operation deadlines retain their existing behavior. The lock coordinates updated daemon constructors, rather than unrelated stores, older binaries, or manual DDL. `CREATE TABLE IF NOT EXISTS` does not validate every possible existing schema. Startup does not automatically repair or coerce incompatible tables. These tests do not establish all historical upgrades, process-crash durability, power-loss recovery, or PostgreSQL major-version upgrades. Pool acquisition uses the same bounded context but was not separately held to its deadline in this fixture.
