package checks

import (
	"context"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
)

// RegisterDB checks that database_url is configured. Formerly checked
// BadgerDB integrity; all persistent stores are now Postgres-backed.
// Registered as "db.integrity".
func RegisterDB(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "db.integrity",
		Name:     "Database config check (PostgreSQL)",
		Category: doctor.CatCore,
		Fn: func(ctx context.Context) doctor.Result {
			if d.Cfg == nil || d.Cfg.DatabaseURL == "" {
				return failf("set database_url in config.toml or DATABASE_URL env var",
					"database_url not configured")
			}
			return okf("database_url configured")
		},
	})
}
