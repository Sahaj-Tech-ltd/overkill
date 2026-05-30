// Package playbooks — ACE-style evolving playbooks (§8.2 Phase 5
// #6, inspired by Zhang 2025 Agentic Context Engineering).
//
// What's a playbook? A stored prompt pattern for a recurring task
// shape: "how to do a database migration", "how to onboard a new
// dependency", "how to refactor a multi-package change". When the
// user says "let's add a new feature", the agent ranks playbooks
// by task-type match + recent success rate + recency, picks the
// best, and uses it. After the task, the agent records the
// outcome — success bumps the success counter, failure bumps the
// failure counter. Over time, "winning" playbooks float to the
// top.
//
// Difference from skills + standing orders:
//
//   - Skills are static how-tos (loaded once, not ranked).
//   - Standing orders are always-on rules ("never auto-commit").
//   - Playbooks are SELECTED per task and TRACKED for success rate.
//
// What this is NOT (yet): the auto-refinement loop. ACE proposes
// playbook edits based on what worked in practice. For now the
// agent proposes new playbooks (via playbook_create) and the user
// reviews; full auto-refinement is a follow-up that needs a
// review gate.
package playbooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/google/uuid"
)

// Playbook is one stored pattern.
type Playbook struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// TaskTypes is the list of task-type tags this playbook
	// matches. "migration", "refactor", "onboard", etc. Match by
	// substring during Rank.
	TaskTypes []string `json:"task_types"`
	// Content is the prompt-shaped body the agent injects when it
	// picks this playbook. Markdown, code blocks, whatever the
	// agent needs.
	Content string `json:"content"`
	// Tags is operator-supplied additional labels.
	Tags []string `json:"tags,omitempty"`
	// SuccessCount / FailureCount track outcomes via
	// RecordOutcome. Used for the success-rate component of Rank.
	SuccessCount int `json:"success_count"`
	FailureCount int `json:"failure_count"`
	// UseCount is incremented by Use (the playbook was selected).
	// Independent of outcomes so we can tell "popular but failing"
	// from "rarely used".
	UseCount int `json:"use_count"`
	// LastUsedAt for recency scoring.
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	// ParentID, when set, links a refinement to the playbook it
	// evolved from. Lets readers walk the refinement chain.
	ParentID string `json:"parent_id,omitempty"`
}

// SuccessRate returns SuccessCount / (Success + Failure). When
// neither counter is set, returns 0.5 — neutral prior so brand-new
// playbooks aren't penalized.
func (p *Playbook) SuccessRate() float64 {
	total := p.SuccessCount + p.FailureCount
	if total == 0 {
		return 0.5
	}
	return float64(p.SuccessCount) / float64(total)
}

