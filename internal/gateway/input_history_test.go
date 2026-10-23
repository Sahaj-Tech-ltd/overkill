package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

// openTestDB opens an isolated Postgres schema and registers test cleanup.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, _ := testpg.Open(t)
	return database
}

func TestInputHistory_AppendAndGet(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil {
		t.Fatalf("NewInputHistory: %v", err)
	}

	require.NoError(t, ih.Append(t.Context(), "chat1", "hello"))
	require.NoError(t, ih.Append(t.Context(), "chat1", "world"))

	got, err := ih.Get(t.Context(), "chat1", 0) // latest
	require.NoError(t, err)
	require.Equal(t, "world", got)

	got, err = ih.Get(t.Context(), "chat1", 1) // second latest
	require.NoError(t, err)
	require.Equal(t, "hello", got)
}

func TestInputHistory_Len(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil {
		t.Fatalf("NewInputHistory: %v", err)
	}

	n, err := ih.Len(t.Context(), "chat1")
	require.NoError(t, err)
	require.Equal(t, 0, n)

	require.NoError(t, ih.Append(t.Context(), "chat1", "a"))
	require.NoError(t, ih.Append(t.Context(), "chat1", "b"))

	n, err = ih.Len(t.Context(), "chat1")
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

func TestInputHistory_EmptyGet(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil {
		t.Fatalf("NewInputHistory: %v", err)
	}

	got, err := ih.Get(t.Context(), "no-such-chat", 0)
	require.NoError(t, err)
	require.Equal(t, "", got)

	got, err = ih.Get(t.Context(), "no-such-chat", 5)
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestInputHistory_OffsetOutOfBounds(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil {
		t.Fatalf("NewInputHistory: %v", err)
	}

	require.NoError(t, ih.Append(t.Context(), "chat1", "only"))

	got, err := ih.Get(t.Context(), "chat1", 0)
	require.NoError(t, err)
	require.Equal(t, "only", got)

	got, err = ih.Get(t.Context(), "chat1", 1) // out of bounds
	require.NoError(t, err)
	require.Equal(t, "", got)

	got, err = ih.Get(t.Context(), "chat1", 100)
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestInputHistory_RingBufferOverflow(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil {
		t.Fatalf("NewInputHistory: %v", err)
	}

	// Append 150 messages — only the last 100 should be retained.
	for i := range 150 {
		msg := "msg-" + string(rune('0'+i%10)) // dummy
		require.NoError(t, ih.Append(t.Context(), "chat1", msg))
	}

	n, err := ih.Len(t.Context(), "chat1")
	require.NoError(t, err)
	require.Equal(t, 100, n, "ring buffer must cap at 100 entries")

	// Latest entry (offset 0) should be the 150th message.
	got, err := ih.Get(t.Context(), "chat1", 0)
	require.NoError(t, err)
	require.Equal(t, "msg-9", got) // i=149 → 149%10 = 9

	// Oldest retained entry (offset 99) is message #51 (0-indexed: i=50).
	got, err = ih.Get(t.Context(), "chat1", 99)
	require.NoError(t, err)
	require.Equal(t, "msg-0", got) // i=50 → 50%10 = 0
}

func TestInputHistory_IndependentChatKeys(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil {
		t.Fatalf("NewInputHistory: %v", err)
	}

	require.NoError(t, ih.Append(t.Context(), "telegram:42", "msg-a"))
	require.NoError(t, ih.Append(t.Context(), "telegram:42", "msg-b"))
	require.NoError(t, ih.Append(t.Context(), "discord:99", "msg-x"))

	n, _ := ih.Len(t.Context(), "telegram:42")
	require.Equal(t, 2, n)

	n, _ = ih.Len(t.Context(), "discord:99")
	require.Equal(t, 1, n)

	got, _ := ih.Get(t.Context(), "telegram:42", 0)
	require.Equal(t, "msg-b", got)

	got, _ = ih.Get(t.Context(), "discord:99", 0)
	require.Equal(t, "msg-x", got)
}

func TestInputHistory_GetHistory(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil {
		t.Fatalf("NewInputHistory: %v", err)
	}

	require.NoError(t, ih.Append(t.Context(), "chat1", "first"))
	require.NoError(t, ih.Append(t.Context(), "chat1", "second"))
	require.NoError(t, ih.Append(t.Context(), "chat1", "third"))

	// Get all 3
	hist, err := ih.GetHistory(t.Context(), "chat1", 5)
	require.NoError(t, err)
	require.Equal(t, []string{"third", "second", "first"}, hist)

	// Get just 2
	hist, err = ih.GetHistory(t.Context(), "chat1", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"third", "second"}, hist)
}

func TestInputHistory_GetHistoryEmpty(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil {
		t.Fatalf("NewInputHistory: %v", err)
	}

	hist, err := ih.GetHistory(t.Context(), "no-such-chat", 10)
	require.NoError(t, err)
	require.Nil(t, hist)
}

func TestInputHistory_GetHistoryZeroLimit(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil {
		t.Fatalf("NewInputHistory: %v", err)
	}

	require.NoError(t, ih.Append(t.Context(), "chat1", "something"))

	hist, err := ih.GetHistory(t.Context(), "chat1", 0)
	require.NoError(t, err)
	require.Nil(t, hist)
}

func TestInputHistory_ConcurrentFirstAppendAcrossInstances(t *testing.T) {
	db := openTestDB(t)
	first, err := NewInputHistory(db)
	require.NoError(t, err)
	second, err := NewInputHistory(db)
	require.NoError(t, err)
	const count = 64
	var workers sync.WaitGroup
	errors := make(chan error, count)
	start := make(chan struct{})
	for i := range count {
		workers.Go(func() {
			<-start
			history := first
			if i%2 != 0 {
				history = second
			}
			errors <- history.Append(t.Context(), "new-chat", fmt.Sprintf("message-%d", i))
		})
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	messages, err := first.GetHistory(t.Context(), "new-chat", count)
	require.NoError(t, err)
	require.Len(t, messages, count)
	seen := make(map[string]bool, count)
	for _, message := range messages {
		require.False(t, seen[message], "duplicate message %s", message)
		seen[message] = true
	}
	for i := range count {
		require.True(t, seen[fmt.Sprintf("message-%d", i)], "missing message %d", i)
	}
}

func TestInputHistory_CancelledOperations(t *testing.T) {
	history, err := NewInputHistory(openTestDB(t))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, history.Append(ctx, "chat", "cancelled"), context.Canceled)
	_, err = history.Get(ctx, "chat", 0)
	require.ErrorIs(t, err, context.Canceled)
	_, err = history.Len(ctx, "chat")
	require.ErrorIs(t, err, context.Canceled)
	_, err = history.GetHistory(ctx, "chat", 10)
	require.ErrorIs(t, err, context.Canceled)
	count, err := history.Len(t.Context(), "chat")
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestInputHistory_InvalidStoredDataPreserved(t *testing.T) {
	for _, data := range []string{`{"invalid": true}`, `null`, `[1, 2]`} {
		t.Run(data, func(t *testing.T) {
			db := openTestDB(t)
			history, err := NewInputHistory(db)
			require.NoError(t, err)
			_, err = db.ExecContext(t.Context(), `INSERT INTO input_history (chat_key, messages) VALUES ('chat', $1)`, data)
			require.NoError(t, err)
			require.Error(t, history.Append(t.Context(), "chat", "new message"))
			var unchanged bool
			err = db.QueryRowContext(t.Context(), `SELECT messages = $1::jsonb FROM input_history WHERE chat_key = 'chat'`, data).Scan(&unchanged)
			require.NoError(t, err)
			require.True(t, unchanged, "failed append must preserve stored data")
			_, err = history.Get(t.Context(), "chat", 0)
			require.Error(t, err)
		})
	}
}
