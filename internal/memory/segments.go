// Package memory — segment-based memory (§8.2 Phase 5 #3, inspired
// by MemAgent / Yu 2025).
//
// The problem: in a massive codebase, dumping the whole context into
// a single cold blob is wasteful — most queries only need a slice
// (the auth module, or just files matching `*_test.go`, or the
// segment we used last turn). MemAgent's idea: split memory into
// labeled SEGMENTS with per-segment metadata, then load segments
// on-demand based on query relevance + recency.
//
// This package provides:
//
//   - Segment: a labeled chunk of context (file globs, a directory,
//     or an arbitrary user-defined group) with metadata for
//     retrieval scoring.
//   - SegmentStore: durable per-segment state under
//     ~/.overkill/segments/<id>.json. Touch updates last-access for
//     recency scoring; LoadFiles materializes the contents.
//   - Retrieval: top-K selection by a composite score
//     (recency × match × inverse size). Composes with the existing
//     vector path in internal/journal — the journal owns observation
//     similarity, this package owns codebase segmentation.
//
// What this is NOT: a replacement for the agent's context window.
// Segments are a *retrieval layer* on top of the filesystem. The
// agent calls segment tools, picks one, and asks Read on the files
// the segment names. No magic context compression here.
package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Segment is one labeled slice of the codebase.
type Segment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Globs is the file-pattern list that defines this segment.
	// Relative to the segment's RootDir. Standard glob syntax via
	// filepath.Match plus a `**` recursive wildcard.
	Globs []string `json:"globs"`
	// RootDir is the absolute path the globs resolve against.
	// Empty → store's default root.
	RootDir string `json:"root_dir,omitempty"`
	// Tags is operator-supplied labels for filtering.
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// LastAccessed is bumped by Touch. Recency component of the
	// retrieval score.
	LastAccessed time.Time `json:"last_accessed,omitempty"`
	// AccessCount tracks how often the segment has been loaded —
	// stable signal that this segment is "hot".
	AccessCount int `json:"access_count"`
	// CachedFileCount + CachedTotalBytes are snapshot stats from
	// the last LoadFiles call. Used by the inverse-size component
	// of the retrieval score and surfaced to the user.
	CachedFileCount  int   `json:"cached_file_count,omitempty"`
	CachedTotalBytes int64 `json:"cached_total_bytes,omitempty"`
}

// SegmentStore wraps the on-disk segment directory.
type SegmentStore struct {
	dir         string
	defaultRoot string
	mu          sync.Mutex
}

// NewSegmentStore wires the store. defaultRoot is the base path
// segments use when they don't set their own RootDir — typically
// the user's project root (passed in by the wiring layer).
func NewSegmentStore(dir, defaultRoot string) *SegmentStore {
	return &SegmentStore{dir: dir, defaultRoot: defaultRoot}
}

