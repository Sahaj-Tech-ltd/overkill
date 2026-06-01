package learning

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

// postgresStore creates a store if DATABASE_URL is set, otherwise skips.
func postgresStore(t *testing.T, maxItems int) *Store {
	t.Helper()
	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		t.Skip("DATABASE_URL not set; skipping PostgreSQL test")
	}
	db, err := sql.Open("postgres", connString)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store, err := NewStore(db, maxItems)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestStoreSaveAndFind(t *testing.T) {
	store := postgresStore(t, 100)

	// Save a correction
	c := NewCorrection(
		"how do I install badger on Linux",
		"you should use apt-get install badger",
		"use go get github.com/dgraph-io/badger",
	)
	if err := store.Save(c); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Find it back
	results, err := store.FindCorrections("install badger linux", 3)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Context != c.Context {
		t.Errorf("context mismatch: got %q, want %q", results[0].Context, c.Context)
	}
	if results[0].Correct != c.Correct {
		t.Errorf("correct mismatch: got %q, want %q", results[0].Correct, c.Correct)
	}
}

func TestStoreDeduplication(t *testing.T) {
	store := postgresStore(t, 100)

	c1 := NewCorrection("context", "wrong", "correct v1")
	c2 := NewCorrection("context", "wrong", "correct v2")

	// Both should have the same key (same context + wrong)
	if err := store.Save(c1); err != nil {
		t.Fatalf("save c1: %v", err)
	}
	if err := store.Save(c2); err != nil {
		t.Fatalf("save c2: %v", err)
	}

	// Count should be 1 (deduped)
	count, err := store.Count()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count=1 after dedup, got %d", count)
	}

	// The stored version should be c2 (overwritten)
	results, err := store.FindCorrections("context", 3)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Correct != "correct v2" {
		t.Errorf("expected overwritten correction, got %q", results[0].Correct)
	}
}

func TestStoreLRUEviction(t *testing.T) {
	maxItems := 5
	store := postgresStore(t, maxItems)

	// Insert maxItems corrections
	for i := 0; i < maxItems; i++ {
		c := NewCorrection(
			"context "+string(rune('a'+i)),
			"wrong "+string(rune('a'+i)),
			"correct "+string(rune('a'+i)),
		)
		if err := store.Save(c); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	count, err := store.Count()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != maxItems {
		t.Errorf("expected count=%d, got %d", maxItems, count)
	}

	// Insert one more — should evict oldest
	c := NewCorrection("context new", "wrong new", "correct new")
	if err := store.Save(c); err != nil {
		t.Fatalf("save extra: %v", err)
	}

	count, err = store.Count()
	if err != nil {
		t.Fatalf("count after eviction: %v", err)
	}
	if count != maxItems {
		t.Errorf("expected count to stay at %d after eviction, got %d", maxItems, count)
	}

	// The new correction should be findable
	results, err := store.FindCorrections("context new", 3)
	if err != nil {
		t.Fatalf("find new: %v", err)
	}
	found := false
	for _, r := range results {
		if r.Correct == "correct new" {
			found = true
			break
		}
	}
	if !found {
		t.Error("new correction should be findable after eviction")
	}
}

func TestStoreFindRelevance(t *testing.T) {
	store := postgresStore(t, 100)

	// Store several corrections on different topics
	store.Save(NewCorrection(
		"how to install badger database",
		"use apt-get",
		"use go get",
	))
	store.Save(NewCorrection(
		"how to make pizza",
		"use store-bought dough",
		"make your own dough from scratch",
	))
	store.Save(NewCorrection(
		"badger configuration",
		"put config in /etc",
		"put config in ~/.overkill",
	))

	// Query about badger should rank badger corrections higher
	results, err := store.FindCorrections("badger database install", 3)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	// The pizza correction should not be the top result
	if len(results) > 0 && results[0].Context == "how to make pizza" {
		t.Error("pizza correction should not be top result for badger query")
	}
}

func TestStoreEmptyFind(t *testing.T) {
	store := postgresStore(t, 100)

	results, err := store.FindCorrections("nonexistent query", 3)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty store, got %d", len(results))
	}
}

func TestStoreClose(t *testing.T) {
	store := postgresStore(t, 100)
	if err := store.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestStoreCount(t *testing.T) {
	store := postgresStore(t, 100)

	count, err := store.Count()
	if err != nil {
		t.Fatalf("initial count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected initial count 0, got %d", count)
	}

	for i := 0; i < 3; i++ {
		c := NewCorrection(
			"context "+string(rune('a'+i)),
			"wrong "+string(rune('a'+i)),
			"correct "+string(rune('a'+i)),
		)
		if err := store.Save(c); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	count, err = store.Count()
	if err != nil {
		t.Fatalf("final count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected count 3, got %d", count)
	}
}
