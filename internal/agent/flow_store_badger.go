// Package agent — BadgerDB-backed FlowStore for the daemon.
//
// Lives in the agent package because FlowState is the on-wire shape
// and we want one place that owns serialisation. Same Badger DB the
// daemon uses for SOPs/alarms; different key prefix.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dgraph-io/badger/v4"
)

// BadgerFlowStore persists FlowState to a Badger DB under the prefix
// "flow:". DB lifecycle is owned by the caller (the daemon).
type BadgerFlowStore struct {
	db *badger.DB
}

// NewBadgerFlowStore wires a store to an open Badger DB.
func NewBadgerFlowStore(db *badger.DB) *BadgerFlowStore {
	return &BadgerFlowStore{db: db}
}

func flowKey(id string) []byte { return []byte("flow:" + id) }

// Save serializes state and writes it under flow:<id>. Overwrites
// existing rows so successive checkpoints land in the same slot.
func (s *BadgerFlowStore) Save(state *FlowState) error {
	if state == nil {
		return fmt.Errorf("flow store: nil state")
	}
	if state.ID == "" {
		return fmt.Errorf("flow store: empty ID")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("flow store: marshal: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(flowKey(state.ID), data)
	})
}

// Load returns ErrFlowCorrupt when the stored blob can't be parsed,
// (nil, nil) when the ID is missing, and the state otherwise.
func (s *BadgerFlowStore) Load(id string) (*FlowState, error) {
	var raw []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(flowKey(id))
		if err != nil {
			return err
		}
		return item.Value(func(v []byte) error {
			raw = append([]byte(nil), v...)
			return nil
		})
	})
	if errors.Is(err, badger.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("flow store: load: %w", err)
	}
	var state FlowState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, ErrFlowCorrupt
	}
	return &state, nil
}

// Delete removes a flow record. Missing keys are no-ops.
func (s *BadgerFlowStore) Delete(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(flowKey(id))
	})
}

// List returns every parseable flow. Corrupt blobs are SKIPPED but
// NOT deleted — operator intervention may want them around for
// forensics. Production callers can choose to call Delete manually.
func (s *BadgerFlowStore) List() ([]*FlowState, error) {
	var out []*FlowState
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("flow:")
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var state FlowState
				if err := json.Unmarshal(v, &state); err != nil {
					return nil // skip corrupt rows
				}
				out = append(out, &state)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("flow store: list: %w", err)
	}
	return out, nil
}
