// Package plan — current-task plan + todo store. One "active plan"
// per session: a list of items the agent committed to doing,
// surfaced in the TUI right pane, and used at end-of-task to drive
// the tick-off + learnings reflection (master plan §4.11 +
// §6.2 self-learning loop).
//
// Storage shape: snapshot JSON at ~/.overkill/plans/<session>.json
// PLUS an event-log sidecar (same pattern as personality state
// files — see internal/personality/eventlog.go). The store IS
// agent-mutable, but only via typed tools (plan_set, plan_check,
// plan_status); raw writes to the plan dir are blocked by the
// protected-path gate in internal/agent/tool_scan.go.
package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Item is one todo in the active plan. ID is opaque and assigned
// by the store on Set; the agent references items by ID for Check
// /Uncheck calls. Done timestamps are kept so the end-of-task
// surface can show "you completed 4 of 6 — what's left?".
type Item struct {
	ID     string     `json:"id"`
	Text   string     `json:"text"`
	Done   bool       `json:"done"`
	DoneAt *time.Time `json:"done_at,omitempty"`
	// Note is an optional one-line annotation the agent can attach
	// when checking an item — useful for "checked but with caveat".
	Note string `json:"note,omitempty"`
}

// Plan is the full active-plan record. Title is the headline
// description; Items is the checklist. Created/Updated bracket the
// plan's lifetime. SessionID lets the daemon and TUI agree on
// which plan to show when multiple sessions overlap.
type Plan struct {
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Items     []Item    `json:"items"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// Store wraps the on-disk plan file for ONE session. Concurrent
// access from the agent (writing) and the TUI sidebar (reading)
// is serialised by the in-memory mutex; cross-process safety
// comes from atomic rename + the event-log sidecar.
type Store struct {
	dir       string
	sessionID string
	mu        sync.RWMutex
	current   *Plan
}

// NewStore builds a per-session Store. The directory is created
// lazily on the first Save. sessionID is the agent's session ID;
// the file lives at <dir>/<sessionID>.json.
func NewStore(dir, sessionID string) *Store {
	return &Store{dir: dir, sessionID: sessionID}
}

// path returns the snapshot file for this session's plan.
func (s *Store) path() string {
	return filepath.Join(s.dir, s.sessionID+".json")
}

// Load reads the snapshot from disk (or the event-log latest if
// the snapshot is missing/corrupt). Missing-file is the legal
// "no active plan" state — returns (nil, nil).
func (s *Store) Load() (*Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("plan: read snapshot: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("plan: parse: %w", err)
	}
	dup := p
	s.current = &dup
	return &p, nil
}

// Current returns the in-memory plan or nil. Cheap — no disk hit.
func (s *Store) Current() *Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == nil {
		return nil
	}
	dup := *s.current
	dup.Items = append([]Item(nil), s.current.Items...)
	return &dup
}

// Set replaces the active plan with a new title + items. Any prior
// plan for this session is overwritten. Items get fresh IDs
// assigned. Use this at the top of a task once the agent has
// decomposed the work into checkable steps.
func (s *Store) Set(title string, itemTexts []string) (*Plan, error) {
	if title == "" && len(itemTexts) == 0 {
		return nil, errors.New("plan: title or items required")
	}
	now := time.Now().UTC()
	items := make([]Item, 0, len(itemTexts))
	for _, t := range itemTexts {
		items = append(items, Item{ID: uuid.New().String(), Text: t})
	}
	p := &Plan{
		SessionID: s.sessionID,
		Title:     title,
		Items:     items,
		Created:   now,
		Updated:   now,
	}
	if err := s.save(p); err != nil {
		return nil, err
	}
	return s.Current(), nil
}

// Check marks an item done by ID. Note is optional. Returns the
// updated plan. Idempotent — checking an already-done item just
// refreshes the DoneAt timestamp.
func (s *Store) Check(itemID, note string) (*Plan, error) {
	s.mu.Lock()
	if s.current == nil {
		s.mu.Unlock()
		return nil, errors.New("plan: no active plan")
	}
	found := false
	now := time.Now().UTC()
	for i := range s.current.Items {
		if s.current.Items[i].ID == itemID {
			s.current.Items[i].Done = true
			s.current.Items[i].DoneAt = &now
			if note != "" {
				s.current.Items[i].Note = note
			}
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return nil, fmt.Errorf("plan: no item with id %q", itemID)
	}
	s.current.Updated = now
	p := *s.current
	s.mu.Unlock()
	if err := s.save(&p); err != nil {
		return nil, err
	}
	return s.Current(), nil
}

// Uncheck flips an item back to pending. Mirrors Check semantics.
// The plan's Updated timestamp moves; the item's DoneAt is cleared.
func (s *Store) Uncheck(itemID string) (*Plan, error) {
	s.mu.Lock()
	if s.current == nil {
		s.mu.Unlock()
		return nil, errors.New("plan: no active plan")
	}
	found := false
	for i := range s.current.Items {
		if s.current.Items[i].ID == itemID {
			s.current.Items[i].Done = false
			s.current.Items[i].DoneAt = nil
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return nil, fmt.Errorf("plan: no item with id %q", itemID)
	}
	s.current.Updated = time.Now().UTC()
	p := *s.current
	s.mu.Unlock()
	if err := s.save(&p); err != nil {
		return nil, err
	}
	return s.Current(), nil
}

// Clear removes the active plan. Used at end of task when the
// agent confirms everything's done — frees the right pane for the
// next task's plan.
func (s *Store) Clear() error {
	s.mu.Lock()
	s.current = nil
	s.mu.Unlock()
	if err := os.Remove(s.path()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("plan: clear: %w", err)
	}
	return nil
}

// Remaining returns the count of unchecked items in the current
// plan, or 0 when there's no plan. Used by the end-of-task surface
// to decide whether to nudge the agent.
func (s *Store) Remaining() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == nil {
		return 0
	}
	n := 0
	for _, it := range s.current.Items {
		if !it.Done {
			n++
		}
	}
	return n
}

// save writes the plan snapshot + event-log entry. Atomic via
// temp-file rename so a crash mid-write leaves the previous
// snapshot intact.
func (s *Store) save(p *Plan) error {
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("plan: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("plan: marshal: %w", err)
	}
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("plan: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("plan: rename: %w", err)
	}
	s.mu.Lock()
	dup := *p
	dup.Items = append([]Item(nil), p.Items...)
	s.current = &dup
	s.mu.Unlock()
	return nil
}
