package session

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type StoreTestSuite struct {
	suite.Suite
	store *PostgresStore
	db    *sql.DB
	ctx   context.Context
}

func (s *StoreTestSuite) SetupTest() {
	s.ctx = context.Background()
	s.db = openTestDB(s.T())
	s.store = NewPostgresStore(s.db)
}

func (s *StoreTestSuite) TearDownTest() {
	s.store.Close()
}

func TestStoreTestSuite(t *testing.T) {
	suite.Run(t, new(StoreTestSuite))
}

func (s *StoreTestSuite) TestNewSession() {
	sess := NewSession("/home/user/project")

	s.NotEmpty(sess.ID)
	s.Equal("/home/user/project", sess.Folder)
	s.Equal("active", sess.Status)
	s.NotZero(sess.CreatedAt)
	s.NotZero(sess.UpdatedAt)
	s.Empty(sess.ParentID)
	s.False(sess.IsSubSession())
}

func (s *StoreTestSuite) TestNewSession_SubSession() {
	sess := NewSession("/home/user/project")
	sess.ParentID = "parent-123"

	s.True(sess.IsSubSession())
}

func (s *StoreTestSuite) TestCreateLoadRoundTrip() {
	sess := NewSession("/home/user/project")
	sess.Title = "Test Session"
	sess.Model = "gpt-4o"
	sess.Provider = "openai"

	err := s.store.Create(s.ctx, sess)
	s.Require().NoError(err)

	loaded, err := s.store.Load(s.ctx, sess.ID)
	s.Require().NoError(err)

	s.Equal(sess.ID, loaded.ID)
	s.Equal("Test Session", loaded.Title)
	s.Equal("/home/user/project", loaded.Folder)
	s.Equal("gpt-4o", loaded.Model)
	s.Equal("openai", loaded.Provider)
	s.Equal("active", loaded.Status)
	s.WithinDuration(sess.CreatedAt, loaded.CreatedAt, time.Second)
	s.WithinDuration(sess.UpdatedAt, loaded.UpdatedAt, time.Second)
}

func (s *StoreTestSuite) TestCreateDuplicate() {
	sess := NewSession("/home/user/project")

	err := s.store.Create(s.ctx, sess)
	s.Require().NoError(err)

	err = s.store.Create(s.ctx, sess)
	s.ErrorIs(err, ErrExists)
}

func (s *StoreTestSuite) TestLoadNotFound() {
	_, err := s.store.Load(s.ctx, "nonexistent-id")
	s.ErrorIs(err, ErrNotFound)
}

func (s *StoreTestSuite) TestSaveUpdatesExisting() {
	sess := NewSession("/home/user/project")
	sess.Title = "Original"
	sess.TokenCount = 100

	err := s.store.Create(s.ctx, sess)
	s.Require().NoError(err)

	sess.Title = "Updated"
	sess.TokenCount = 200
	sess.CostUSD = 0.05

	err = s.store.Save(s.ctx, sess)
	s.Require().NoError(err)

	loaded, err := s.store.Load(s.ctx, sess.ID)
	s.Require().NoError(err)

	s.Equal("Updated", loaded.Title)
	s.Equal(int64(200), loaded.TokenCount)
	s.Equal(0.05, loaded.CostUSD)
	s.True(loaded.UpdatedAt.After(loaded.CreatedAt))
}

func (s *StoreTestSuite) TestSaveUpdatesStatus() {
	sess := NewSession("/home/user/project")

	err := s.store.Create(s.ctx, sess)
	s.Require().NoError(err)

	sess.Status = "closed"
	err = s.store.Save(s.ctx, sess)
	s.Require().NoError(err)

	loaded, err := s.store.Load(s.ctx, sess.ID)
	s.Require().NoError(err)
	s.Equal("closed", loaded.Status)

	sessions, err := s.store.List(s.ctx, ListOptions{Status: "closed"})
	s.Require().NoError(err)
	s.Len(sessions, 1)
	s.Equal(sess.ID, sessions[0].ID)
}

