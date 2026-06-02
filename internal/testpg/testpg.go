// Package testpg provides a shared PostgreSQL test helper for packages
// that need a *sql.DB in tests. If no PG is reachable, tests are skipped.
//
// Usage:
//
//	db, cleanup := testpg.Open(t)
//	defer cleanup()
//	store, err := NewPostgresStore(db)
package testpg

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

// Open returns a *sql.DB connected to the test PostgreSQL database.
// If PG_TEST_URL or DATABASE_URL is not set or the database is
// unreachable, the test is skipped. Caller must call the returned
// cleanup function.
func Open(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	connStr := os.Getenv("PG_TEST_URL")
	if connStr == "" {
		connStr = os.Getenv("DATABASE_URL")
	}
	if connStr == "" {
		connStr = "postgres://postgres:***@localhost:5432/overkill_test?sslmode=disable"
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Skipf("testpg: cannot open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("testpg: cannot ping postgres: %v (set PG_TEST_URL or DATABASE_URL)", err)
	}

	return db, func() { db.Close() }
}
