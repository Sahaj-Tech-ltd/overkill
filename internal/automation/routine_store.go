// Package automation — routine persistence (§7.1 Layer 4).
//
// Mirrors the AlarmStore shape: a small Save/Load/Delete interface
// with a MemoryRoutineStore for tests and a BadgerRoutineStore for
// the daemon. Same Badger DB as alarms + SOPs, different key prefix.
//
// Why persist: routines are user-defined automation rules ("when
// build_success fires, notify Slack"). Losing them across a daemon
// restart turns the feature into a parlor trick. Persisting also
// captures LastFired / FireCount so cooldowns survive reboots —
// otherwise restarting the daemon would let an event re-fire a
// routine that was still in its cooldown window.
package automation

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/dgraph-io/badger/v4"
)

// RoutineStore is the minimal surface the engine uses. Concrete
// implementations must be concurrent-safe — the engine calls Save
// from inside its own mutex.
type RoutineStore interface {
	Save(r *Routine) error
	Load() ([]*Routine, error)
	Delete(id string) error
}

// MemoryRoutineStore is the test/no-persistence impl.
type MemoryRoutineStore struct {
	mu       sync.RWMutex
	routines map[string]*Routine
}

func NewMemoryRoutineStore() *MemoryRoutineStore {
	return &MemoryRoutineStore{routines: map[string]*Routine{}}
}

func (s *MemoryRoutineStore) Save(r *Routine) error {
	if r == nil {
		return fmt.Errorf("routine store: nil routine")
	}
	if r.ID == "" {
		return fmt.Errorf("routine store: empty ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *r
	s.routines[r.ID] = &cp
	return nil
}

func (s *MemoryRoutineStore) Load() ([]*Routine, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Routine, 0, len(s.routines))
	for _, r := range s.routines {
		cp := *r
		out = append(out, &cp)
	}
	sortRoutinesByID(out)
	return out, nil
}

func (s *MemoryRoutineStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.routines, id)
	return nil
}

// BadgerRoutineStore persists routines under the "routine:" prefix
// of the same Badger DB the daemon uses for alarms + SOPs. DB
// lifecycle is owned by the caller.
type BadgerRoutineStore struct {
	db *badger.DB
}

func NewBadgerRoutineStore(db *badger.DB) *BadgerRoutineStore {
	return &BadgerRoutineStore{db: db}
}

func routineKey(id string) []byte { return []byte("routine:" + id) }

func (s *BadgerRoutineStore) Save(r *Routine) error {
	if r == nil {
		return fmt.Errorf("routine store: nil routine")
	}
	if r.ID == "" {
		return fmt.Errorf("routine store: empty ID")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("routine store: marshal %s: %w", r.ID, err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(routineKey(r.ID), data)
	})
}

func (s *BadgerRoutineStore) Load() ([]*Routine, error) {
	var out []*Routine
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("routine:")
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var r Routine
				if err := json.Unmarshal(v, &r); err != nil {
					return fmt.Errorf("routine store: parse %s: %w", item.Key(), err)
				}
				out = append(out, &r)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortRoutinesByID(out)
	return out, nil
}

func (s *BadgerRoutineStore) Delete(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(routineKey(id))
	})
}

// sortRoutinesByID is a deterministic ordering so the CLI and
// boot-replay always see routines in the same order.
func sortRoutinesByID(rs []*Routine) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
}