func (s *StoreTestSuite) TestSaveUpdatesFolder() {
	sess := NewSession("/home/user/old")

	err := s.store.Create(s.ctx, sess)
	s.Require().NoError(err)

	sess.Folder = "/home/user/new"
	err = s.store.Save(s.ctx, sess)
	s.Require().NoError(err)

	oldSessions, err := s.store.List(s.ctx, ListOptions{Folder: "/home/user/old"})
	s.Require().NoError(err)
	s.Empty(oldSessions)

	newSessions, err := s.store.List(s.ctx, ListOptions{Folder: "/home/user/new"})
	s.Require().NoError(err)
	s.Len(newSessions, 1)
	s.Equal(sess.ID, newSessions[0].ID)
}

func (s *StoreTestSuite) TestSaveNotFound() {
	sess := NewSession("/home/user/project")

	err := s.store.Save(s.ctx, sess)
	s.ErrorIs(err, ErrNotFound)
}

func (s *StoreTestSuite) TestDelete() {
	sess := NewSession("/home/user/project")

	err := s.store.Create(s.ctx, sess)
	s.Require().NoError(err)

	err = s.store.Delete(s.ctx, sess.ID)
	s.Require().NoError(err)

	_, err = s.store.Load(s.ctx, sess.ID)
	s.ErrorIs(err, ErrNotFound)
}

func (s *StoreTestSuite) TestDeleteNotFound() {
	err := s.store.Delete(s.ctx, "nonexistent-id")
	s.ErrorIs(err, ErrNotFound)
}

func (s *StoreTestSuite) TestListByFolder() {
	sess1 := NewSession("/home/user/project-a")
	sess1.Title = "Project A Session"
	sess2 := NewSession("/home/user/project-b")
	sess2.Title = "Project B Session"
	sess3 := NewSession("/home/user/project-a")
	sess3.Title = "Project A Session 2"

	s.Require().NoError(s.store.Create(s.ctx, sess1))
	s.Require().NoError(s.store.Create(s.ctx, sess2))
	s.Require().NoError(s.store.Create(s.ctx, sess3))

	sessions, err := s.store.List(s.ctx, ListOptions{Folder: "/home/user/project-a"})
	s.Require().NoError(err)
	s.Len(sessions, 2)

	ids := []string{sessions[0].ID, sessions[1].ID}
	s.Contains(ids, sess1.ID)
	s.Contains(ids, sess3.ID)
}

func (s *StoreTestSuite) TestListByStatus() {
	sess1 := NewSession("/home/user/project")
	sess1.Status = "active"
	sess2 := NewSession("/home/user/project")
	sess2.Status = "closed"
	sess3 := NewSession("/home/user/project")
	sess3.Status = "active"

	s.Require().NoError(s.store.Create(s.ctx, sess1))
	s.Require().NoError(s.store.Create(s.ctx, sess2))
	s.Require().NoError(s.store.Create(s.ctx, sess3))

	sessions, err := s.store.List(s.ctx, ListOptions{Status: "active"})
	s.Require().NoError(err)
	s.Len(sessions, 2)

	sessions, err = s.store.List(s.ctx, ListOptions{Status: "closed"})
	s.Require().NoError(err)
	s.Len(sessions, 1)
	s.Equal(sess2.ID, sessions[0].ID)
}

func (s *StoreTestSuite) TestListSubSessionsByParent() {
	parent := NewSession("/home/user/project")
	s.Require().NoError(s.store.Create(s.ctx, parent))

	child1 := NewSession("/home/user/project")
	child1.ParentID = parent.ID
	child1.Title = "Child 1"
	s.Require().NoError(s.store.Create(s.ctx, child1))

	child2 := NewSession("/home/user/project")
	child2.ParentID = parent.ID
	child2.Title = "Child 2"
	s.Require().NoError(s.store.Create(s.ctx, child2))

	orphan := NewSession("/home/user/project")
	orphan.Title = "Top-level"
	s.Require().NoError(s.store.Create(s.ctx, orphan))

	sessions, err := s.store.List(s.ctx, ListOptions{ParentID: parent.ID})
	s.Require().NoError(err)
	s.Len(sessions, 2)

	ids := []string{sessions[0].ID, sessions[1].ID}
	s.Contains(ids, child1.ID)
	s.Contains(ids, child2.ID)
	s.NotContains(ids, orphan.ID)
}

