package testpg

import (
	"os"
	"testing"
)

func TestOpen_SkipsWhenNoPG(t *testing.T) {
	// Unset PG vars so the default connection fails and test is skipped.
	oldPG := os.Getenv("PG_TEST_URL")
	oldDB := os.Getenv("DATABASE_URL")
	os.Unsetenv("PG_TEST_URL")
	os.Unsetenv("DATABASE_URL")
	defer func() {
		if oldPG != "" {
			os.Setenv("PG_TEST_URL", oldPG)
		}
		if oldDB != "" {
			os.Setenv("DATABASE_URL", oldDB)
		}
	}()

	// This test should always skip because localhost:5432 with default
	// credentials won't work in CI. But if PG is running, it might
	// connect — in which case we just verify it works.
	db, cleanup := Open(t)
	defer cleanup()

	if db == nil {
		t.Fatal("Open returned nil db")
	}
	if err := db.Ping(); err != nil {
		t.Errorf("ping failed: %v", err)
	}
}

func TestOpen_InvalidConnString(t *testing.T) {
	os.Setenv("PG_TEST_URL", "postgres://invalid:invalid@localhost:15432/nonexistent?sslmode=disable&connect_timeout=1")
	defer os.Unsetenv("PG_TEST_URL")

	// This should skip because the connection will fail.
	db, cleanup := Open(t)
	defer cleanup()
	_ = db
	// If we get here (test was skipped), that's fine.
	// If we don't get skipped (PG happens to be running), that's also fine.
}