// Store persists playbooks on disk as JSON-per-file under dir.
type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Create persists a new playbook. ID + timestamps are auto-assigned.
// Rejects empty Name / Content / TaskTypes.
func (s *Store) Create(pb *Playbook) (*Playbook, error) {
	if pb == nil {
		return nil, errors.New("playbooks: nil playbook")
	}
	if strings.TrimSpace(pb.Name) == "" {
		return nil, errors.New("playbooks: name required")
	}
	if strings.TrimSpace(pb.Content) == "" {
		return nil, errors.New("playbooks: content required")
	}
	if len(pb.TaskTypes) == 0 {
		return nil, errors.New("playbooks: at least one task type required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	dup := *pb
	dup.ID = uuid.NewString()
	dup.CreatedAt = now
	dup.UpdatedAt = now
	if err := s.saveLocked(&dup); err != nil {
		return nil, err
	}
	return &dup, nil
}

// Get returns a playbook by ID, or (nil, nil) when not found.
func (s *Store) Get(id string) (*Playbook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(id)
}

// Delete removes a playbook. Idempotent.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := security.SafePath(s.dir, id+".json")
	if err != nil {
		return fmt.Errorf("playbooks: delete: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("playbooks: delete: %w", err)
	}
	return nil
}

// Use records that the playbook was selected for use. Bumps UseCount
// and LastUsedAt. Does NOT touch the success/failure counters —
// caller follows up with RecordOutcome after the task completes.
func (s *Store) Use(id string) (*Playbook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pb, err := s.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if pb == nil {
		return nil, fmt.Errorf("playbooks: %s not found", id)
	}
	pb.UseCount++
	pb.LastUsedAt = time.Now().UTC()
	pb.UpdatedAt = pb.LastUsedAt
	if err := s.saveLocked(pb); err != nil {
		return nil, err
	}
	return pb, nil
}

// RecordOutcome bumps success or failure counter. Idempotent
// is NOT the goal here — multiple calls accumulate, since one
// task can have multiple recorded outcomes (e.g. the agent self-
// evaluates after each subtask).
func (s *Store) RecordOutcome(id string, success bool) (*Playbook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pb, err := s.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if pb == nil {
		return nil, fmt.Errorf("playbooks: %s not found", id)
	}
	if success {
		pb.SuccessCount++
	} else {
		pb.FailureCount++
	}
	pb.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(pb); err != nil {
		return nil, err
	}
	return pb, nil
}

// Refine creates a child playbook derived from parentID with new
// Content + (optionally) updated metadata. The parent's counters
// stay intact; the child starts fresh. Use this when the agent
// notices a playbook would have worked with a tweak.
//
// B137: Chains are capped at 5 levels deep. Attempting to refine a
// playbook that is already the 5th generation returns an error to
// prevent unbounded parent chains.
func (s *Store) Refine(parentID string, content, description string) (*Playbook, error) {
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("playbooks: refined content required")
	}
	s.mu.Lock()
	parent, err := s.loadLocked(parentID)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if parent == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("playbooks: parent %s not found", parentID)
	}

	// Depth check: walk parent chain to count generations.
	depth := 1
	current := parent
	for current.ParentID != "" && depth <= 6 {
		depth++
		if depth > 5 {
			s.mu.Unlock()
			return nil, fmt.Errorf("playbooks: refinement depth exceeded (max 5 generations)")
		}
		// Walk up one level without recursion to avoid stack growth.
		ancestor, err := s.loadLocked(current.ParentID)
		if err != nil || ancestor == nil {
			break
		}
		current = ancestor
	}
	s.mu.Unlock()

	child := &Playbook{
		Name:        parent.Name + " (refined)",
		Description: description,
		TaskTypes:   append([]string(nil), parent.TaskTypes...),
		Content:     content,
		Tags:        append([]string(nil), parent.Tags...),
		ParentID:    parent.ID,
	}
	return s.Create(child)
}