func (s *StoreTestSuite) TestListPagination() {
	for i := 0; i < 5; i++ {
		sess := NewSession("/home/user/project")
		sess.Title = filepath.Join("Session", string(rune('A'+i)))
		s.Require().NoError(s.store.Create(s.ctx, sess))
		time.Sleep(time.Millisecond * 2)
	}

	sessions, err := s.store.List(s.ctx, ListOptions{Limit: 3})
	s.Require().NoError(err)
	s.Len(sessions, 3)

	sessions, err = s.store.List(s.ctx, ListOptions{Limit: 3, Offset: 3})
	s.Require().NoError(err)
	s.Len(sessions, 2)

	sessions, err = s.store.List(s.ctx, ListOptions{Offset: 10})
	s.Require().NoError(err)
	s.Empty(sessions)
}

func (s *StoreTestSuite) TestListSortedByUpdatedAtDesc() {
	sess1 := NewSession("/home/user/project")
	sess1.Title = "Oldest"
	s.Require().NoError(s.store.Create(s.ctx, sess1))
	time.Sleep(time.Millisecond * 5)

	sess2 := NewSession("/home/user/project")
	sess2.Title = "Middle"
	s.Require().NoError(s.store.Create(s.ctx, sess2))
	time.Sleep(time.Millisecond * 5)

	sess3 := NewSession("/home/user/project")
	sess3.Title = "Newest"
	s.Require().NoError(s.store.Create(s.ctx, sess3))

	sessions, err := s.store.List(s.ctx, ListOptions{})
	s.Require().NoError(err)
	s.Require().Len(sessions, 3)

	s.Equal("Newest", sessions[0].Title)
	s.Equal("Middle", sessions[1].Title)
	s.Equal("Oldest", sessions[2].Title)

	for i := 1; i < len(sessions); i++ {
		s.False(sessions[i].UpdatedAt.After(sessions[i-1].UpdatedAt),
			"sessions should be sorted by UpdatedAt descending")
	}
}

func (s *StoreTestSuite) TestListAll() {
	sess1 := NewSession("/home/user/a")
	sess2 := NewSession("/home/user/b")
	sess3 := NewSession("/home/user/c")

	s.Require().NoError(s.store.Create(s.ctx, sess1))
	s.Require().NoError(s.store.Create(s.ctx, sess2))
	s.Require().NoError(s.store.Create(s.ctx, sess3))

	sessions, err := s.store.List(s.ctx, ListOptions{})
	s.Require().NoError(err)
	s.Len(sessions, 3)
}

func (s *StoreTestSuite) TestListWithAfter() {
	sess1 := NewSession("/home/user/project")
	s.Require().NoError(s.store.Create(s.ctx, sess1))

	cutoff := time.Now().UTC().Add(time.Millisecond * 10)
	time.Sleep(time.Millisecond * 20)

	sess2 := NewSession("/home/user/project")
	s.Require().NoError(s.store.Create(s.ctx, sess2))

	sessions, err := s.store.List(s.ctx, ListOptions{After: cutoff})
	s.Require().NoError(err)
	s.Len(sessions, 1)
	s.Equal(sess2.ID, sessions[0].ID)
}

func TestListSortedByUpdatedAtDesc_Table(t *testing.T) {
	db := openTestDB(t)
	store := NewPostgresStore(db)
	defer store.Close()

	ctx := context.Background()

	var sessions []*Session
	baseTime := time.Now().UTC()

	for i := 0; i < 10; i++ {
		sess := NewSession("/test/project")
		sess.Title = string(rune('A' + i))
		sess.CreatedAt = baseTime.Add(time.Duration(i) * time.Minute)
		sess.UpdatedAt = baseTime.Add(time.Duration(i) * time.Minute)
		sess.TurnCount = i
		require.NoError(t, store.Create(ctx, sess))
		sessions = append(sessions, sess)
	}

	results, err := store.List(ctx, ListOptions{})
	require.NoError(t, err)
	require.Len(t, results, 10)

	assert.True(t, sort.SliceIsSorted(results, func(i, j int) bool {
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	}), "results should be sorted by UpdatedAt descending")

	assert.Equal(t, "J", results[0].Title)
	assert.Equal(t, "A", results[9].Title)
}
