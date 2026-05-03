// Package tags stores user-applied annotations on file paths. Tags are
// surfaced in the TUI via /tags and used by the agent to find files quickly
// (e.g. "show me the @important files").
package tags

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tag is a single annotation on a file path.
type Tag struct {
	Path string    `json:"path"`
	Tag  string    `json:"tag"`
	Note string    `json:"note,omitempty"`
	Time time.Time `json:"ts"`
}

// Manager owns the tag store. Backed by a JSONL file appended atomically.
type Manager struct {
	mu   sync.RWMutex
	path string
	tags []Tag
}

// NewManager opens (or creates) a tag store at the given path. If path is
// empty, it defaults to ~/.ethos/tags.jsonl.
func NewManager(path string) (*Manager, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".ethos", "tags.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	m := &Manager{path: path}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) load() error {
	f, err := os.Open(m.path)
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
		var t Tag
		if err := json.Unmarshal([]byte(line), &t); err == nil && t.Path != "" {
			m.tags = append(m.tags, t)
		}
	}
	return sc.Err()
}

func (m *Manager) appendLine(t Tag) error {
	f, err := os.OpenFile(m.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func (m *Manager) rewrite() error {
	tmp := m.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, t := range m.tags {
		data, _ := json.Marshal(t)
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// Tag adds an annotation. If (path, tag) already exists, the note is updated.
func (m *Manager) Tag(path, tag, note string) error {
	if path == "" || tag == "" {
		return errors.New("tags: path and tag are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, t := range m.tags {
		if t.Path == path && t.Tag == tag {
			m.tags[i].Note = note
			m.tags[i].Time = time.Now().UTC()
			return m.rewrite()
		}
	}
	t := Tag{Path: path, Tag: tag, Note: note, Time: time.Now().UTC()}
	m.tags = append(m.tags, t)
	return m.appendLine(t)
}

// Untag removes a (path, tag) pair. If tag is empty, all tags on the path
// are removed.
func (m *Manager) Untag(path, tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.tags[:0]
	changed := false
	for _, t := range m.tags {
		if t.Path == path && (tag == "" || t.Tag == tag) {
			changed = true
			continue
		}
		out = append(out, t)
	}
	m.tags = out
	if !changed {
		return nil
	}
	return m.rewrite()
}

// List returns a snapshot of all tags, sorted by tag then path.
func (m *Manager) List() []Tag {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Tag, len(m.tags))
	copy(out, m.tags)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Tag != out[j].Tag {
			return out[i].Tag < out[j].Tag
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// ByPath returns all tags on a given path.
func (m *Manager) ByPath(path string) []Tag {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Tag
	for _, t := range m.tags {
		if t.Path == path {
			out = append(out, t)
		}
	}
	return out
}

// ByTag returns all paths that have the given tag, in insertion order.
func (m *Manager) ByTag(tag string) []Tag {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Tag
	for _, t := range m.tags {
		if t.Tag == tag {
			out = append(out, t)
		}
	}
	return out
}

// Tags returns the unique set of tag names in the store.
func (m *Manager) Tags() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, t := range m.tags {
		if !seen[t.Tag] {
			seen[t.Tag] = true
			out = append(out, t.Tag)
		}
	}
	sort.Strings(out)
	return out
}
