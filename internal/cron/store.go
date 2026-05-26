package cron

import (
	"encoding/json"
	"fmt"

	"github.com/dgraph-io/badger/v4"
)

type BadgerJobStore struct {
	db *badger.DB
}

func NewBadgerJobStore(db *badger.DB) *BadgerJobStore {
	return &BadgerJobStore{db: db}
}

func (s *BadgerJobStore) SaveJob(job *Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("cron: marshaling job: %w", err)
	}

	err = s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(jobKey(job.ID), data)
	})
	if err != nil {
		return fmt.Errorf("cron: saving job: %w", err)
	}

	return nil
}

func (s *BadgerJobStore) LoadJobs() ([]Job, error) {
	var jobs []Job

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("cron:job:")
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			var j Job
			if err := it.Item().Value(func(val []byte) error {
				return json.Unmarshal(val, &j)
			}); err != nil {
				continue
			}
			jobs = append(jobs, j)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cron: loading jobs: %w", err)
	}

	return jobs, nil
}

func (s *BadgerJobStore) DeleteJob(id string) error {
	err := s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(jobKey(id))
	})
	if err != nil {
		return fmt.Errorf("cron: deleting job: %w", err)
	}
	return nil
}
