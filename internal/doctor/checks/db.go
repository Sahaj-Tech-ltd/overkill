package checks

import (
	"context"
	"database/sql"
	"net/url"
	"os"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
)

// RegisterDB checks that the database is configured AND reachable.
// Formerly only checked DATABASE_URL is set — never parsed DSN or pinged.
// Now validates: env var set, DSN parses, Postgres responds, sessions
// table exists.
// Registered as "db.integrity".
func RegisterDB(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "db.integrity",
		Name:     "Database integrity (PostgreSQL)",
		Category: doctor.CatCore,
		Fn: func(ctx context.Context) doctor.Result {
			dsn := ""
			if d.Cfg != nil && d.Cfg.DatabaseURL != "" {
				dsn = d.Cfg.DatabaseURL
			}
			if dsn == "" {
				dsn = os.Getenv("DATABASE_URL")
			}
			if dsn == "" {
				return failf("set database_url in config.toml or DATABASE_URL env var",
					"database_url not configured")
			}

			// Parse DSN to catch malformed URLs early.
			u, err := url.Parse(dsn)
			if err != nil {
				return failf("check database_url format — must be a valid postgres:// URL",
					"cannot parse DSN: %v", err)
			}
			if u.Scheme != "postgres" && u.Scheme != "postgresql" {
				return failf("database_url must start with postgres:// or postgresql://",
					"unsupported scheme %q in DSN", u.Scheme)
			}

			// Open and ping.
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				return failf("check that database_url points to a running PostgreSQL",
					"open: %v", err)
			}
			defer db.Close()

			if err := db.PingContext(ctx); err != nil {
				return failf("PostgreSQL is unreachable — check host, port, and network",
					"ping: %v", err)
			}

			// Validate sessions table exists.
			var exists bool
			err = db.QueryRowContext(ctx,
				`SELECT EXISTS (
					SELECT FROM information_schema.tables
					WHERE table_schema = 'public' AND table_name = 'sessions'
				)`).Scan(&exists)
			if err != nil {
				return warnf("could not verify sessions table; run migrations",
					"table check: %v", err)
			}
			if !exists {
				return warnf("sessions table not found; run `overkill migrate`",
					"sessions table does not exist in public schema")
			}

			return okf("PostgreSQL reachable, sessions table present (%s@%s)", u.User.Username(), u.Host)
		},
	})
}
