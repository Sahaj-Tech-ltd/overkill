package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type BadgerStoreTestSuite struct {
	suite.Suite
	store *BadgerStore
	db    *badger.DB
	dir   string
	ctx   context.Context
}

func (s *BadgerStoreTestSuite) SetupTest() {
	s.ctx = context.Background()
	s.dir = s.T().TempDir()

	opts := badger.DefaultOptions(s.dir).
		WithLoggingLevel(badger.ERROR)

	db, err := badger.Open(opts)
	s.Require().NoError(err)

	s.db = db
	s.store = NewBadgerStore(db)
}

func (s *BadgerStoreTestSuite) TearDownTest() {
	s.store.Close()
}

func TestBadgerStoreTestSuite(t *testing.T) {
	suite.Run(t, new(BadgerStoreTestSuite))
}

func (s *BadgerStoreTestSuite) TestBadgerStore_StoreAndGet() {
	m := &Memory{
		Type:      MemoryEpisodic,
		Content:   "deployed v2.3 to production",
		Tags:      []string{"deploy", "production"},
		SessionID: "sess-001",
		Timestamp: time.Now().UTC(),
		Metadata:  map[string]string{"env": "prod"},
	}

	err := s.store.Store(s.ctx, m)
	s.Require().NoError(err)
	s.NotEmpty(m.ID)

	got, err := s.store.Get(s.ctx, m.ID)
	s.Require().NoError(err)

	s.Equal(m.ID, got.ID)
	s.Equal(MemoryEpisodic, got.Type)
	s.Equal("deployed v2.3 to production", got.Content)
	s.Equal([]string{"deploy", "production"}, got.Tags)
	s.Equal("sess-001", got.SessionID)
	s.Equal("prod", got.Metadata["env"])
}

func (s *BadgerStoreTestSuite) TestBadgerStore_Delete() {
	m := &Memory{
		Type:    MemorySemantic,
		Content: "project uses Go 1.24",
		Tags:    []string{"go", "version"},
	}

	err := s.store.Store(s.ctx, m)
	s.Require().NoError(err)

	err = s.store.Delete(s.ctx, m.ID)
	s.Require().NoError(err)

	_, err = s.store.Get(s.ctx, m.ID)
	s.ErrorIs(err, ErrNotFound)
}

func (s *BadgerStoreTestSuite) TestBadgerStore_List_All() {
	for i := 0; i < 5; i++ {
		m := &Memory{
			Type:    MemoryEpisodic,
			Content: fmt.Sprintf("event %d", i),
		}
		s.Require().NoError(s.store.Store(s.ctx, m))
		time.Sleep(time.Millisecond)
	}

	memories, err := s.store.List(s.ctx, ListOptions{})
	s.Require().NoError(err)
	s.Len(memories, 5)
}

func (s *BadgerStoreTestSuite) TestBadgerStore_List_ByType() {
	episodic := &Memory{Type: MemoryEpisodic, Content: "event happened"}
	semantic := &Memory{Type: MemorySemantic, Content: "fact learned"}
	procedural := &Memory{Type: MemoryProcedural, Content: "how to do X"}

	s.Require().NoError(s.store.Store(s.ctx, episodic))
	s.Require().NoError(s.store.Store(s.ctx, semantic))
	s.Require().NoError(s.store.Store(s.ctx, procedural))

	memories, err := s.store.List(s.ctx, ListOptions{Type: MemorySemantic})
	s.Require().NoError(err)
	s.Len(memories, 1)
	s.Equal(MemorySemantic, memories[0].Type)
	s.Equal("fact learned", memories[0].Content)
}

func (s *BadgerStoreTestSuite) TestBadgerStore_List_BySession() {
	sessA := &Memory{Type: MemoryEpisodic, Content: "event A", SessionID: "sess-a"}
	sessB := &Memory{Type: MemoryEpisodic, Content: "event B", SessionID: "sess-b"}
	sessA2 := &Memory{Type: MemoryEpisodic, Content: "event A2", SessionID: "sess-a"}

	s.Require().NoError(s.store.Store(s.ctx, sessA))
	s.Require().NoError(s.store.Store(s.ctx, sessB))
	s.Require().NoError(s.store.Store(s.ctx, sessA2))

	memories, err := s.store.List(s.ctx, ListOptions{SessionID: "sess-a"})
	s.Require().NoError(err)
	s.Len(memories, 2)

	ids := []string{memories[0].ID, memories[1].ID}
	s.Contains(ids, sessA.ID)
	s.Contains(ids, sessA2.ID)
}

