// Package journal — typed failed-hypothesis tracker (paper #48 design
// input #5).
//
// The shape we care about: the agent forms a hypothesis ("I think X is
// the bug"), tries it, and reports back that it didn't work. Today
// that signal lives only in the prose of agent_reply entries — it is
// not searchable, not surfaceable in future sessions, and tends to
// get retried verbatim a week later.
//
// FailedHypothesis is a structured record carved out of those replies.
// We extract them with conservative regexes (it's better to miss than
// to populate the store with noise) and persist them as their own
// JSONL stream alongside the flight recorder.
//
// Future readers — what the next session can do with this:
//
//   - Before exploring a fix, query "have we already tried this on
//     this codebase?" — saves a full debug loop on repeats.
//   - Group by Subject (the thing we hypothesised about) to surface
//     "we tried 4 things on auth, none worked" patterns that warrant
//     a different angle.
package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// FailedHypothesis is one "tried X, failed because Y" record.
// Subject is the noun-phrase the hypothesis was about (the file, the
// function, the system); Hypothesis is what was tried; Reason is the
// failure mode reported by the agent. EntryID points back to the
// agent_reply this was extracted from so callers can read the full
// turn for context.
type FailedHypothesis struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	EntryID    string    `json:"entry_id"`
	Timestamp  time.Time `json:"timestamp"`
	Subject    string    `json:"subject,omitempty"`
	Hypothesis string    `json:"hypothesis"`
	Reason     string    `json:"reason"`
	// ModelID tags the record with the model that produced the
	// failure. Optional (older records lack it). Used at read time
	// to filter "I've hit this wall twice" down to the CURRENT
	// model — a stale record from a previous model swap is noise.
	// See §4.16 model fingerprinting.
	ModelID string `json:"model_id,omitempty"`
}

// extraction patterns — all conservative, designed to fire only on
// explicit causal language. Each capture group is (hypothesis, reason).
// We prefer false negatives to false positives because the store is
// meant to be queried unanchored ("have we tried this?") — noise
// poisons that quickly.
var failurePatterns = []*regexp.Regexp{
	// "tried X, but it failed because Y" / "didn't work because Y"
	regexp.MustCompile(`(?i)(?:I |we )?tried ([^.,;]{6,160})(?:[,.;]+)\s*(?:but|and)?\s*(?:it )?(?:failed|didn'?t work|didn'?t help|broke)(?:\s+because|\s+since|\s*[—:-]\s*)\s+([^.\n]{6,200})`),
	// "X did not work because Y"
	regexp.MustCompile(`(?i)([A-Z][^.\n]{8,160})\s+(?:did|does) not work(?:\s+because|\s+since|\s*[—:-]\s*)\s+([^.\n]{6,200})`),
	// "attempted X, ... fails because Y"
	regexp.MustCompile(`(?i)attempted ([^.,;]{6,160})(?:[,.;]+)[^.\n]{0,80}fails?(?:\s+because|\s+since|\s*[—:-]\s*)\s+([^.\n]{6,200})`),
	// "hypothesis: X — wrong: Y"
	regexp.MustCompile(`(?i)hypothesis[:\-]\s*([^.\n]{6,160})\s*[—\-]+\s*(?:wrong|incorrect|disproven)[:\-]?\s*([^.\n]{6,200})`),
}

// subjectPattern best-effort extracts a leading noun phrase from the
// hypothesis text. We look for "the X" / "the X foo" — gives the
// caller something to group on. Empty when there's no clear subject.
var subjectPattern = regexp.MustCompile(`(?i)\bthe\s+([a-z_][a-zA-Z0-9_./\-]{2,40}(?:\s+[a-z][a-zA-Z0-9_./\-]{2,40})?)`)

