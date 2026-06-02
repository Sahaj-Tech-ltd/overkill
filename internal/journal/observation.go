package journal

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ObservationType string

const (
	ObsBugfix    ObservationType = "bugfix"
	ObsFeature   ObservationType = "feature"
	ObsDecision  ObservationType = "decision"
	ObsDiscovery ObservationType = "discovery"
	ObsChange    ObservationType = "change"
	ObsRefactor  ObservationType = "refactor"
)

type Observation struct {
	ID            string          `json:"id"`
	Type          ObservationType `json:"type"`
	Title         string          `json:"title"`
	Narrative     string          `json:"narrative"`
	Facts         []string        `json:"facts"`
	Concepts      []string        `json:"concepts"`
	FilesRead     []string        `json:"files_read"`
	FilesModified []string        `json:"files_modified"`
	SessionID     string          `json:"session_id"`
	Timestamp     time.Time       `json:"timestamp"`
	ContentHash   string          `json:"content_hash"`
}

type ObservationIndex struct {
	ID        string          `json:"id"`
	Type      ObservationType `json:"type"`
	Title     string          `json:"title"`
	Timestamp time.Time       `json:"timestamp"`
}

func NewObservation(obsType ObservationType, title, narrative, sessionID string) *Observation {
	o := &Observation{
		ID:        uuid.New().String(),
		Type:      obsType,
		Title:     title,
		Narrative: narrative,
		SessionID: sessionID,
		Timestamp: time.Now().UTC(),
	}
	o.ContentHash = o.ComputeHash()
	return o
}

func (o *Observation) ComputeHash() string {
	raw := o.SessionID + o.Title + o.Narrative
	h := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", h)
}

func (o *Observation) Index() ObservationIndex {
	return ObservationIndex{
		ID:        o.ID,
		Type:      o.Type,
		Title:     o.Title,
		Timestamp: o.Timestamp,
	}
}

type ObservationStore struct {
	dir string
	mu  sync.Mutex
	// hashSet caches observed ContentHash values so Store's dedup
	// check is O(1) instead of O(N) on the on-disk corpus. nil until
	// first use; populated lazily from disk on the first Store call.
	// Reset to nil by callers who externally truncate the store.
	hashSet map[string]struct{}
	// index is an in-memory map[ID]*Observation populated lazily on
	// first read and kept in sync by Store. Eliminates the O(N) disk
	// scan on every Get/List/Timeline call.
	index          map[string]*Observation
	indexPopulated bool
}

func NewObservationStore(dir string) *ObservationStore {
	return &ObservationStore{dir: dir}
}

func (s *ObservationStore) Store(obs *Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	obsDir := filepath.Join(s.dir, "observations")
	if err := os.MkdirAll(obsDir, 0o750); err != nil {
		return fmt.Errorf("journal: creating observations dir: %w", err)
	}

	// Populate the hash cache and index on first Store. Subsequent
	// calls hit the O(1) map instead of re-reading the full corpus.
	if !s.indexPopulated {
		if err := s.populateIndexLocked(); err != nil {
			return fmt.Errorf("journal: building index: %w", err)
		}
	}
	if _, dup := s.hashSet[obs.ContentHash]; dup {
		return nil
	}

	filename := "observations-" + obs.Timestamp.Format("2006-01-02") + ".jsonl"
	path := filepath.Join(obsDir, filename)

	data, err := json.Marshal(obs)
	if err != nil {
		return fmt.Errorf("journal: marshaling observation: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("journal: opening observation file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("journal: writing observation: %w", err)
	}
	s.hashSet[obs.ContentHash] = struct{}{}
	s.index[obs.ID] = obs

	return nil
}

func (s *ObservationStore) Get(id string) (*Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureIndexLocked(); err != nil {
		return nil, err
	}

	if obs, ok := s.index[id]; ok {
		return obs, nil
	}

	return nil, fmt.Errorf("journal: observation %s not found", id)
}

func (s *ObservationStore) List(obsType ObservationType, limit int) []ObservationIndex {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureIndexLocked(); err != nil {
		return nil
	}

	// Collect matching observations from the index.
	type indexed struct {
		idx ObservationIndex
		ts  time.Time
	}
	var candidates []indexed
	for _, o := range s.index {
		if obsType != "" && o.Type != obsType {
			continue
		}
		candidates = append(candidates, indexed{idx: o.Index(), ts: o.Timestamp})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ts.After(candidates[j].ts)
	})

	if limit <= 0 {
		limit = 50
	}
	if limit > len(candidates) {
		limit = len(candidates)
	}

	result := make([]ObservationIndex, limit)
	for i := 0; i < limit; i++ {
		result[i] = candidates[i].idx
	}
	return result
}

func (s *ObservationStore) Timeline(anchorID string, depth int) []Observation {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureIndexLocked(); err != nil {
		return nil
	}

	// Collect all observations sorted by timestamp.
	all := make([]*Observation, 0, len(s.index))
	for _, o := range s.index {
		all = append(all, o)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.Before(all[j].Timestamp)
	})

	anchorIdx := -1
	for i, o := range all {
		if o.ID == anchorID {
			anchorIdx = i
			break
		}
	}

	if anchorIdx == -1 {
		return nil
	}

	start := anchorIdx - depth
	if start < 0 {
		start = 0
	}

	end := anchorIdx + depth + 1
	if end > len(all) {
		end = len(all)
	}

	result := make([]Observation, end-start)
	for i := range result {
		result[i] = *all[start+i]
	}
	return result
}

// ensureIndexLocked populates the index lazily if not already done.
// Caller must hold s.mu.
func (s *ObservationStore) ensureIndexLocked() error {
	if s.indexPopulated {
		return nil
	}
	return s.populateIndexLocked()
}

// populateIndexLocked reads all observation files and builds the
// in-memory index and hashSet. Caller must hold s.mu.
func (s *ObservationStore) populateIndexLocked() error {
	all, err := s.readAllLocked()
	if err != nil {
		return err
	}
	s.hashSet = make(map[string]struct{}, len(all))
	s.index = make(map[string]*Observation, len(all))
	for i := range all {
		s.hashSet[all[i].ContentHash] = struct{}{}
		s.index[all[i].ID] = &all[i]
	}
	s.indexPopulated = true
	return nil
}

func (s *ObservationStore) readAllLocked() ([]Observation, error) {
	obsDir := filepath.Join(s.dir, "observations")

	entries, err := os.ReadDir(obsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("journal: reading observations dir: %w", err)
	}

	var result []Observation
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "observations-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		path := filepath.Join(obsDir, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var obs Observation
			if err := json.Unmarshal(line, &obs); err != nil {
				continue
			}
			result = append(result, obs)
		}
		f.Close()
	}

	return result, nil
}