func (s *BadgerStoreTestSuite) TestBadgerStore_List_Pagination() {
	for i := 0; i < 7; i++ {
		m := &Memory{
			Type:    MemoryEpisodic,
			Content: fmt.Sprintf("item %d", i),
		}
		s.Require().NoError(s.store.Store(s.ctx, m))
		time.Sleep(time.Millisecond)
	}

	memories, err := s.store.List(s.ctx, ListOptions{Limit: 3})
	s.Require().NoError(err)
	s.Len(memories, 3)

	memories, err = s.store.List(s.ctx, ListOptions{Limit: 3, Offset: 3})
	s.Require().NoError(err)
	s.Len(memories, 3)

	memories, err = s.store.List(s.ctx, ListOptions{Offset: 10})
	s.Require().NoError(err)
	s.Empty(memories)
}

func (s *BadgerStoreTestSuite) TestBadgerStore_Retrieve_Keyword() {
	s.Require().NoError(s.store.Store(s.ctx, &Memory{
		Type:    MemorySemantic,
		Content: "The project uses PostgreSQL for persistence",
		Tags:    []string{"database"},
	}))
	s.Require().NoError(s.store.Store(s.ctx, &Memory{
		Type:    MemorySemantic,
		Content: "Deployments happen on Fridays",
		Tags:    []string{"deploy"},
	}))
	s.Require().NoError(s.store.Store(s.ctx, &Memory{
		Type:    MemoryEpisodic,
		Content: "PostgreSQL migration ran successfully",
		Tags:    []string{"database", "migration"},
	}))

	result, err := s.store.Retrieve(s.ctx, Query{Content: "PostgreSQL"})
	s.Require().NoError(err)
	s.Equal(2, result.Total)
	for _, m := range result.Memories {
		s.Contains(m.Content, "PostgreSQL")
	}
}

func (s *BadgerStoreTestSuite) TestBadgerStore_Retrieve_Tags() {
	s.Require().NoError(s.store.Store(s.ctx, &Memory{
		Type:    MemorySemantic,
		Content: "database config",
		Tags:    []string{"database", "config"},
	}))
	s.Require().NoError(s.store.Store(s.ctx, &Memory{
		Type:    MemorySemantic,
		Content: "deploy script",
		Tags:    []string{"deploy", "script"},
	}))

	result, err := s.store.Retrieve(s.ctx, Query{
		Content: "config",
		Tags:    []string{"database"},
	})
	s.Require().NoError(err)
	s.Equal(1, result.Total)
	s.Equal("database config", result.Memories[0].Content)
}

func (s *BadgerStoreTestSuite) TestBadgerStore_Retrieve_MinRelevance() {
	s.Require().NoError(s.store.Store(s.ctx, &Memory{
		Type:    MemorySemantic,
		Content: "Go is a statically typed language",
	}))
	s.Require().NoError(s.store.Store(s.ctx, &Memory{
		Type:    MemorySemantic,
		Content: "Python uses duck typing",
	}))

	result, err := s.store.Retrieve(s.ctx, Query{
		Content:      "Go language",
		MinRelevance: 0.5,
	})
	s.Require().NoError(err)
	for _, m := range result.Memories {
		s.GreaterOrEqual(m.Relevance, 0.5)
	}
}

func (s *BadgerStoreTestSuite) TestBadgerStore_Retrieve_Limit() {
	for i := 0; i < 5; i++ {
		s.Require().NoError(s.store.Store(s.ctx, &Memory{
			Type:    MemorySemantic,
			Content: fmt.Sprintf("memory about Go programming %d", i),
		}))
	}

	result, err := s.store.Retrieve(s.ctx, Query{
		Content: "Go",
		Limit:   2,
	})
	s.Require().NoError(err)
	s.LessOrEqual(len(result.Memories), 2)
	s.Equal(5, result.Total)
}

func TestOrchestrator_Remember(t *testing.T) {
	dir := t.TempDir()
	opts := badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR)
	db, err := badger.Open(opts)
	require.NoError(t, err)
	defer db.Close()

	store := NewBadgerStore(db)
	orch := NewOrchestrator(store, nil, "")

	ctx := context.Background()
	err = orch.Remember(ctx, "learned about BadgerDB indexes", MemorySemantic, []string{"database", "badger"}, "sess-1")
	require.NoError(t, err)

	memories, err := store.List(ctx, ListOptions{SessionID: "sess-1"})
	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, "learned about BadgerDB indexes", memories[0].Content)
	assert.Equal(t, MemorySemantic, memories[0].Type)
}

