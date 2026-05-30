package gateway

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// openTestDB opens a Postgres connection for testing.  The caller must
// close it.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv("PG_TEST_URL"); if connStr == "" { connStr = os.Getenv("DATABASE_URL") }
	if connStr == "" { t.Skip("skipping: set PG_TEST_URL or DATABASE_URL") }
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInputHistory_AppendAndGet(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil { t.Fatalf("NewInputHistory: %v", err) }

	require.NoError(t, ih.Append(nil, "chat1", "hello"))
	require.NoError(t, ih.Append(nil, "chat1", "world"))

	got, err := ih.Get(nil, "chat1", 0) // latest
	require.NoError(t, err)
	require.Equal(t, "world", got)

	got, err = ih.Get(nil, "chat1", 1) // second latest
	require.NoError(t, err)
	require.Equal(t, "hello", got)
}

func TestInputHistory_Len(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil { t.Fatalf("NewInputHistory: %v", err) }

	n, err := ih.Len(nil, "chat1")
	require.NoError(t, err)
	require.Equal(t, 0, n)

	require.NoError(t, ih.Append(nil, "chat1", "a"))
	require.NoError(t, ih.Append(nil, "chat1", "b"))

	n, err = ih.Len(nil, "chat1")
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

func TestInputHistory_EmptyGet(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil { t.Fatalf("NewInputHistory: %v", err) }

	got, err := ih.Get(nil, "no-such-chat", 0)
	require.NoError(t, err)
	require.Equal(t, "", got)

	got, err = ih.Get(nil, "no-such-chat", 5)
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestInputHistory_OffsetOutOfBounds(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil { t.Fatalf("NewInputHistory: %v", err) }

	require.NoError(t, ih.Append(nil, "chat1", "only"))

	got, err := ih.Get(nil, "chat1", 0)
	require.NoError(t, err)
	require.Equal(t, "only", got)

	got, err = ih.Get(nil, "chat1", 1) // out of bounds
	require.NoError(t, err)
	require.Equal(t, "", got)

	got, err = ih.Get(nil, "chat1", 100)
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestInputHistory_RingBufferOverflow(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil { t.Fatalf("NewInputHistory: %v", err) }

	// Append 150 messages — only the last 100 should be retained.
	for i := range 150 {
		msg := "msg-" + string(rune('0'+i%10)) // dummy
		require.NoError(t, ih.Append(nil, "chat1", msg))
	}

	n, err := ih.Len(nil, "chat1")
	require.NoError(t, err)
	require.Equal(t, 100, n, "ring buffer must cap at 100 entries")

	// Latest entry (offset 0) should be the 150th message.
	got, err := ih.Get(nil, "chat1", 0)
	require.NoError(t, err)
	require.Equal(t, "msg-9", got) // i=149 → 149%10 = 9

	// Oldest retained entry (offset 99) is message #51 (0-indexed: i=50).
	got, err = ih.Get(nil, "chat1", 99)
	require.NoError(t, err)
	require.Equal(t, "msg-0", got) // i=50 → 50%10 = 0
}

func TestInputHistory_IndependentChatKeys(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil { t.Fatalf("NewInputHistory: %v", err) }

	require.NoError(t, ih.Append(nil, "telegram:42", "msg-a"))
	require.NoError(t, ih.Append(nil, "telegram:42", "msg-b"))
	require.NoError(t, ih.Append(nil, "discord:99", "msg-x"))

	n, _ := ih.Len(nil, "telegram:42")
	require.Equal(t, 2, n)

	n, _ = ih.Len(nil, "discord:99")
	require.Equal(t, 1, n)

	got, _ := ih.Get(nil, "telegram:42", 0)
	require.Equal(t, "msg-b", got)

	got, _ = ih.Get(nil, "discord:99", 0)
	require.Equal(t, "msg-x", got)
}

func TestInputHistory_GetHistory(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil { t.Fatalf("NewInputHistory: %v", err) }

	require.NoError(t, ih.Append(nil, "chat1", "first"))
	require.NoError(t, ih.Append(nil, "chat1", "second"))
	require.NoError(t, ih.Append(nil, "chat1", "third"))

	// Get all 3
	hist, err := ih.GetHistory(nil, "chat1", 5)
	require.NoError(t, err)
	require.Equal(t, []string{"third", "second", "first"}, hist)

	// Get just 2
	hist, err = ih.GetHistory(nil, "chat1", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"third", "second"}, hist)
}

func TestInputHistory_GetHistoryEmpty(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil { t.Fatalf("NewInputHistory: %v", err) }

	hist, err := ih.GetHistory(nil, "no-such-chat", 10)
	require.NoError(t, err)
	require.Nil(t, hist)
}

func TestInputHistory_GetHistoryZeroLimit(t *testing.T) {
	db := openTestDB(t)
	ih, err := NewInputHistory(db)
	if err != nil { t.Fatalf("NewInputHistory: %v", err) }

	require.NoError(t, ih.Append(nil, "chat1", "something"))

	hist, err := ih.GetHistory(nil, "chat1", 0)
	require.NoError(t, err)
	require.Nil(t, hist)
}
