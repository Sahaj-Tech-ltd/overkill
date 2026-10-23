# Daemon flow-store migration safety

The independent PostgreSQL `flow_states` store now initializes its table and index in one transaction under a schema-specific advisory owner lock, with a ten-second constructor context. It returns no store on migration error or missing database. This remains separate from central bootstrap's `agent_flows` table; the two state formats are not merged.

A real warmed-pool startup fixture launched twenty-four constructors against an empty isolated schema. All three pre-fix repetitions failed with PostgreSQL's `pg_type_typname_nsp_index` uniqueness error (`.cache/flow-migration-before.log`). The locked migration passes ten repetitions, writes a real flow, reconstructs twice and preserves its session/objective. A held owner lock respects the caller deadline without creating a table, then permits retry; nil pools return errors (`.cache/flow-migration-repeat.log`).

The implementation is `internal/dbschema/flow.go`, called by `internal/agent/flow_store_postgres.go`. Store startup is bounded and atomic; this does not change the legacy context-free Save/Load interfaces, validate every malformed historical state, certify crash/power-loss behavior, or claim that all application migrations share one global transaction. Original VALIDATE-01 remains broader than these cases.
