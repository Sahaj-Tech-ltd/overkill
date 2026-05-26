package subagent

import (
	"path/filepath"
	"sync"
	"time"
)

// FileStateTracker tracks which files each task (agent) reads and writes.
// It detects conflicts when a child agent modifies files the parent previously read.
// All methods are safe for concurrent use by multiple goroutines.
type FileStateTracker struct {
	mu     sync.RWMutex
	reads  map[string]map[string]time.Time // taskID -> normalizedPath -> timestamp
	writes map[string]map[string]time.Time // taskID -> normalizedPath -> timestamp
}

// NewFileStateTracker creates a new, ready-to-use FileStateTracker.
func NewFileStateTracker() *FileStateTracker {
	return &FileStateTracker{
		reads:  make(map[string]map[string]time.Time),
		writes: make(map[string]map[string]time.Time),
	}
}

// RecordRead records that taskID read the file at path.
// The path is normalized to an absolute path via filepath.Abs.
func (t *FileStateTracker) RecordRead(taskID, path string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path // fallback to original if Abs fails
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.reads[taskID] == nil {
		t.reads[taskID] = make(map[string]time.Time)
	}
	t.reads[taskID][absPath] = time.Now()
}

// RecordWrite records that taskID wrote to the file at path.
// The path is normalized to an absolute path via filepath.Abs.
func (t *FileStateTracker) RecordWrite(taskID, path string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.writes[taskID] == nil {
		t.writes[taskID] = make(map[string]time.Time)
	}
	t.writes[taskID][absPath] = time.Now()
}

// KnownReads returns a copy of all normalized file paths that taskID has read.
// Returns an empty (non-nil) slice for unknown tasks.
func (t *FileStateTracker) KnownReads(taskID string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	taskReads, ok := t.reads[taskID]
	if !ok || len(taskReads) == 0 {
		return []string{}
	}

	result := make([]string, 0, len(taskReads))
	for path := range taskReads {
		result = append(result, path)
	}
	return result
}

// WritesByTask returns a copy of all normalized file paths that taskID has written to.
// Returns an empty (non-nil) slice for unknown tasks.
func (t *FileStateTracker) WritesByTask(taskID string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	taskWrites, ok := t.writes[taskID]
	if !ok || len(taskWrites) == 0 {
		return []string{}
	}

	result := make([]string, 0, len(taskWrites))
	for path := range taskWrites {
		result = append(result, path)
	}
	return result
}

// WritesSince returns the intersection of knownReads and the files written by taskID
// since the given time. This detects conflicts: files that the parent read and the child wrote.
// Pass time.Time{} (zero value) to include all writes regardless of timestamp.
func (t *FileStateTracker) WritesSince(taskID string, since time.Time, knownReads []string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	taskWrites, ok := t.writes[taskID]
	if !ok || len(taskWrites) == 0 {
		return []string{}
	}

	// Build a set from knownReads for O(1) lookup
	readSet := make(map[string]struct{}, len(knownReads))
	for _, p := range knownReads {
		readSet[p] = struct{}{}
	}

	var conflicts []string
	for path, writeTime := range taskWrites {
		if _, wasRead := readSet[path]; !wasRead {
			continue
		}
		if since.IsZero() || writeTime.After(since) {
			conflicts = append(conflicts, path)
		}
	}

	if conflicts == nil {
		return []string{}
	}
	return conflicts
}