// All returns every persisted playbook. No ordering guarantee.
func (s *Store) All() ([]*Playbook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

// RankOptions tunes the rank scoring weights.
type RankOptions struct {
	// MatchWeight: how much task-type / name / desc match counts.
	// Default 2.0.
	MatchWeight float64
	// SuccessWeight: how much SuccessRate counts. Default 1.5.
	SuccessWeight float64
	// RecencyWeight: how much LastUsedAt half-life counts.
	// Default 0.5.
	RecencyWeight float64
	// RecencyHalfLife after which the recency component drops to
	// 0.5. Default 7 days — playbooks decay slower than memory
	// segments.
	RecencyHalfLife time.Duration
	// UseCountWeight: a tiny bump for popular playbooks so a
	// well-used-but-mixed-record playbook still beats an untested
	// neutral-prior one. Default 0.1.
	UseCountWeight float64
}

func (o *RankOptions) weights() (m, s, r, u float64) {
	m = o.MatchWeight
	if m <= 0 {
		m = 2.0
	}
	s = o.SuccessWeight
	if s <= 0 {
		s = 1.5
	}
	r = o.RecencyWeight
	if r <= 0 {
		r = 0.5
	}
	u = o.UseCountWeight
	if u <= 0 {
		u = 0.1
	}
	return
}

func (o *RankOptions) halfLife() time.Duration {
	if o.RecencyHalfLife > 0 {
		return o.RecencyHalfLife
	}
	return 7 * 24 * time.Hour
}

// Hit is a ranked retrieval result.
type Hit struct {
	Playbook *Playbook
	Score    float64
}

// Rank returns top-K playbooks for the given task type / query.
// taskType is the primary input (matched against TaskTypes); query
// is an optional free-form string that bumps matches against Name /
// Description / Tags. Empty taskType ranks by query alone.
func (s *Store) Rank(taskType, query string, topK int, opts RankOptions) ([]Hit, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 5
	}
	mw, sw, rw, uw := opts.weights()
	hl := opts.halfLife()
	tt := strings.ToLower(strings.TrimSpace(taskType))
	q := strings.ToLower(strings.TrimSpace(query))
	now := time.Now().UTC()

	hits := make([]Hit, 0, len(all))
	for _, pb := range all {
		match := 0.0
		if tt != "" {
			for _, t := range pb.TaskTypes {
				if strings.ToLower(t) == tt {
					match += 1.0
					break
				}
				if strings.Contains(strings.ToLower(t), tt) {
					match += 0.5
					break
				}
			}
		}
		if q != "" {
			if strings.Contains(strings.ToLower(pb.Name), q) {
				match += 0.5
			}
			if strings.Contains(strings.ToLower(pb.Description), q) {
				match += 0.3
			}
			for _, tag := range pb.Tags {
				if strings.Contains(strings.ToLower(tag), q) {
					match += 0.3
					break
				}
			}
		}
		// When BOTH inputs are empty, give a baseline so we still
		// return something useful (recency-sorted defaults).
		if tt == "" && q == "" {
			match = 0.5
		} else if match == 0 {
			// No match → skip; don't pollute the result list.
			continue
		}

		recencyScore := 0.0
		if !pb.LastUsedAt.IsZero() {
			recencyScore = halfLifeDecay(now.Sub(pb.LastUsedAt), hl)
		}
		successScore := pb.SuccessRate() // 0..1
		useScore := 0.0
		if pb.UseCount > 0 {
			// Diminishing returns: 1 use → ~0.5, 10 → ~0.95.
			useScore = 1.0 - 1.0/(1.0+float64(pb.UseCount))
		}
		score := match*mw + successScore*sw + recencyScore*rw + useScore*uw
		hits = append(hits, Hit{Playbook: pb, Score: score})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// halfLifeDecay returns 2^(-age/halfLife) using math.Exp2 for accuracy.
// Bounded [0, 1].
func halfLifeDecay(age, halfLife time.Duration) float64 {
	if halfLife <= 0 || age <= 0 {
		return 1.0
	}
	return math.Exp2(-float64(age) / float64(halfLife))
}

// ── internals ───────────────────────────────────────────────────────

func (s *Store) saveLocked(pb *Playbook) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("playbooks: mkdir: %w", err)
	}
	path, err := security.SafePath(s.dir, pb.ID+".json")
	if err != nil {
		return fmt.Errorf("playbooks: save: %w", err)
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(pb, "", "  ")
	if err != nil {
		return fmt.Errorf("playbooks: marshal: %w", err)
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("playbooks: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("playbooks: rename: %w", err)
	}
	return nil
}

func (s *Store) loadLocked(id string) (*Playbook, error) {
	if id == "" {
		return nil, errors.New("playbooks: empty id")
	}
	path, err := security.SafePath(s.dir, id+".json")
	if err != nil {
		return nil, fmt.Errorf("playbooks: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("playbooks: read: %w", err)
	}
	var pb Playbook
	if err := json.Unmarshal(data, &pb); err != nil {
		return nil, fmt.Errorf("playbooks: parse %s: %w", id, err)
	}
	return &pb, nil
}

func (s *Store) listLocked() ([]*Playbook, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("playbooks: readdir: %w", err)
	}
	out := make([]*Playbook, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		pb, err := s.loadLocked(id)
		if err != nil {
			continue
		}
		if pb != nil {
			out = append(out, pb)
		}
	}
	return out, nil
}
