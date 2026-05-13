package security

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LedgerEntry records a single permission decision for audit/recall.
type LedgerEntry struct {
	Time     time.Time `json:"ts"`
	Tool     string    `json:"tool"`
	Args     string    `json:"args"`
	Decision string    `json:"decision"` // allow_once | allow_session | allow_global | deny
	Risk     string    `json:"risk,omitempty"`
}

// Ledger appends permission decisions to a per-session JSONL file and serves
// them back to the TUI for the /permissions overlay.
type Ledger struct {
	mu   sync.RWMutex
	path string
	mem  []LedgerEntry
}

// NewLedger constructs a session ledger at ~/.overkill/sessions/<id>/permissions.jsonl
// (or the path given). The directory is created if missing.
func NewLedger(path string) (*Ledger, error) {
	if path == "" {
		return nil, errors.New("ledger: path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l := &Ledger{path: path}
	_ = l.load() // best-effort
	return l, nil
}

func (l *Ledger) load() error {
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e LedgerEntry
		if err := json.Unmarshal([]byte(line), &e); err == nil {
			l.mem = append(l.mem, e)
		}
	}
	return nil
}

// Append writes a new entry to disk and memory.
func (l *Ledger) Append(e LedgerEntry) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.mem = append(l.mem, e)

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// Entries returns a snapshot of all entries (oldest first).
func (l *Ledger) Entries() []LedgerEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]LedgerEntry, len(l.mem))
	copy(out, l.mem)
	return out
}

// Filter returns entries matching the predicate.
func (l *Ledger) Filter(pred func(LedgerEntry) bool) []LedgerEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []LedgerEntry
	for _, e := range l.mem {
		if pred(e) {
			out = append(out, e)
		}
	}
	return out
}