func TestOrchestrator_Recall(t *testing.T) {
	dir := t.TempDir()
	opts := badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR)
	db, err := badger.Open(opts)
	require.NoError(t, err)
	defer db.Close()

	store := NewBadgerStore(db)
	orch := NewOrchestrator(store, nil, "")

	ctx := context.Background()
	require.NoError(t, orch.Remember(ctx, "project uses Chi router", MemorySemantic, []string{"router"}, ""))
	require.NoError(t, orch.Remember(ctx, "deployed to staging", MemoryEpisodic, []string{"deploy"}, ""))

	result, err := orch.Recall(ctx, "Chi router", 10)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Total)
	assert.Equal(t, "project uses Chi router", result.Memories[0].Content)
}

func TestOrchestrator_Summarize(t *testing.T) {
	dir := t.TempDir()
	opts := badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR)
	db, err := badger.Open(opts)
	require.NoError(t, err)
	defer db.Close()

	store := NewBadgerStore(db)

	mockProvider := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{
			Content: "Summary: project uses Go and BadgerDB",
			Usage:   providers.Usage{InputTokens: 50, OutputTokens: 10},
		}, nil
	})

	orch := NewOrchestrator(store, mockProvider, "test-model")

	ctx := context.Background()
	memories := []Memory{
		{Content: "project uses Go 1.24"},
		{Content: "storage is BadgerDB"},
	}

	summary, err := orch.Summarize(ctx, memories)
	require.NoError(t, err)
	assert.Equal(t, "Summary: project uses Go and BadgerDB", summary)
}

func TestOrchestrator_Summarize_Empty(t *testing.T) {
	dir := t.TempDir()
	opts := badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR)
	db, err := badger.Open(opts)
	require.NoError(t, err)
	defer db.Close()

	store := NewBadgerStore(db)
	orch := NewOrchestrator(store, nil, "")

	ctx := context.Background()
	summary, err := orch.Summarize(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, "", summary)
}

func TestOrchestrator_ExtractMemories(t *testing.T) {
	dir := t.TempDir()
	opts := badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR)
	db, err := badger.Open(opts)
	require.NoError(t, err)
	defer db.Close()

	store := NewBadgerStore(db)

	extracted := []map[string]any{
		{"type": "semantic", "content": "user prefers dark theme", "tags": []string{"ui", "preference"}},
		{"type": "procedural", "content": "always run tests before deploy", "tags": []string{"testing", "deploy"}},
	}
	extractedJSON, err := json.Marshal(extracted)
	require.NoError(t, err)

	mockProvider := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{
			Content: string(extractedJSON),
			Usage:   providers.Usage{InputTokens: 100, OutputTokens: 50},
		}, nil
	})

	orch := NewOrchestrator(store, mockProvider, "test-model")

	ctx := context.Background()
	conversation := "User: I like dark theme. Assistant: noted. User: always run tests before deploy."

	memories, err := orch.ExtractMemories(ctx, conversation, "sess-extract")
	require.NoError(t, err)
	require.Len(t, memories, 2)

	assert.Equal(t, MemorySemantic, memories[0].Type)
	assert.Equal(t, "user prefers dark theme", memories[0].Content)

	assert.Equal(t, MemoryProcedural, memories[1].Type)
	assert.Equal(t, "always run tests before deploy", memories[1].Content)

	stored, err := store.List(ctx, ListOptions{SessionID: "sess-extract"})
	require.NoError(t, err)
	assert.Len(t, stored, 2)
}

func TestMemory_Types(t *testing.T) {
	dir := t.TempDir()
	opts := badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR)
	db, err := badger.Open(opts)
	require.NoError(t, err)
	defer db.Close()

	store := NewBadgerStore(db)
	ctx := context.Background()

	episodic := &Memory{Type: MemoryEpisodic, Content: "event occurred"}
	semantic := &Memory{Type: MemorySemantic, Content: "fact is true"}
	procedural := &Memory{Type: MemoryProcedural, Content: "do this step by step"}

	require.NoError(t, store.Store(ctx, episodic))
	require.NoError(t, store.Store(ctx, semantic))
	require.NoError(t, store.Store(ctx, procedural))

	epList, err := store.List(ctx, ListOptions{Type: MemoryEpisodic})
	require.NoError(t, err)
	assert.Len(t, epList, 1)
	assert.Equal(t, MemoryEpisodic, epList[0].Type)

	semList, err := store.List(ctx, ListOptions{Type: MemorySemantic})
	require.NoError(t, err)
	assert.Len(t, semList, 1)
	assert.Equal(t, MemorySemantic, semList[0].Type)

	procList, err := store.List(ctx, ListOptions{Type: MemoryProcedural})
	require.NoError(t, err)
	assert.Len(t, procList, 1)
	assert.Equal(t, MemoryProcedural, procList[0].Type)
}