// ExtractFailedHypotheses pulls structured records out of one
// agent_reply entry. Returns at most one finding per pattern match —
// duplicates within a single reply are deduped by hypothesis text.
func ExtractFailedHypotheses(e Entry) []FailedHypothesis {
	if e.Type != EntryAgentReply || e.Content == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []FailedHypothesis
	for _, p := range failurePatterns {
		for _, m := range p.FindAllStringSubmatch(e.Content, -1) {
			if len(m) < 3 {
				continue
			}
			hyp := strings.TrimSpace(m[1])
			reason := strings.TrimSpace(m[2])
			if hyp == "" || reason == "" {
				continue
			}
			key := strings.ToLower(hyp)
			if seen[key] {
				continue
			}
			seen[key] = true

			subj := ""
			if sm := subjectPattern.FindStringSubmatch(hyp); len(sm) >= 2 {
				subj = strings.TrimSpace(sm[1])
			}
			out = append(out, FailedHypothesis{
				ID:         uuid.New().String(),
				SessionID:  e.SessionID,
				EntryID:    e.ID,
				Timestamp:  e.Timestamp,
				Subject:    subj,
				Hypothesis: hyp,
				Reason:     reason,
			})
		}
	}
	return out
}

// FailedHypothesisStore is the durable, append-only stream of
// extracted records. One file per day (failed_hypotheses/YYYY-MM-DD.jsonl)
// mirroring the flight-recorder layout — same disk-budget story, same
// roll-up cadence.
type FailedHypothesisStore struct {
	dir string
	mu  sync.Mutex
}

func NewFailedHypothesisStore(dir string) *FailedHypothesisStore {
	return &FailedHypothesisStore{dir: dir}
}

func (s *FailedHypothesisStore) Append(h FailedHypothesis) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("failhypo: mkdir: %w", err)
	}
	ts := h.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	path := filepath.Join(s.dir, ts.Format("2006-01-02")+".jsonl")
	data, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("failhypo: marshal: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failhypo: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failhypo: write: %w", err)
	}
	return nil
}

// All loads every persisted record across every day file. Suitable
// for small histories; if the store grows past tens of thousands we
// add a date-range API.
func (s *FailedHypothesisStore) All() ([]FailedHypothesis, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failhypo: readdir: %w", err)
	}
	var out []FailedHypothesis
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		recs, err := readFailHypoFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

// SearchForModel is Search restricted to records produced by the
// given model ID. Empty modelID returns no filtering (same as
// Search). Records without a ModelID (older entries that predate
// fingerprinting) are INCLUDED — we'd rather show stale-but-marked
// records than silently drop them. The caller can dedupe by Subject
// if needed.
func (s *FailedHypothesisStore) SearchForModel(query, modelID string) ([]FailedHypothesis, error) {
	hits, err := s.Search(query)
	if err != nil || modelID == "" {
		return hits, err
	}
	out := hits[:0]
	for _, h := range hits {
		if h.ModelID == "" || h.ModelID == modelID {
			out = append(out, h)
		}
	}
	return out, nil
}

// Search returns records whose Subject, Hypothesis, or Reason contains
// the given query (case-insensitive). The intent is "have we tried
// this?" not "exact phrase match" — substring is good enough.
func (s *FailedHypothesisStore) Search(query string) ([]FailedHypothesis, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	var out []FailedHypothesis
	for _, h := range all {
		if strings.Contains(strings.ToLower(h.Subject), query) ||
			strings.Contains(strings.ToLower(h.Hypothesis), query) ||
			strings.Contains(strings.ToLower(h.Reason), query) {
			out = append(out, h)
		}
	}
	return out, nil
}

func readFailHypoFile(path string) ([]FailedHypothesis, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failhypo: open: %w", err)
	}
	defer f.Close()
	var out []FailedHypothesis
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var h FailedHypothesis
		if err := json.Unmarshal(line, &h); err != nil {
			continue // corrupt line, skip
		}
		out = append(out, h)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("failhypo: scan: %w", err)
	}
	return out, nil
}
