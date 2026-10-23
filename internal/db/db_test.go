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
	if err := Migrate(nil); err == nil {
		t.Fatal("expected actionable error for nil database")
	}
}
