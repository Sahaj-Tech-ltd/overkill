// Package testpg provides isolated PostgreSQL schemas for integration tests.
// A configured but unavailable database is a test failure. Local runs without
// configuration skip database tests; OVERKILL_REQUIRE_TEST_DB=1 makes missing
// configuration a failure as well.
package testpg

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

// Open creates a private schema and returns a pool whose search_path points to
// it on every connection. Cleanup is registered with t and is safe to call
// explicitly as well. Tests must not change search_path or use public tables.
func Open(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	connString := os.Getenv("PG_TEST_URL")
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString == "" {
		if os.Getenv("OVERKILL_REQUIRE_TEST_DB") == "1" {
			t.Fatal("testpg: PG_TEST_URL or DATABASE_URL is required")
		}
		t.Skip("testpg: set PG_TEST_URL or DATABASE_URL to run database integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, cleanup, err := openIsolated(ctx, connString, func(err error) { t.Errorf("testpg: cleanup: %v", err) })
	if err != nil {
		t.Fatalf("testpg: initialize isolated database: %v", err)
	}
	t.Cleanup(cleanup)
	return database, cleanup
}

func schemaDSN(connString, schema string) (string, error) {
	if strings.HasPrefix(connString, "postgres://") || strings.HasPrefix(connString, "postgresql://") {
		u, err := url.Parse(connString)
		if err != nil {
			return "", fmt.Errorf("parse PostgreSQL URL: %w", err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	return connString + " search_path=" + schema, nil
}

func openIsolated(ctx context.Context, connString string, cleanupError func(error)) (*sql.DB, func(), error) {
	admin, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, nil, err
	}
	failed := true
	defer func() {
		if failed {
			admin.Close()
		}
	}()
	if err := admin.PingContext(ctx); err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, err
	}
	schema := "overkill_test_" + hex.EncodeToString(random[:])
	quoted := pq.QuoteIdentifier(schema)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		return nil, nil, fmt.Errorf("create test schema: %w", err)
	}
	var database *sql.DB
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			if database != nil {
				database.Close()
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				// Cleanup must remain visible rather than making isolation look successful.
				// The caller reports operational failures through the test's cleanup below.
				cleanupError(fmt.Errorf("schema %s: %w", schema, err))
			}
			admin.Close()
		})
	}
	scopedDSN, err := schemaDSN(connString, schema)
	if err == nil {
		database, err = sql.Open("postgres", scopedDSN)
	}
	if err == nil {
		err = database.PingContext(ctx)
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	failed = false
	return database, cleanup, nil
}
