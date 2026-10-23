# Memory-store startup migration safety

`internal/memory/postgres.go` now wraps its existing table creation, legacy `content_hash` column addition, unique hash index, and lookup indexes in one transaction. A transaction-scoped advisory lock keyed by `current_schema() || ':overkill:memory'` serializes updated memory-store constructors in the same schema. A ten-second context covers pool acquisition, lock waiting, DDL, and commit. Nil pools return an error. Migration failures return no store, identify the failing stage, and roll back; the background pruner only starts after successful commit. The caller continues to own the database pool.

## Actual failures and verification

Before changing production code, real isolated-PG tests reproduced both defects:

- A preexisting memory table without `timestamp` failed only on the final lookup index. Its newly added `content_hash` column and earlier indexes had already committed. The rollback assertion failed.
- Sixteen constructors released together onto prewarmed connections against a fresh schema produced a `pg_type_typname_nsp_index` duplicate-key error (SQLSTATE 23505).

Both reproductions are recorded in `.cache/memory-migration-before.log`.

The new migration tests verify that a final lookup-index failure rolls back the added hash column and all newly created indexes while retaining the original content. Explicitly adding the missing timestamp column permits repeated construction and normal storage. The legacy row's hash remains NULL: startup does not backfill or reinterpret it. New identical content still deduplicates and an older write does not decrease its timestamp.

A separate duplicate-hash fixture proves that startup returns a visible unique-index error without deleting, rehashing, or choosing between conflicting legacy rows. Explicit operator repair clears one conflicting hash to NULL, preserving both contents, and retry succeeds. A deliberately held advisory lock makes startup return a lock-stage error at its deadline without creating the table; releasing it allows retry and persistence. Nil pools return an error without panicking.

The complete memory package passed **90 tests with no skips** under the race detector in `.cache/memory-migration-safety-final.jsonl`. The sixteen-constructor scenario also passed ten repetitions in `.cache/memory-migration-concurrency-repeat.log`. Focused vet passed in `.cache/memory-migration-safety-vet.log`.

```sh
PG_TEST_URL='postgres://overkill_test:local-audit-only@127.0.0.1:55432/overkill_test?sslmode=disable' \
OVERKILL_REQUIRE_TEST_DB=1 .cache/toolchains/go/bin/go test -race -json \
  ./internal/memory -count=1 -timeout=2m
```

## Bounds

Memory hash generation, retention, pruning, and ordinary query behavior remain unchanged. The lock coordinates updated constructors for this store and schema; older binaries and manual or unrelated DDL do not automatically participate. Existing same-name indexes and arbitrary historical table shapes are not fully validated by `IF NOT EXISTS`. Startup does not automatically repair incompatible schemas or duplicate hashes. These fixtures do not establish every historical upgrade, process-crash or power-loss recovery, or PostgreSQL major-version migration. Pool acquisition uses the startup context but was not separately held through its deadline in this test set.
