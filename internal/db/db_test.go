package db

import (
	"testing"
)

func TestOpen_InvalidConnString(t *testing.T) {
	// "postgres://invalid" should fail to connect but not panic.
	_, err := Open("postgres://invalid")
	if err == nil {
		t.Error("expected error for invalid connection string")
	}
}

func TestOpen_EmptyConnString(t *testing.T) {
	_, err := Open("")
	if err == nil {
		t.Error("expected error for empty connection string")
	}
}

func TestMigrate_NilDB(t *testing.T) {
	// Should panic (nil pointer dereference) — this is expected behavior
	// for a nil *sql.DB. Document this in the test.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil db")
		}
	}()
	_ = Migrate(nil)
}
