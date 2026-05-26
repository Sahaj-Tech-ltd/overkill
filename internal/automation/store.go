package automation

import (
	"encoding/json"
	"fmt"

	"github.com/dgraph-io/badger/v4"
)

type BadgerSOPStore struct {
	db *badger.DB
}

func NewBadgerSOPStore(db *badger.DB) *BadgerSOPStore {
	return &BadgerSOPStore{db: db}
}

func sopKey(id string) []byte {
	return []byte("sop:" + id)
}

func (s *BadgerSOPStore) SaveSOP(sop *SOP) error {
	data, err := json.Marshal(sop)
	if err != nil {
		return fmt.Errorf("automation: marshal SOP %s: %w", sop.ID, err)
	}

	err = s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(sopKey(sop.ID), data); err != nil {
			return fmt.Errorf("automation: write SOP %s: %w", sop.ID, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("automation: save SOP %s: %w", sop.ID, err)
	}
	return nil
}

func (s *BadgerSOPStore) LoadSOPs() ([]SOP, error) {
	var sops []SOP

	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte("sop:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			var sop SOP
			if err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &sop)
			}); err != nil {
				return fmt.Errorf("automation: unmarshal SOP: %w", err)
			}
			sops = append(sops, sop)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("automation: load SOPs: %w", err)
	}

	return sops, nil
}

func (s *BadgerSOPStore) DeleteSOP(id string) error {
	err := s.db.Update(func(txn *badger.Txn) error {
		_, err := txn.Get(sopKey(id))
		if err == badger.ErrKeyNotFound {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("automation: get SOP %s for delete: %w", id, err)
		}
		return txn.Delete(sopKey(id))
	})
	if err != nil {
		return fmt.Errorf("automation: delete SOP %s: %w", id, err)
	}
	return nil
}