// Create persists a new Segment. ID is auto-assigned; CreatedAt /
// UpdatedAt are stamped. Empty Globs is rejected (a segment with
// no files is useless).
func (s *SegmentStore) Create(seg *Segment) (*Segment, error) {
	if seg == nil {
		return nil, errors.New("segments: nil segment")
	}
	if strings.TrimSpace(seg.Name) == "" {
		return nil, errors.New("segments: name required")
	}
	if len(seg.Globs) == 0 {
		return nil, errors.New("segments: at least one glob required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	dup := *seg
	dup.ID = uuid.NewString()
	dup.CreatedAt = now
	dup.UpdatedAt = now
	if err := s.saveLocked(&dup); err != nil {
		return nil, err
	}
	return &dup, nil
}

// Get returns a segment by ID, or (nil, nil) when not found.
func (s *SegmentStore) Get(id string) (*Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(id)
}

// Delete removes a segment. Idempotent.
func (s *SegmentStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, id+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("segments: delete: %w", err)
	}
	return nil
}

// Touch updates LastAccessed and bumps AccessCount. Called by
// retrieval consumers so recency reflects actual use, not just
// existence.
func (s *SegmentStore) Touch(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seg, err := s.loadLocked(id)
	if err != nil {
		return err
	}
	if seg == nil {
		return fmt.Errorf("segments: %s not found", id)
	}
	seg.LastAccessed = time.Now().UTC()
	seg.AccessCount++
	return s.saveLocked(seg)
}

// All returns every persisted segment.
func (s *SegmentStore) All() ([]*Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

// Search returns segments whose Name, Description, or Tags
// substring-match the query (case-insensitive). Empty query → all.
func (s *SegmentStore) Search(query string) ([]*Segment, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return all, nil
	}
	var out []*Segment
	for _, seg := range all {
		if strings.Contains(strings.ToLower(seg.Name), query) ||
			strings.Contains(strings.ToLower(seg.Description), query) {
			out = append(out, seg)
			continue
		}
		for _, tag := range seg.Tags {
			if strings.Contains(strings.ToLower(tag), query) {
				out = append(out, seg)
				break
			}
		}
	}
	return out, nil
}

// RankOptions tunes scoring weights. Zero-value gives sensible
// defaults: weight recency 1.0, name match 2.0, inverse size 0.5.
type RankOptions struct {
	// RecencyHalfLife is the time after which the recency score
	// drops to 0.5. Default: 24h.
	RecencyHalfLife time.Duration
	// MatchWeight scales the name/desc/tag match contribution.
	MatchWeight float64
	// RecencyWeight scales the recency contribution.
	RecencyWeight float64
	// SizeWeight scales the inverse-size contribution (smaller =
	// faster to load).
	SizeWeight float64
}

func (o *RankOptions) halfLife() time.Duration {
	if o.RecencyHalfLife > 0 {
		return o.RecencyHalfLife
	}
	return 24 * time.Hour
}

func (o *RankOptions) weights() (match, recency, size float64) {
	match = o.MatchWeight
	if match <= 0 {
		match = 2.0
	}
	recency = o.RecencyWeight
	if recency <= 0 {
		recency = 1.0
	}
	size = o.SizeWeight
	if size <= 0 {
		size = 0.5
	}
	return
}

// SegmentHit is a ranked retrieval result.
type SegmentHit struct {
	Segment *Segment
	Score   float64
}

// Rank returns the top-K segments for the query, scored by a
// composite (recency × match × inverse-size). Empty query relies
// on recency + size alone (closest to "what should I load by
// default?").
func (s *SegmentStore) Rank(query string, topK int, opts RankOptions) ([]SegmentHit, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 5
	}
	mw, rw, sw := opts.weights()
	hl := opts.halfLife()
	now := time.Now().UTC()
	q := strings.ToLower(strings.TrimSpace(query))

	hits := make([]SegmentHit, 0, len(all))
	for _, seg := range all {
		matchScore := 0.0
		if q != "" {
			if strings.Contains(strings.ToLower(seg.Name), q) {
				matchScore += 1.0
			}
			if strings.Contains(strings.ToLower(seg.Description), q) {
				matchScore += 0.5
			}
			for _, tag := range seg.Tags {
				if strings.Contains(strings.ToLower(tag), q) {
					matchScore += 0.5
					break
				}
			}
		}
		recencyScore := 0.0
		if !seg.LastAccessed.IsZero() {
			age := now.Sub(seg.LastAccessed)
			// Half-life decay: score = 2^(-age/halfLife)
			recencyScore = halfLifeDecay(age, hl)
		}
		sizeScore := 1.0
		if seg.CachedFileCount > 0 {
			// Smaller segments load faster → higher score. Cap at
			// 1.0 so a brand-new segment isn't penalized.
			sizeScore = 1.0 / (1.0 + float64(seg.CachedFileCount)/50.0)
		}
		// If there's a query, drop segments with zero match score —
		// otherwise we'd surface unrelated hot segments.
		if q != "" && matchScore == 0 {
			continue
		}
		score := matchScore*mw + recencyScore*rw + sizeScore*sw
		hits = append(hits, SegmentHit{Segment: seg, Score: score})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// halfLifeDecay returns 2^(-age/halfLife). Used for the recency
// component so very-recent segments score near 1.0 and old ones
// score asymptotically near 0.
func halfLifeDecay(age, halfLife time.Duration) float64 {
	if halfLife <= 0 || age <= 0 {
		return 1.0
	}
	// score = 2^(-age/halfLife) = exp(ln(2) * -age/halfLife)
	ratio := -float64(age) / float64(halfLife)
	// Approximate 2^x via series for small x; for our range we use
	// the math.Pow exact form to keep it simple.
	return pow2(ratio)
}

// pow2 returns 2^x for x in [-50, 1]. Sufficient for our half-life
// math without pulling math.Pow's full precision.
func pow2(x float64) float64 {
	if x >= 0 {
		return 1.0
	}
	if x < -50 {
		return 0
	}
	// Repeated halving — coarse but bounded. 50 iterations max.
	result := 1.0
	for x < 0 {
		// halve while we can
		if x <= -1 {
			result *= 0.5
			x += 1
		} else {
			// fractional remainder; linear approximation between
			// 1 (x=0) and 0.5 (x=-1)
			result *= 1.0 + x*0.5
			break
		}
	}
	if result < 0 {
		result = 0
	}
	return result
}

// LoadFiles resolves the segment's Globs to concrete files and
// returns the resulting paths. Updates CachedFileCount +
// CachedTotalBytes so future Rank calls have accurate size info.
//
// Returns paths absolute, sorted, deduplicated. Recursive `**`
// glob is supported.
func (s *SegmentStore) LoadFiles(id string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seg, err := s.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if seg == nil {
		return nil, fmt.Errorf("segments: %s not found", id)
	}
	root := seg.RootDir
	if root == "" {
		root = s.defaultRoot
	}
	if root == "" {
		return nil, errors.New("segments: no root dir set (segment or store)")
	}
	seen := map[string]bool{}
	var paths []string
	var totalBytes int64
	for _, glob := range seg.Globs {
		matches, err := expandGlob(root, glob)
		if err != nil {
			return nil, fmt.Errorf("segments: glob %q: %w", glob, err)
		}
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			if info, err := os.Stat(m); err == nil && !info.IsDir() {
				paths = append(paths, m)
				totalBytes += info.Size()
			}
		}
	}
	sort.Strings(paths)

	// Cache stats. Mutex still held — safe to persist.
	seg.CachedFileCount = len(paths)
	seg.CachedTotalBytes = totalBytes
	seg.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(seg); err != nil {
		return nil, fmt.Errorf("segments: persist cache: %w", err)
	}
	return paths, nil
}

// expandGlob supports a single `**` recursive wildcard plus
// filepath.Match patterns. Falls back to filepath.Glob when there's
// no `**`. The recursive path uses filepath.Walk with a prefix +
// suffix match.
func expandGlob(root, pattern string) ([]string, error) {
	if !strings.Contains(pattern, "**") {
		return filepath.Glob(filepath.Join(root, pattern))
	}
	// Split around the first `**`.
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimRight(parts[0], string(filepath.Separator))
	suffix := strings.TrimLeft(parts[1], string(filepath.Separator))
	base := filepath.Join(root, prefix)
	var out []string
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip unreadable subtrees rather than aborting the walk.
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if suffix == "" {
			out = append(out, path)
			return nil
		}
		if ok, _ := filepath.Match("*"+suffix, filepath.Base(path)); ok {
			out = append(out, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return out, nil
}

// ── internals ───────────────────────────────────────────────────────

func (s *SegmentStore) saveLocked(seg *Segment) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("segments: mkdir: %w", err)
	}
	path := filepath.Join(s.dir, seg.ID+".json")
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(seg, "", "  ")
	if err != nil {
		return fmt.Errorf("segments: marshal: %w", err)
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("segments: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("segments: rename: %w", err)
	}
	return nil
}

func (s *SegmentStore) loadLocked(id string) (*Segment, error) {
	if id == "" {
		return nil, errors.New("segments: empty id")
	}
	path := filepath.Join(s.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("segments: read: %w", err)
	}
	var seg Segment
	if err := json.Unmarshal(data, &seg); err != nil {
		return nil, fmt.Errorf("segments: parse %s: %w", id, err)
	}
	return &seg, nil
}

func (s *SegmentStore) listLocked() ([]*Segment, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("segments: readdir: %w", err)
	}
	out := make([]*Segment, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		seg, err := s.loadLocked(id)
		if err != nil {
			continue
		}
		if seg != nil {
			out = append(out, seg)
		}
	}
	return out, nil
}
