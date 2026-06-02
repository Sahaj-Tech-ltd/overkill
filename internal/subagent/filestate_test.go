package subagent

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileStateTracker_RecordRead(t *testing.T) {
	tracker := NewFileStateTracker()
	taskID := "task-1"

	tracker.RecordRead(taskID, "/project/internal/auth.go")
	tracker.RecordRead(taskID, "/project/internal/middleware.go")

	reads := tracker.KnownReads(taskID)
	assert.Len(t, reads, 2)

	// Paths should be normalized (filepath.Abs)
	authNorm, _ := filepath.Abs("/project/internal/auth.go")
	mwNorm, _ := filepath.Abs("/project/internal/middleware.go")
	assert.Contains(t, reads, authNorm)
	assert.Contains(t, reads, mwNorm)
}

func TestFileStateTracker_KnownReadsEmpty(t *testing.T) {
	tracker := NewFileStateTracker()

	reads := tracker.KnownReads("nonexistent-task")
	assert.NotNil(t, reads)
	assert.Empty(t, reads)
}

func TestFileStateTracker_RecordWrite(t *testing.T) {
	tracker := NewFileStateTracker()
	taskID := "task-2"

	tracker.RecordWrite(taskID, "/project/internal/config.go")
	tracker.RecordWrite(taskID, "/project/internal/utils.go")

	writes := tracker.WritesByTask(taskID)
	assert.Len(t, writes, 2)

	configNorm, _ := filepath.Abs("/project/internal/config.go")
	utilsNorm, _ := filepath.Abs("/project/internal/utils.go")
	assert.Contains(t, writes, configNorm)
	assert.Contains(t, writes, utilsNorm)
}

func TestFileStateTracker_WritesSinceConflict(t *testing.T) {
	tracker := NewFileStateTracker()
	parentID := "parent"
	childID := "child"

	// Parent reads these files
	tracker.RecordRead(parentID, "/project/internal/auth.go")
	tracker.RecordRead(parentID, "/project/internal/middleware.go")
	tracker.RecordRead(parentID, "/project/internal/utils.go")

	// Child writes to auth.go (conflict) and new_file.go (no conflict)
	tracker.RecordWrite(childID, "/project/internal/auth.go")
	tracker.RecordWrite(childID, "/project/internal/new_file.go")

	parentReads := tracker.KnownReads(parentID)
	conflicts := tracker.WritesSince(childID, time.Time{}, parentReads)

	require.Len(t, conflicts, 1, "should detect exactly one conflict: auth.go")

	authNorm, _ := filepath.Abs("/project/internal/auth.go")
	assert.Contains(t, conflicts, authNorm, "conflict should be on auth.go")
}

func TestFileStateTracker_WritesSinceNoConflict(t *testing.T) {
	tracker := NewFileStateTracker()
	parentID := "parent"
	childID := "child"

	// Parent reads auth.go
	tracker.RecordRead(parentID, "/project/internal/auth.go")

	// Child writes config.go (no overlap)
	tracker.RecordWrite(childID, "/project/internal/config.go")

	parentReads := tracker.KnownReads(parentID)
	conflicts := tracker.WritesSince(childID, time.Time{}, parentReads)

	assert.Empty(t, conflicts, "no conflict when child writes a file parent never read")
}

func TestFileStateTracker_ConcurrentAccess(t *testing.T) {
	tracker := NewFileStateTracker()
	var wg sync.WaitGroup

	taskID := "task-concurrent"

	// 100 goroutines reading + 100 writing simultaneously
	for i := range 100 {
		wg.Add(2)

		go func(idx int) {
			defer wg.Done()
			tracker.RecordRead(taskID, "/project/internal/file.go")
		}(i)

		go func(idx int) {
			defer wg.Done()
			tracker.RecordWrite(taskID, "/project/internal/file.go")
		}(i)
	}

	wg.Wait()

	// If we get here without a race detector firing, the test passes.
	// Verify data integrity: at least 1 read and 1 write recorded (deduplicated)
	reads := tracker.KnownReads(taskID)
	writes := tracker.WritesByTask(taskID)
	assert.True(t, len(reads) >= 1, "should have at least 1 read recorded")
	assert.True(t, len(writes) >= 1, "should have at least 1 write recorded")
}
