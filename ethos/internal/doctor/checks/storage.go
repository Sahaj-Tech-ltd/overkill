package checks

import (
	"context"
	"os"
	"path/filepath"

	"github.com/dgraph-io/badger/v4"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
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
				return failf("delete corrupted DB at "+dir+" and restart overkill",
					"badger open at %s: %v", dir, err)
			}
			defer store.Close()
			// Cheap read sweep — List with empty options enumerates all keys.
			if _, err := store.List(ctx, session.ListOptions{}); err != nil {
				return failf("backup and remove "+dir+", then restart overkill",
					"badger list: %v", err)
			}
			return okf("session store at %s is readable", dir)
		},
	})

	// Per-subsystem Badger dirs (master plan §4.20). Each is opened in
	// read-only mode so the live daemon (if any) is not disturbed. Missing
	// directories are info-only — the subsystem is simply not in use yet.
	for _, sub := range []struct{ id, name, sub string }{
		{"storage.memory", "Memory store (BadgerDB)", "memory"},
		{"storage.cron", "Cron store (BadgerDB)", "cron"},
		{"storage.automation", "Automation store (BadgerDB)", "automation"},
	} {
		sub := sub
		r.Register(doctor.SubsystemCheck{
			ID:       sub.id,
			Name:     sub.name,
			Category: doctor.CatStorage,
			Fn: func(ctx context.Context) doctor.Result {
				home, err := os.UserHomeDir()
				if err != nil {
					return failf("set $HOME", "user home: %v", err)
				}
				dir := filepath.Join(home, ".overkill", sub.sub)
				if _, err := os.Stat(dir); os.IsNotExist(err) {
					return info("no %s store yet (created on first use)", sub.sub)
				}
				// Use read-only mode so we don't fight a running daemon.
				opts := badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR).WithReadOnly(true)
				db, err := badger.Open(opts)
				if err != nil {
					return failf("stop daemon and back up "+dir+", then re-run doctor",
						"badger open: %v", err)
				}
				defer db.Close()
				// Cheap probe: count keys via iterator. Bound at 50k to keep doctor snappy.
				count := 0
				err = db.View(func(txn *badger.Txn) error {
					it := txn.NewIterator(badger.DefaultIteratorOptions)
					defer it.Close()
					for it.Rewind(); it.Valid() && count < 50_000; it.Next() {
						count++
					}
					return nil
				})
				if err != nil {
					return failf("inspect "+dir+" — Badger reports corruption", "iter: %v", err)
				}
				return okf("%s store at %s — %d keys", sub.sub, dir, count)
			},
		})
	}
}
