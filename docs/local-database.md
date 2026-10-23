# Managed local PostgreSQL for CLI commands

`run`, `tui`, `serve`, and `daemon start` can explicitly own a private PostgreSQL
cluster. This is disabled by default. Overkill never downloads PostgreSQL during
command startup. Supply an existing, compatible PostgreSQL runtime containing
`postgres`, `initdb`, and `pg_ctl` and its required libraries/share files.

```sh
overkill serve --local-database --local-database-bin /absolute/path/to/postgres/bin
overkill tui --local-database --local-database-bin /absolute/path/to/postgres/bin
overkill run --local-database --local-database-bin /absolute/path/to/postgres/bin
overkill daemon start --local-database --local-database-bin /absolute/path/to/postgres/bin
```

Use the same settings persistently in `config.toml`:

```toml
[local_database]
enabled = true
bin_dir = "/absolute/path/to/postgres/bin"
# Optional; defaults to the profile's postgres directory.
data_dir = "/absolute/path/to/private/cluster"
```

`--local-database=false` overrides the saved opt-in for that invocation.
`--local-database-bin` and `--local-database-dir` override the corresponding paths.
Paths to binaries and clusters must be absolute. The default cluster is
`OVERKILL_HOME/postgres`, or `postgres` under the normal configuration directory
when `OVERKILL_HOME` is unset. Use an absolute `OVERKILL_HOME` for managed mode.

Selection order is `DATABASE_URL`, then `database_url` in configuration, then an
explicitly enabled managed cluster. External connection strings always win,
even when managed mode is enabled. Closing a command closes only its pool when
using an external database; it never stops that server. Other database-consuming
utility commands continue to use their existing external connection settings.

Without a configured database, `run` retains its existing nonpersistent mode;
`tui`, `serve`, and `daemon start` require an external URL or managed opt-in.
Provider configuration is still required for running agents. Managed database
startup does not configure a provider or install the terminal frontend.

The manager initializes a fresh private cluster, generates private credentials,
binds only to loopback on a dynamic port, applies migrations, and starts the
application only after PostgreSQL is ready. The application drains and closes
its pool before PostgreSQL stops. A database child crash cancels the application
and returns an error. Cluster data survives normal shutdown and relaunch. The
manager locks each cluster to one owner; simultaneous commands need separate
profile/cluster paths or a shared external PostgreSQL server.

Keep the cluster directory and its adjacent `.manager.json` ownership/credential
file together. Unsupported PostgreSQL major versions, missing runtimes, corrupt
clusters, and unverified existing servers cause explicit errors; startup does
not silently replace a cluster. This path does not establish distributable
PostgreSQL packages for every supported operating system: release packaging and
runtime availability must be verified separately on each target.

The manager authenticates the server's exact process identity before accepting
readiness or recovering an orphan. See [identity verification and startup
diagnostics](local-database-identity.md) for the checks and troubleshooting evidence.
