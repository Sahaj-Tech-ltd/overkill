package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type AlertStore struct {
	dir       string
	mu        sync.RWMutex
	alerts    []Alert
	retention time.Duration // how long to keep acknowledged alerts before pruning
	maxAlerts int           // cap on total in-memory alerts
	dirty     bool          // set when alerts are mutated; cleared by saveLocked
}

func NewAlertStore(dir string) *AlertStore {
	return &AlertStore{
		dir:       dir,
		retention: 7 * 24 * time.Hour, // default: 7 days
		maxAlerts: 1000,
	}
}

// SetRetention configures how long acknowledged alerts are kept before
// pruning. Set <= 0 to disable time-based pruning (still capped by max).
func (s *AlertStore) SetRetention(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retention = d
}

// SetMaxAlerts configures the cap on total in-memory alerts (default 1000).
func (s *AlertStore) SetMaxAlerts(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > 0 {
		s.maxAlerts = n
	}
}

func (s *AlertStore) Create(alertType AlertType, message string, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	alert := Alert{
		ID:        uuid.New().String(),
		Type:      alertType,
		Message:   message,
		SessionID: sessionID,
		Timestamp: time.Now().UTC(),
	}

	s.alerts = append(s.alerts, alert)
	s.dirty = true
	s.pruneLocked()
	return s.saveLocked()
}

func (s *AlertStore) Pending() []Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Alert
	for _, a := range s.alerts {
		if !a.Acknowledged {
			result = append(result, a)
		}
	}
	return result
}

func (s *AlertStore) Acknowledge(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.alerts {
		if s.alerts[i].ID == id {
			s.alerts[i].Acknowledged = true
			s.dirty = true
			return s.saveLocked()
		}
	}

	return fmt.Errorf("journal: alert %s not found", id)
}

func (s *AlertStore) DismissAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.alerts {
		s.alerts[i].Acknowledged = true
	}
	s.dirty = true
	return s.saveLocked()
}

func (s *AlertStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, "alerts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.alerts = nil
			return nil
		}
		return fmt.Errorf("journal: loading alerts: %w", err)
	}

	if len(data) == 0 {
		s.alerts = nil
		return nil
	}

	if err := json.Unmarshal(data, &s.alerts); err != nil {
		return fmt.Errorf("journal: unmarshaling alerts: %w", err)
	}

	s.dirty = true
	return nil
}

func (s *AlertStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveLocked()
}

func (s *AlertStore) saveLocked() error {
	// Fast path: skip file write when nothing changed since last save
	// (B014). This avoids rewriting the entire alerts.json on every
	// read-only call that accidentally goes through saveLocked.
	if !s.dirty {
		return nil
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("journal: creating alert dir: %w", err)
	}

	path := filepath.Join(s.dir, "alerts.json")
	data, err := json.MarshalIndent(s.alerts, "", "  ")
	if err != nil {
		return fmt.Errorf("journal: marshaling alerts: %w", err)
	}

	// Atomic write: temp + rename. The prior direct os.WriteFile
	// could leave a truncated/empty alerts.json on a crash mid-
	// write; the next boot then loaded zero alerts and silently
	// dropped every pending memory_corruption / task_completed /
	// frustration_signal record. Every other state file in this
	// repo uses temp+rename; this was the outlier.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("journal: writing alerts tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("journal: rename alerts: %w", err)
	}

	s.dirty = false
	return nil
}

// pruneLocked removes acknowledged alerts older than retention and
// caps total alerts to maxAlerts (oldest first). Caller must hold s.mu.
func (s *AlertStore) pruneLocked() {
	if len(s.alerts) == 0 {
		return
	}

	cutoff := time.Now().UTC().Add(-s.retention)

	kept := s.alerts[:0]
	for _, a := range s.alerts {
		// Drop acknowledged alerts older than the retention window.
		if a.Acknowledged && s.retention > 0 && a.Timestamp.Before(cutoff) {
			continue
		}
		kept = append(kept, a)
	}

	// Cap to maxAlerts — drop oldest (smallest timestamp) if over the cap.
	if s.maxAlerts > 0 && len(kept) > s.maxAlerts {
		sort.Slice(kept, func(i, j int) bool {
			return kept[i].Timestamp.Before(kept[j].Timestamp)
		})
		kept = kept[len(kept)-s.maxAlerts:]
	}

	s.alerts = kept
}
