package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
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

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("journal: writing entry: %w", err)
	}

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
	return r.readFiltered(func(e Entry) bool {
		return e.SessionID == sessionID
	})
}

func (r *FlightRecorder) ReadDay(date time.Time) ([]Entry, error) {
	targetFile := date.Format("2006-01-02") + ".jsonl"
	path := filepath.Join(r.dir, "raw", targetFile)
	return readFile(path)
}

func (r *FlightRecorder) readFiltered(filter func(Entry) bool) ([]Entry, error) {
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
			return nil, fmt.Errorf("journal: reading file %s: %w", entry.Name(), err)
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
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("journal: scanning file: %w", err)
	}

	return entries, nil
}
