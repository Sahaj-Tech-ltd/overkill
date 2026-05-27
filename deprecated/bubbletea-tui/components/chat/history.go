// Package chat — prompt history (up/down recall).
//
// History keeps an in-memory ring of recent user prompts and persists them to
// disk per-session so the user can recall previous inputs across runs.
package chat

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxHistoryEntries = 100

// History tracks recent user prompts with a recall cursor.
type History struct {
	mu      sync.Mutex
	entries []string
	cursor  int // -1 = inactive (not recalling), otherwise index into entries
	path    string
}

// NewHistory builds an in-memory history with no persistence.
func NewHistory() *History {
	return &History{cursor: -1}
}

// NewHistoryWithFile builds a history persisted to a file. The file is loaded
// immediately if it exists; missing/unreadable files are treated as empty.
func NewHistoryWithFile(path string) *History {
	h := &History{cursor: -1, path: path}
	h.load()
	return h
}

// Append adds a new entry, deduping consecutive duplicates and trimming the
// ring to maxHistoryEntries. It also persists to disk if a path is set.
func (h *History) Append(text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	h.mu.Lock()
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == text {
		h.cursor = -1
		h.mu.Unlock()
		return
	}
	h.entries = append(h.entries, text)
	if len(h.entries) > maxHistoryEntries {
		h.entries = h.entries[len(h.entries)-maxHistoryEntries:]
	}
	h.cursor = -1
	path := h.path
	entries := append([]string(nil), h.entries...)
	h.mu.Unlock()

	if path != "" {
		_ = persist(path, entries)
	}
}

// Prev walks backward in history. Returns "" when there's nothing earlier.
func (h *History) Prev() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.entries) == 0 {
		return ""
	}
	if h.cursor == -1 {
		h.cursor = len(h.entries) - 1
		return h.entries[h.cursor]
	}
	if h.cursor > 0 {
		h.cursor--
		return h.entries[h.cursor]
	}
	return h.entries[h.cursor]
}

// Next walks forward in history. Returns "" when the cursor walks past the
// newest entry (signalling the editor should clear).
func (h *History) Next() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cursor == -1 || len(h.entries) == 0 {
		return ""
	}
	if h.cursor < len(h.entries)-1 {
		h.cursor++
		return h.entries[h.cursor]
	}
	h.cursor = -1
	return ""
}

// Reset clears the recall cursor (call this on submit or on free typing).
func (h *History) Reset() {
	h.mu.Lock()
	h.cursor = -1
	h.mu.Unlock()
}

// IsActive reports whether the user is currently walking history.
func (h *History) IsActive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cursor != -1
}

// Entries returns a copy of the in-memory ring (newest last).
func (h *History) Entries() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]string, len(h.entries))
	copy(cp, h.entries)
	return cp
}

func (h *History) load() {
	if h.path == "" {
		return
	}
	f, err := os.Open(h.path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		h.entries = append(h.entries, line)
	}
	if len(h.entries) > maxHistoryEntries {
		h.entries = h.entries[len(h.entries)-maxHistoryEntries:]
	}
}

func persist(path string, entries []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, e := range entries {
		_, _ = w.WriteString(e)
		_, _ = w.WriteString("\n")
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
