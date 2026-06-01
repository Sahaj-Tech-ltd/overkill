// Package personality — cold-start workflow orchestrator.
// Coordinates the sequence of cold-start checks, user prompts,
// and auto-seeding of personality state files.
package personality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ColdStartManager wires ColdStartProtocol to disk so the "did we
// already meet?" check is durable across sessions (master plan §4.16).
//
// File: `<memoriesDir>/relationship.json`. Presence + non-empty content
// means a prior session completed the cold-start. Missing or empty file
// triggers the opening-question flow on the next boot.
type ColdStartManager struct {
	proto *ColdStartProtocol
	path  string

	mu       sync.Mutex
	answered bool // ProcessResponse has been called this session
}

// NewColdStartManager wires the protocol to a relationship-file path
// under memoriesDir. The file doesn't have to exist — IsColdStart
// treats missing/empty as cold start.
func NewColdStartManager(memoriesDir string) *ColdStartManager {
	path := filepath.Join(memoriesDir, "relationship.json")
	return &ColdStartManager{
		proto: NewColdStartProtocol(),
		path:  path,
	}
}

// Path returns the relationship file location, exposed for tests and
// for the doctor command.
func (m *ColdStartManager) Path() string { return m.path }

// IsColdStart returns true when the relationship file is missing,
// empty, or doesn't decode. Conservative: any failure to read the
// existing state treats it as cold start (better to ask the user
// again than to silently behave as if we've worked together).
func (m *ColdStartManager) IsColdStart() bool {
	return m.proto.IsColdStart(m.path)
}

// OpeningQuestion returns the single question shown to the user on
// cold start. Same text as the underlying protocol — exposed here so
// callers don't have to thread the inner protocol through.
func (m *ColdStartManager) OpeningQuestion() string {
	return m.proto.OpeningQuestion()
}

// ProcessFirstResponse takes the user's first cold-start reply, infers
// the 5-dim profile, persists it to disk, and marks the protocol
// complete. Subsequent calls in the same session are no-ops (idempotent
// — the protocol completes on the FIRST answer only).
//
// Returns the inferred profile so callers can render a friendly
// confirmation if they want ("got it — direct, terse, urgent").
// Returns nil when already answered.
func (m *ColdStartManager) ProcessFirstResponse(response string) (*ColdStartProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.answered {
		return nil, nil
	}
	profile := m.proto.ProcessResponse(response)
	if profile == nil {
		return nil, fmt.Errorf("personality: cold start: profile inference returned nil")
	}
	if err := m.persistLocked(profile); err != nil {
		return profile, err
	}
	m.answered = true
	return profile, nil
}

func (m *ColdStartManager) persistLocked(profile *ColdStartProfile) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("personality: cold start: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("personality: cold start: marshal: %w", err)
	}
	// Write to a sibling tmp file then rename — avoids leaving a
	// half-written relationship.json behind on partial failures.
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("personality: cold start: write: %w", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("personality: cold start: rename: %w", err)
	}
	return nil
}

// LoadExistingProfile reads the saved profile from disk if one exists.
// Returns nil, nil when the file is missing/empty (cold start). The
// caller decides whether to use the profile to tune the personality
// provider for THIS session.
func (m *ColdStartManager) LoadExistingProfile() (*ColdStartProfile, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var p ColdStartProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("personality: cold start: parse %s: %w", m.path, err)
	}
	return &p, nil
}
