package checks

import (
	"context"
	"path/filepath"

	"github.com/Sahaj-Tech-ltd/ethos/internal/doctor"
	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
)

// RegisterStorage opens the BadgerDB session store and runs a basic
// integrity probe (open, list iter, close). Serial — Badger doesn't tolerate
// two opens against the same dir.
func RegisterStorage(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "storage.sessions",
		Name:     "Session store (BadgerDB)",
		Category: doctor.CatStorage,
		Fn: func(ctx context.Context) doctor.Result {
			dir := filepath.Join(d.ConfigDir, "sessions")
			store, err := session.NewBadgerStore(dir)
			if err != nil {
				return failf("delete corrupted DB at "+dir+" and restart ethos",
					"badger open at %s: %v", dir, err)
			}
			defer store.Close()
			// Cheap read sweep — List with empty options enumerates all keys.
			if _, err := store.List(ctx, session.ListOptions{}); err != nil {
				return failf("backup and remove "+dir+", then restart ethos",
					"badger list: %v", err)
			}
			return okf("session store at %s is readable", dir)
		},
	})
}
