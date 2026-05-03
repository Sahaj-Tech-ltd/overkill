package walls

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"
)

func TestRegressionBank_RecordValidation(t *testing.T) {
	b := NewRegressionBank(NewMemRegressionStore(), nil)
	cases := []struct {
		name string
		r    *Regression
		want string
	}{
		{"nil", nil, "nil record"},
		{"no-title", &Regression{TestCmd: "true"}, "title is required"},
		{"no-cmd", &Regression{Title: "x"}, "test_cmd is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := b.Record(tc.r); err == nil {
				t.Fatal("expected error")
			} else if err.Error() == "" || !contains(err.Error(), tc.want) {
				t.Fatalf("error %q missing %q", err.Error(), tc.want)
			}
		})
	}
}

func TestRegressionBank_RecordAssignsIDAndTimestamp(t *testing.T) {
	b := NewRegressionBank(NewMemRegressionStore(), nil)
	got, err := b.Record(&Regression{Title: "broken parser", TestCmd: "true"})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got.ID == "" {
		t.Fatal("ID not assigned")
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt not assigned")
	}
}

func TestRegressionBank_ListNewestFirst(t *testing.T) {
	store := NewMemRegressionStore()
	b := NewRegressionBank(store, nil)
	r1, _ := b.Record(&Regression{Title: "older", TestCmd: "true"})
	time.Sleep(10 * time.Millisecond)
	r2, _ := b.Record(&Regression{Title: "newer", TestCmd: "true"})

	list, err := b.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d want 2", len(list))
	}
	if list[0].ID != r2.ID {
		t.Fatalf("first = %s want %s (newest first)", list[0].ID, r2.ID)
	}
	if list[1].ID != r1.ID {
		t.Fatalf("second = %s want %s", list[1].ID, r1.ID)
	}
}

func TestRegressionBank_VerifyMixed(t *testing.T) {
	store := NewMemRegressionStore()
	calls := map[string]int{}
	runner := func(ctx context.Context, cmd string, _ time.Duration) (string, error) {
		calls[cmd]++
		if cmd == "fail-cmd" {
			return "boom", errors.New("exit 1")
		}
		return "ok", nil
	}
	b := NewRegressionBank(store, runner)
	pass, _ := b.Record(&Regression{Title: "fixed", TestCmd: "pass-cmd"})
	fail, _ := b.Record(&Regression{Title: "reopened", TestCmd: "fail-cmd"})

	results, err := b.Verify(context.Background(), 2*time.Second)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results", len(results))
	}
	resByID := map[string]VerifyResult{}
	for _, r := range results {
		resByID[r.ID] = r
	}
	if !resByID[pass.ID].Passed {
		t.Errorf("pass-cmd should be passing")
	}
	if resByID[fail.ID].Passed {
		t.Errorf("fail-cmd should be failing")
	}
	// Persisted state updated.
	got, _ := b.Get(fail.ID)
	if got.LastResult != "failed" || got.LastFailMsg == "" {
		t.Errorf("LastResult/Msg not updated: %+v", got)
	}
}

func TestRegressionBank_Delete(t *testing.T) {
	b := NewRegressionBank(NewMemRegressionStore(), nil)
	r, _ := b.Record(&Regression{Title: "x", TestCmd: "true"})
	if err := b.Delete(r.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := b.Get(r.ID); !errors.Is(err, ErrRegressionNotFound) {
		t.Fatalf("expected ErrRegressionNotFound, got %v", err)
	}
}

func TestBadgerRegressionStore_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	db, err := badger.Open(badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR))
	if err != nil {
		t.Fatalf("badger open: %v", err)
	}
	defer db.Close()
	store := NewBadgerRegressionStore(db)
	b := NewRegressionBank(store, nil)
	rec, err := b.Record(&Regression{Title: "persist", TestCmd: "true"})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := b.Get(rec.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "persist" {
		t.Fatalf("title roundtrip wrong: %q", got.Title)
	}
	list, _ := b.List()
	if len(list) != 1 {
		t.Fatalf("list count = %d", len(list))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && (indexOf(s, sub) >= 0)))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
