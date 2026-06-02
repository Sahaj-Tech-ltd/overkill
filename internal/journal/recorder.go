package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type FlightRecorder struct {
	dir       string
	mu        sync.Mutex
	sessionID string
}

func NewFlightRecorder(dir string, sessionID string) *FlightRecorder {
	return &FlightRecorder{
		dir:       dir,
		sessionID: sessionID,
	}
}

func (r *FlightRecorder) Record(entryType EntryType, content string, metadata json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := Entry{
		ID:        uuid.New().String(),
		Type:      entryType,
		SessionID: r.sessionID,
		Timestamp: time.Now().UTC(),
		Content:   content,
		Metadata:  metadata,
	}

	rawDir := filepath.Join(r.dir, "raw")
	if err := os.MkdirAll(rawDir, 0o750); err != nil {
		return fmt.Errorf("journal: creating raw dir: %w", err)
	}

	filename := entry.Timestamp.Format("2006-01-02") + ".jsonl"
	path := filepath.Join(rawDir, filename)

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("journal: marshaling entry: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("journal: opening jsonl file: %w", err)
	}
	defer f.Close()

	// Cross-process file lock to prevent interleaved JSONL lines
	// when multiple processes write to the same date file.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("journal: locking jsonl file: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("journal: writing entry: %w", err)
	}
	// Fsync the flight-recorder line so a crash doesn't lose the last
	// N records sitting in the kernel buffer. The flight recorder is
	// specifically the audit-trail-of-last-resort during a crash, so
	// the durability tradeoff (~1-2ms per Record on consumer SSDs) is
	// worth it. Best-effort: if Sync fails (e.g., on a filesystem that
	// doesn't support it like 9p), the write itself still landed.
	_ = f.Sync()

	return nil
}

func (r *FlightRecorder) RecordInput(content string) error {
	return r.Record(EntryUserInput, content, nil)
}

func (r *FlightRecorder) RecordReply(content string) error {
	return r.Record(EntryAgentReply, content, nil)
}

func (r *FlightRecorder) RecordToolCall(toolName string, input json.RawMessage) error {
	return r.Record(EntryToolCall, toolName, input)
}

func (r *FlightRecorder) RecordToolResult(toolName string, output json.RawMessage) error {
	return r.Record(EntryToolResult, toolName, output)
}

func (r *FlightRecorder) RecordError(err error) error {
	return r.Record(EntryError, err.Error(), nil)
}

func (r *FlightRecorder) ReadSession(sessionID string) ([]Entry, error) {
	// Lock for the duration of the read. Without this a concurrent
	// Record (which appends + fsyncs under r.mu) could leave a
	// readFiltered traversal mid-line, surfacing a truncated JSON
	// entry as a parse error. Holding the mutex for the read serialises
	// reader vs writer at file granularity — fine, journal isn't hot.
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readFilteredLocked(func(e Entry) bool {
		return e.SessionID == sessionID
	})
}

func (r *FlightRecorder) ReadDay(date time.Time) ([]Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	targetFile := date.Format("2006-01-02") + ".jsonl"
	path := filepath.Join(r.dir, "raw", targetFile)
	return readFile(path)
}

func (r *FlightRecorder) readFiltered(filter func(Entry) bool) ([]Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readFilteredLocked(filter)
}

// readFilteredLocked assumes the caller already holds r.mu.
func (r *FlightRecorder) readFilteredLocked(filter func(Entry) bool) ([]Entry, error) {
	rawDir := filepath.Join(r.dir, "raw")
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return nil, fmt.Errorf("journal: reading raw dir: %w", err)
	}

	var result []Entry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(rawDir, entry.Name())
		fileEntries, err := readFile(path)
		if err != nil {
			log.Printf("journal: skipping unreadable file %s: %v", entry.Name(), err)
			continue
		}
		for _, e := range fileEntries {
			if filter(e) {
				result = append(result, e)
			}
		}
	}

	return result, nil
}

func readFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("journal: opening file: %w", err)
	}
	defer f.Close()

	var entries []Entry
	var skipped int
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			// Corrupt / truncated line (often from a crash mid-write).
			// Surface ONCE per file so recovery analysis can see that
			// the picture is incomplete — silently skipping was the
			// previous behaviour and hid real fault chains.
			skipped++
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("journal: scanning file: %w", err)
	}
	if skipped > 0 {
		log.Printf("journal: %s: skipped %d corrupt line(s) — recovery view may be incomplete", path, skipped)
	}

	return entries, nil
}
