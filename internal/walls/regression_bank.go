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
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	_ "github.com/lib/pq"
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
	if err := validateRegressionCmd(cmd); err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "sh", "-c", cmd).CombinedOutput()
	return string(out), err
}

// ErrRegressionNotFound is returned when an ID is missing.
var ErrRegressionNotFound = errors.New("regression bank: not found")

// validateRegressionCmd blocks dangerous shell metacharacters in a
// regression test command. Prevents command injection via stored TestCmd
// strings that were recorded before validation was added or by
// compromised callers.
func validateRegressionCmd(cmd string) error {
	// B054: This intentionally blocks shell metacharacters like $() and {}
	// to prevent command injection in stored regression test commands. As a
	// side effect, valid shell commands that use command substitution
	// (e.g. go test $(go list ./...)) are also rejected. Users needing
	// these patterns should write a wrapper script and store the script path
	// as the test command instead.
	if strings.ContainsAny(cmd, ";&|`$(){}[]<>\n") {
		return fmt.Errorf("regression bank: test_cmd contains dangerous characters")
	}
	return nil
}

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
	if err := validateRegressionCmd(r.TestCmd); err != nil {
		return nil, err
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
		// Re-validate stored TestCmd before execution to catch
		// commands that were recorded before validation was added.
		if valErr := validateRegressionCmd(r.TestCmd); valErr != nil {
			r.LastVerify = time.Now().UTC()
			r.LastResult = "failed"
			r.LastFailMsg = valErr.Error()
			_ = b.store.Save(&r)
			results = append(results, VerifyResult{
				ID:     r.ID,
				Title:  r.Title,
				Passed: false,
				Output: "validation: " + valErr.Error(),
			})
			continue
		}
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

// --- PostgreSQL-backed store ---------------------------------------------------

// PostgresRegressionStore persists regressions in the regressions table.
type PostgresRegressionStore struct {
	db *sql.DB
}

func NewPostgresRegressionStore(db *sql.DB) (*PostgresRegressionStore, error) {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS regressions (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		symptom TEXT NOT NULL DEFAULT '',
		root_cause TEXT NOT NULL DEFAULT '',
		test_cmd TEXT NOT NULL,
		commit_sha TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_verify TIMESTAMPTZ,
		last_result TEXT NOT NULL DEFAULT '',
		last_fail_msg TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		return nil, fmt.Errorf("regression bank: create table: %w", err)
	}
	return &PostgresRegressionStore{db: db}, nil
}

func (s *PostgresRegressionStore) Save(r *Regression) error {
	_, err := s.db.Exec(
		`INSERT INTO regressions (id, title, symptom, root_cause, test_cmd, commit_sha, created_at, last_verify, last_result, last_fail_msg)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT (id) DO UPDATE SET
			title=EXCLUDED.title, symptom=EXCLUDED.symptom, root_cause=EXCLUDED.root_cause,
			test_cmd=EXCLUDED.test_cmd, commit_sha=EXCLUDED.commit_sha,
			last_verify=EXCLUDED.last_verify, last_result=EXCLUDED.last_result, last_fail_msg=EXCLUDED.last_fail_msg`,
		r.ID, r.Title, r.Symptom, r.RootCause, r.TestCmd, r.CommitSHA,
		nullTime(r.CreatedAt), nullTime(r.LastVerify), r.LastResult, r.LastFailMsg,
	)
	return err
}

func (s *PostgresRegressionStore) Get(id string) (*Regression, error) {
	var r Regression
	var created, verify sql.NullTime
	err := s.db.QueryRow(
		`SELECT id, title, symptom, root_cause, test_cmd, commit_sha, created_at, last_verify, last_result, last_fail_msg
		 FROM regressions WHERE id = $1`, id,
	).Scan(&r.ID, &r.Title, &r.Symptom, &r.RootCause, &r.TestCmd, &r.CommitSHA,
		&created, &verify, &r.LastResult, &r.LastFailMsg)
	if err == sql.ErrNoRows {
		return nil, ErrRegressionNotFound
	}
	if err != nil {
		return nil, err
	}
	if created.Valid {
		r.CreatedAt = created.Time
	}
	if verify.Valid {
		r.LastVerify = verify.Time
	}
	return &r, nil
}

func (s *PostgresRegressionStore) List() ([]Regression, error) {
	rows, err := s.db.Query(
		`SELECT id, title, symptom, root_cause, test_cmd, commit_sha, created_at, last_verify, last_result, last_fail_msg
		 FROM regressions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Regression
	for rows.Next() {
		var r Regression
		var created, verify sql.NullTime
		if err := rows.Scan(&r.ID, &r.Title, &r.Symptom, &r.RootCause, &r.TestCmd, &r.CommitSHA,
			&created, &verify, &r.LastResult, &r.LastFailMsg); err != nil {
			return nil, err
		}
		if created.Valid {
			r.CreatedAt = created.Time
		}
		if verify.Valid {
			r.LastVerify = verify.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresRegressionStore) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM regressions WHERE id = $1", id)
	return err
}

func nullTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
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
