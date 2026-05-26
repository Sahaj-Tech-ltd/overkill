package checks

import (
	"context"
	"path/filepath"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// RegisterDB runs a BadgerDB integrity check via session.Probe.
// Registered as "db.integrity" — the plan calls it `overkill doctor --check-db`.
func RegisterDB(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "db.integrity",
		Name:     "BadgerDB integrity check",
		Category: doctor.CatCore,
		Fn: func(ctx context.Context) doctor.Result {
			dir := filepath.Join(d.ConfigDir, "sessions")
			exportPath := filepath.Join(d.ConfigDir, "memory-export.md")

			res := session.Probe(dir, exportPath)
			if res.Corrupt {
				return failf(
					"run `/restore <path>` if you have a memory-export.md backup, or delete ~/.overkill/sessions to start fresh",
					"db corrupt at %s: %s", dir, res.Cause,
				)
			}
			return okf("db healthy at %s", dir)
		},
	})
}
