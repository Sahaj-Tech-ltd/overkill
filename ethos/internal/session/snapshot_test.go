package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type SnapshotSuite struct {
	suite.Suite
	store *BadgerStore
	dir   string
	snapDir string
	ctx   context.Context
	sm    *SnapshotManager
}

func (s *SnapshotSuite) SetupTest() {
	s.ctx = context.Background()
	s.dir = s.T().TempDir()
	s.snapDir = filepath.Join(s.dir, "snapshots")

	store, err := NewBadgerStore(s.dir)
	s.Require().NoError(err)
	s.store = store
	s.sm = NewSnapshotManager(s.store, s.snapDir, 3)
}

func (s *SnapshotSuite) TearDownTest() {
	s.store.Close()
}

func TestSnapshotSuite(t *testing.T) {
	suite.Run(t, new(SnapshotSuite))
}

func (s *SnapshotSuite) TestCreateSnapshot_CreatesFile() {
	sess := NewSession("/home/user/project")
	sess.Title = "Snapshot Test"
	s.Require().NoError(s.store.Create(s.ctx, sess))

	path, err := s.sm.CreateSnapshot(s.ctx)
	s.Require().NoError(err)
	s.NotEmpty(path)

	_, err = os.Stat(path)
	s.NoError(err)

	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.True(strings.Contains(string(raw), `"version"`))
	s.True(strings.Contains(string(raw), `"entries"`))
}

func (s *SnapshotSuite) TestRestoreFromSnapshot_RecoverData() {
	sess := NewSession("/home/user/project")
	sess.Title = "Before Snapshot"
	sess.Model = "gpt-4o"
	s.Require().NoError(s.store.Create(s.ctx, sess))

	path, err := s.sm.CreateSnapshot(s.ctx)
	s.Require().NoError(err)

	s.Require().NoError(s.store.Delete(s.ctx, sess.ID))

	_, err = s.store.Load(s.ctx, sess.ID)
	s.ErrorIs(err, ErrNotFound)

	s.Require().NoError(s.sm.RestoreFromSnapshot(s.ctx, path))

	loaded, err := s.store.Load(s.ctx, sess.ID)
	s.Require().NoError(err)
	s.Equal("Before Snapshot", loaded.Title)
	s.Equal("gpt-4o", loaded.Model)
}

func (s *SnapshotSuite) TestPruneOld_KeepsMaxSnapshots() {
	for i := 0; i < 5; i++ {
		sess := NewSession("/home/user/project")
		sess.Title = "Session"
		s.Require().NoError(s.store.Create(s.ctx, sess))
		_, err := s.sm.CreateSnapshot(s.ctx)
		s.Require().NoError(err)
		time.Sleep(10 * time.Millisecond)
	}

	names, err := s.sm.ListSnapshots()
	s.Require().NoError(err)
	s.Len(names, 3)
}

func (s *SnapshotSuite) TestListSnapshots_ReturnsSorted() {
	for i := 0; i < 3; i++ {
		sess := NewSession("/home/user/project")
		sess.Title = "Session"
		s.Require().NoError(s.store.Create(s.ctx, sess))
		_, err := s.sm.CreateSnapshot(s.ctx)
		s.Require().NoError(err)
		time.Sleep(10 * time.Millisecond)
	}

	names, err := s.sm.ListSnapshots()
	s.Require().NoError(err)
	s.Len(names, 3)

	for i := 1; i < len(names); i++ {
		s.True(names[i-1] >= names[i], "snapshots should be sorted newest first")
	}
}

func (s *SnapshotSuite) TestRestoreFromSnapshot_FileNotFound() {
	err := s.sm.RestoreFromSnapshot(s.ctx, "/nonexistent/path/snapshot.json")
	s.Error(err)
}

func (s *SnapshotSuite) TestSnapshot_EmptyDB() {
	path, err := s.sm.CreateSnapshot(s.ctx)
	s.Require().NoError(err)
	s.NotEmpty(path)

	raw, err := os.ReadFile(path)
	s.Require().NoError(err)

	var data snapshotData
	s.Require().NoError(json.Unmarshal(raw, &data))
	s.Empty(data.Entries)
}
