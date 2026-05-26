// Package walls — behavioral regression bank (master plan §6.5 Wall 3).
//
// Every shipped bug should leave a regression test behind so the same bug
// cannot re-ship. The bank persists each regression as a small record:
// what broke, the symptom, the test command that proves it's fixed. A
// `Verify` pass re-runs every recorded test command and reports any that
// fail — those are reopened bugs.
package walls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
)

// Regression is one persisted record. Created on bug-fix, verified on demand.
type Regression struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Symptom     string    `json:"symptom"`
	RootCause   string    `json:"root_cause,omitempty"`
	TestCmd     string    `json:"test_cmd"`
	CommitSHA   string    `json:"commit_sha,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastVerify  time.Time `json:"last_verified,omitempty"`
	LastResult  string    `json:"last_result,omitempty"` // "passed" | "failed" | ""
	LastFailMsg string    `json:"last_fail_msg,omitempty"`
}

// VerifyResult is one entry in the report returned by RegressionBank.Verify.
type VerifyResult struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Passed bool   `json:"passed"`
	Output string `json:"output,omitempty"`
}

// RegressionStore abstracts persistence so tests can swap in an in-memory store.
type RegressionStore interface {
	Save(r *Regression) error
	Get(id string) (*Regression, error)
	List() ([]Regression, error)
	Delete(id string) error
}

// CmdRunner runs `sh -c cmd` and returns combined output + exit error. The
// default uses os/exec; tests inject a fake.
type CmdRunner func(ctx context.Context, cmd string, timeout time.Duration) (string, error)

// DefaultCmdRunner runs the command via /bin/sh with the given timeout.
func DefaultCmdRunner(ctx context.Context, cmd string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "sh", "-c", cmd).CombinedOutput()
	return string(out), err
}

// ErrRegressionNotFound is returned when an ID is missing.
var ErrRegressionNotFound = errors.New("regression bank: not found")

// RegressionBank coordinates record/verify against a RegressionStore.
type RegressionBank struct {
	store  RegressionStore
	runner CmdRunner
	mu     sync.Mutex
}

// NewRegressionBank wires a store and a runner. Nil runner falls back to
// DefaultCmdRunner.
func NewRegressionBank(store RegressionStore, runner CmdRunner) *RegressionBank {
	if runner == nil {
		runner = DefaultCmdRunner
	}
	return &RegressionBank{store: store, runner: runner}
}

// Record persists a new regression. Title and TestCmd are required.
func (b *RegressionBank) Record(r *Regression) (*Regression, error) {
	if r == nil {
		return nil, errors.New("regression bank: nil record")
	}
	if strings.TrimSpace(r.Title) == "" {
		return nil, errors.New("regression bank: title is required")
	}
	if strings.TrimSpace(r.TestCmd) == "" {
		return nil, errors.New("regression bank: test_cmd is required")
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if err := b.store.Save(r); err != nil {
		return nil, fmt.Errorf("regression bank: save: %w", err)
	}
	return r, nil
}

// List returns every regression sorted newest-first.
func (b *RegressionBank) List() ([]Regression, error) {
	rs, err := b.store.List()
	if err != nil {
		return nil, err
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].CreatedAt.After(rs[j].CreatedAt) })
	return rs, nil
}

// Get fetches one regression.
func (b *RegressionBank) Get(id string) (*Regression, error) {
	return b.store.Get(id)
}

// Delete removes one regression (e.g. when a feature is intentionally retired).
func (b *RegressionBank) Delete(id string) error {
	return b.store.Delete(id)
}

// Verify runs every recorded TestCmd and updates LastVerify/LastResult.
// timeout is per-command. Returns one VerifyResult per regression, in the
// same order as List().
func (b *RegressionBank) Verify(ctx context.Context, timeout time.Duration) ([]VerifyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	regs, err := b.List()
	if err != nil {
		return nil, err
	}
	results := make([]VerifyResult, 0, len(regs))
	for i := range regs {
		r := regs[i]
		out, runErr := b.runner(ctx, r.TestCmd, timeout)
		passed := runErr == nil
		r.LastVerify = time.Now().UTC()
		if passed {
			r.LastResult = "passed"
			r.LastFailMsg = ""
		} else {
			r.LastResult = "failed"
			r.LastFailMsg = trimOutput(out, 1024)
		}
		_ = b.store.Save(&r)
		results = append(results, VerifyResult{
			ID:     r.ID,
			Title:  r.Title,
			Passed: passed,
			Output: trimOutput(out, 2048),
		})
	}
	return results, nil
}

func trimOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... [truncated]"
}

// --- BadgerDB-backed store ----------------------------------------------------

// BadgerRegressionStore persists regressions under the "regression:" key prefix.
type BadgerRegressionStore struct {
	db *badger.DB
}

// NewBadgerRegressionStore wraps an open Badger DB.
func NewBadgerRegressionStore(db *badger.DB) *BadgerRegressionStore {
	return &BadgerRegressionStore{db: db}
}

const regKeyPrefix = "regression:"

func regKey(id string) []byte { return []byte(regKeyPrefix + id) }

func (s *BadgerRegressionStore) Save(r *Regression) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(regKey(r.ID), data)
	})
}

func (s *BadgerRegressionStore) Get(id string) (*Regression, error) {
	var out *Regression
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(regKey(id))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrRegressionNotFound
			}
			return err
		}
		return item.Value(func(val []byte) error {
			var r Regression
			if err := json.Unmarshal(val, &r); err != nil {
				return err
			}
			out = &r
			return nil
		})
	})
	return out, err
}

func (s *BadgerRegressionStore) List() ([]Regression, error) {
	var out []Regression
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()
		prefix := []byte(regKeyPrefix)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			err := it.Item().Value(func(val []byte) error {
				var r Regression
				if err := json.Unmarshal(val, &r); err != nil {
					return err
				}
				out = append(out, r)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func (s *BadgerRegressionStore) Delete(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(regKey(id))
	})
}

// --- in-memory store (tests) --------------------------------------------------

// MemRegressionStore is a non-persistent store, mostly for tests.
type MemRegressionStore struct {
	mu   sync.Mutex
	data map[string]Regression
}

func NewMemRegressionStore() *MemRegressionStore {
	return &MemRegressionStore{data: map[string]Regression{}}
}

func (m *MemRegressionStore) Save(r *Regression) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[r.ID] = *r
	return nil
}

func (m *MemRegressionStore) Get(id string) (*Regression, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.data[id]; ok {
		return &r, nil
	}
	return nil, ErrRegressionNotFound
}

func (m *MemRegressionStore) List() ([]Regression, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Regression, 0, len(m.data))
	for _, r := range m.data {
		out = append(out, r)
	}
	return out, nil
}

func (m *MemRegressionStore) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, id)
	return nil
}
