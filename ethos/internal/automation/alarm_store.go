// Package automation — alarm persistence so alarms set during a TUI
// session survive both the TUI exiting AND the daemon restarting.
//
// Without persistence the killer use case dies on its own ground:
// "wake me when this build finishes" only works if the alarm is still
// scheduled 20 minutes later when the TUI has been closed.
//
// The store is intentionally separate from the AlarmClock so an
// in-memory store can be used in tests and so a future migration to
// SQLite or a remote store doesn't touch the clock loop.
package automation

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/dgraph-io/badger/v4"
)

// AlarmStore is the persistence surface for alarms. Save is idempotent
// (overwrite by ID). Load returns ALL alarms — pending and terminal —
// so the AlarmClock can decide what to retain on reload (typically:
// drop alarms more than 24h past their fire time to keep the store
// from growing unbounded).
type AlarmStore interface {
	Save(a *Alarm) error
	Load() ([]*Alarm, error)
	Delete(id string) error
}

// MemoryAlarmStore is the test/no-persistence implementation. Safe for
// concurrent use.
type MemoryAlarmStore struct {
	mu     sync.RWMutex
	alarms map[string]*Alarm
}

// NewMemoryAlarmStore returns an empty in-memory store.
func NewMemoryAlarmStore() *MemoryAlarmStore {
	return &MemoryAlarmStore{alarms: map[string]*Alarm{}}
}

// Save copies a so the caller mutating it after the Save can't corrupt
// the store's view.
func (s *MemoryAlarmStore) Save(a *Alarm) error {
	if a == nil {
		return fmt.Errorf("alarm store: nil alarm")
	}
	if a.ID == "" {
		return fmt.Errorf("alarm store: empty ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *a
	s.alarms[a.ID] = &cp
	return nil
}

// Load returns alarms in deterministic order — by FireAt ascending —
// so tests don't fight nondeterministic map iteration.
func (s *MemoryAlarmStore) Load() ([]*Alarm, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Alarm, 0, len(s.alarms))
	for _, a := range s.alarms {
		cp := *a
		out = append(out, &cp)
	}
	sortAlarmsByFireAt(out)
	return out, nil
}

// Delete removes the alarm by ID. No-op when missing.
func (s *MemoryAlarmStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.alarms, id)
	return nil
}

// BadgerAlarmStore is the production implementation. Same Badger DB as
// SOPs — different key prefix.
type BadgerAlarmStore struct {
	db *badger.DB
}

// NewBadgerAlarmStore wires a Badger DB. The DB lifecycle is owned by
// the caller (typically the daemon).
func NewBadgerAlarmStore(db *badger.DB) *BadgerAlarmStore {
	return &BadgerAlarmStore{db: db}
}

func alarmKey(id string) []byte { return []byte("alarm:" + id) }

// Save serializes and writes the alarm under its ID. Overwrite is the
// expected behavior so a fire callback can re-Save with Fired=true.
func (s *BadgerAlarmStore) Save(a *Alarm) error {
	if a == nil {
		return fmt.Errorf("alarm store: nil alarm")
	}
	if a.ID == "" {
		return fmt.Errorf("alarm store: empty ID")
	}
	data, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("alarm store: marshal %s: %w", a.ID, err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(alarmKey(a.ID), data)
	})
}

// Load reads every persisted alarm. Iteration is bounded by the prefix
// scan; we don't paginate because alarm counts are small (humans set
// dozens, not millions).
func (s *BadgerAlarmStore) Load() ([]*Alarm, error) {
	var out []*Alarm
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("alarm:")
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var a Alarm
				if err := json.Unmarshal(v, &a); err != nil {
					// Skip corrupt rows rather than aborting the whole
					// load — the user's pending alarms shouldn't die
					// because one row got mangled.
					return nil
				}
				out = append(out, &a)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("alarm store: load: %w", err)
	}
	sortAlarmsByFireAt(out)
	return out, nil
}

// Delete removes an alarm. Missing key is not an error.
func (s *BadgerAlarmStore) Delete(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(alarmKey(id))
	})
}

// sortAlarmsByFireAt sorts in place by FireAt ascending. Centralized
// so memory + badger stores agree on iteration order.
func sortAlarmsByFireAt(alarms []*Alarm) {
	// Bubble sort is cheap when n is small (it always is here) and we
	// avoid pulling sort.Slice's closure allocation per call. If alarm
	// counts ever cross 100, swap to sort.Slice without changing the
	// behavior.
	for i := 1; i < len(alarms); i++ {
		for j := i; j > 0 && alarms[j].FireAt.Before(alarms[j-1].FireAt); j-- {
			alarms[j], alarms[j-1] = alarms[j-1], alarms[j]
		}
	}
}
