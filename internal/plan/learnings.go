// Package plan also owns the durable learnings stream — the
// "what did we learn this task" record the agent fills in at
// end-of-task. Separate from §6.2 LearnTrigger (which counts
// successful recoveries by error class); this is the prose
// reflection layer.
//
// Storage: append-only JSONL at ~/.overkill/learnings/YYYY-MM-DD.jsonl
// — same shape as the flight recorder. The agent can ONLY append
// via the record_learning tool; raw writes are blocked by the
// protected-path gate.

package plan

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Learning is one reflection record. Topic is the broad area
// ("auth refactor", "wall 4 monitor"); Lesson is the takeaway in
// the agent's own words. Tags optional. ModelID lets cross-session
// queries filter by current model (§4.16) — same pattern as
// FailedHypothesis.
type Learning struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	Topic     string    `json:"topic"`
	Lesson    string    `json:"lesson"`
	Tags      []string  `json:"tags,omitempty"`
	ModelID   string    `json:"model_id,omitempty"`
}

// LearningsStore wraps the JSONL directory. Reads scan every
// daily file under the dir; writes append to today's file.
type LearningsStore struct {
	dir string
	mu  sync.Mutex
}

func NewLearningsStore(dir string) *LearningsStore {
	return &LearningsStore{dir: dir}
}

// Append writes one learning. ID is assigned if blank.
func (s *LearningsStore) Append(l Learning) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("learnings: mkdir: %w", err)
	}
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	if l.Timestamp.IsZero() {
		l.Timestamp = time.Now().UTC()
	}
	path := filepath.Join(s.dir, l.Timestamp.Format("2006-01-02")+".jsonl")
	data, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("learnings: marshal: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("learnings: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("learnings: write: %w", err)
	}
	return nil
}

// All loads every persisted learning across every day file.
func (s *LearningsStore) All() ([]Learning, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("learnings: readdir: %w", err)
	}
	var out []Learning
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		recs, err := readLearningsFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

// Search substring-matches across Topic / Lesson / Tags
// case-insensitively. Empty query returns nothing — callers wanting
// "everything" should use All.
func (s *LearningsStore) Search(query string) ([]Learning, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	var out []Learning
	for _, l := range all {
		if strings.Contains(strings.ToLower(l.Topic), query) ||
			strings.Contains(strings.ToLower(l.Lesson), query) {
			out = append(out, l)
			continue
		}
		for _, tag := range l.Tags {
			if strings.Contains(strings.ToLower(tag), query) {
				out = append(out, l)
				break
			}
		}
	}
	return out, nil
}

// SearchForModel is Search filtered to records produced by the
// given model ID. Unversioned records (no ModelID) pass through —
// see FailedHypothesisStore.SearchForModel for the same pattern.
func (s *LearningsStore) SearchForModel(query, modelID string) ([]Learning, error) {
	hits, err := s.Search(query)
	if err != nil || modelID == "" {
		return hits, err
	}
	out := hits[:0]
	for _, l := range hits {
		if l.ModelID == "" || l.ModelID == modelID {
			out = append(out, l)
		}
	}
	return out, nil
}

func readLearningsFile(path string) ([]Learning, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("learnings: open %s: %w", path, err)
	}
	defer f.Close()
	var out []Learning
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var l Learning
		if err := json.Unmarshal(line, &l); err != nil {
			continue
		}
		out = append(out, l)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("learnings: scan: %w", err)
	}
	return out, nil
}
