# Central database bootstrap migration safety

`db.Migrate` now applies its shared suspension/routine upgrades and central table/index definitions in one transaction. A ten-second context covers pool acquisition, owner-lock waits, statements and commit. `dbschema.Bootstrap` takes the same schema-scoped suspension/routine advisory locks as standalone stores, followed by the central bootstrap lock, before executing any DDL. All participating bootstraps use this order. Standalone constructors acquire only their own lock. Failure returns a contextual stage error and rolls back; the caller retains its pool.

Before the change, a real PostgreSQL fixture with an unsupported JSON bookmarks index caused a late bootstrap failure while both suspension/routine column additions had already committed. Sixteen concurrent bootstraps with a prewarmed pool also reproduced a relation-already-exists catalog race despite `IF NOT EXISTS`. These failures are recorded in `.cache/bootstrap-atomic-before.log`.

`internal/db/migration_atomic_test.go` now verifies:

- A late bookmarks-index failure rolls back shared-store columns, earlier central tables and indexes, while preserving legacy approval state, routine steps and bookmark data. Explicit operator conversion of the unsupported fixture column permits two successful retries without discarding its content.
- Sixteen concurrent bootstraps succeed; persisted input history survives another bootstrap.
- A held routine-owner lock causes a visible bounded startup failure before suspension DDL is published; releasing it permits retry.
- Eighteen mixed central/suspension/routine constructors succeed against one fresh schema without conflicting catalog creation or deadlock.

The mandatory PostgreSQL race run for `internal/db` and `internal/dbschema` passes in `.cache/bootstrap-atomic-final.log`. Existing fresh/legacy store bootstrap acceptance also runs in that package suite.

This is atomicity for central bootstrap's own transaction, including its shared-store definitions. Session, cost, learning, cron, daemon, memory and other independent constructors retain separate transactions; it is not one transaction across the entire application's startup. Advisory locks coordinate updated constructors, not older binaries or manual DDL. Existing names do not validate arbitrary historical table/index definitions. No automatic destructive repair, archived-release matrix, process-crash/power-loss, or major-PostgreSQL-upgrade claim is made.


## Shared goal and gateway owner follow-up

Bootstrap now also acquires the goal, gateway bookmark and gateway input-history owner locks before DDL, using exactly the definitions and keys used by their standalone constructors. Goals include the stable-identity/time backfill and turn journal in this transaction. Gateway delivery remains separately owned and is not silently added to central bootstrap. The prior suspension/routine-only description records the earlier checkpoint; current lock order is suspension, routines, goals, bookmarks, input history, bootstrap.

Real pre-fix concurrent central/store constructors collided on PostgreSQL table-type uniqueness for bookmarks and goals. Late bookmark-index and goal-backfill failures also committed earlier independent statements. Shared owner transactions repair both cases; focused four-package race acceptance and ten repetitions of each migration lane pass. See [gateway safety](gateway-migration-safety.md), [goal safety](goal-migration-safety.md), `.cache/gateway-migration-before.log`, `.cache/goal-migration-before.log`, `.cache/goal-gateway-migration-focused.log`, `.cache/gateway-migration-repeat.log` and `.cache/goal-migration-repeat.log`. No one transaction covers all unrelated application stores, and these cases do not prove arbitrary historical schema compatibility.
