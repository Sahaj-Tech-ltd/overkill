// Package session — prompt stash storage.
//
// StashStore persists draft prompts to ~/.overkill/stash.json so users can save a
// half-written message and restore it later.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

// StashEntry is one saved prompt.
type StashEntry struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	SavedAt time.Time `json:"saved_at"`
}

// StashStore is a file-backed list of stash entries.
type StashStore struct {
	mu   sync.Mutex
	path string
}

// NewStashStore opens or creates a stash file at path. The directory is
// created if missing.
func NewStashStore(path string) (*StashStore, error) {
	if path == "" {
		return nil, fmt.Errorf("stash: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("stash: mkdir: %w", err)
	}
	return &StashStore{path: path}, nil
}

// DefaultStashPath returns ~/.overkill/stash.json.
func DefaultStashPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".overkill", "stash.json"), nil
}

// Save persists a new entry and returns its id.
func (s *StashStore) Save(text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("stash: cannot save empty text")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, _ := s.readLocked()
	entry := StashEntry{
		ID:      uuid.New().String(),
		Text:    text,
		SavedAt: time.Now().UTC(),
	}
	entries = append(entries, entry)
	if err := s.writeLocked(entries); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// List returns all stash entries, newest first.
func (s *StashStore) List() ([]StashEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []StashEntry{}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SavedAt.After(entries[j].SavedAt)
	})
	return entries, nil
}

// Get returns the text of the entry with the given id.
func (s *StashStore) Get(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readLocked()
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.ID == id {
			return e.Text, nil
		}
	}
	return "", fmt.Errorf("stash: id %q not found", id)
}

// Delete removes the entry with the given id. Missing ids return nil.
func (s *StashStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readLocked()
	if err != nil {
		return err
	}
	out := make([]StashEntry, 0, len(entries))
	for _, e := range entries {
		if e.ID == id {
			continue
		}
		out = append(out, e)
	}
	return s.writeLocked(out)
}

func (s *StashStore) readLocked() ([]StashEntry, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stash: read: %w", err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	var entries []StashEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("stash: parse: %w", err)
	}
	return entries, nil
}

func (s *StashStore) writeLocked(entries []StashEntry) error {
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("stash: marshal: %w", err)
	}
	return atomicfile.WriteFile(s.path, b, 0o644)
}
