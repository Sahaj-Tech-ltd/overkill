package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/memory"
	_ "github.com/lib/pq"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping Postgres test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("postgres open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newOrch(t *testing.T) *memory.Orchestrator {
	t.Helper()
	db := openTestDB(t)
	store, err := memory.NewPostgresStore(db)
	if err != nil {
		t.Fatalf("postgres store: %v", err)
	}
	return memory.NewOrchestrator(store, nil, "")
}

func TestMemoryTools_NilOrch(t *testing.T) {
	for _, tool := range []interface {
		Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error)
	}{
		NewMemoryRememberTool(nil),
		NewMemoryRecallTool(nil),
		NewMemoryForgetTool(nil),
	} {
		out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(string(out), "not configured") {
			t.Fatalf("unexpected: %s", out)
		}
	}
}

func TestMemoryTools_RememberThenRecall(t *testing.T) {
	o := newOrch(t)
	rt := NewMemoryRememberTool(o)
	out, err := rt.Execute(context.Background(), json.RawMessage(`{"content":"the quick brown fox","type":"semantic","tags":["test"]}`))
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	var got map[string]any
	json.Unmarshal(out, &got)
	if got["id"] == nil {
		t.Fatalf("no id in response: %s", out)
	}

	rc := NewMemoryRecallTool(o)
	out, err = rc.Execute(context.Background(), json.RawMessage(`{"query":"fox","top_k":5}`))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(string(out), "fox") {
		t.Fatalf("recall did not find content: %s", out)
	}
}

func TestMemoryTools_ValidationErrors(t *testing.T) {
	o := newOrch(t)
	cases := []struct {
		tool interface {
			Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error)
		}
		input string
		want  string
	}{
		{NewMemoryRememberTool(o), `{}`, "content is required"},
		{NewMemoryRecallTool(o), `{}`, "query is required"},
		{NewMemoryForgetTool(o), `{}`, "id is required"},
	}
	for _, tc := range cases {
		out, _ := tc.tool.Execute(context.Background(), json.RawMessage(tc.input))
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("got %s; want %q", out, tc.want)
		}
	}
}
