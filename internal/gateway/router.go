package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SessionRouter maps a (channel, chatKey, thread) tuple onto an overkill
// session id, persists the binding to disk so restarts don't lose
// in-flight conversations, and tracks a per-chat "follow" pointer the
// dispatcher consults each turn.
//
// Follow mode is the cross-channel continuity primitive: a phone chat
// can call /follow tui to mirror whatever session id the TUI is
// currently using, so subsequent user messages route there even after
// the TUI swaps sessions.
type SessionRouter struct {
	path string
	mu   sync.Mutex
	data routerFile
}

type routerFile struct {
	Bindings map[string]binding `json:"bindings"`
	Follows  map[string]string  `json:"follows"` // chatKey -> "tui" | sessionID
}

type binding struct {
	SessionID string    `json:"session_id"`
	Channel   string    `json:"channel"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// NewSessionRouter loads the persisted file. Empty path disables
// persistence (used by tests). Missing file is not an error.
func NewSessionRouter(path string) (*SessionRouter, error) {
	r := &SessionRouter{
		path: path,
		data: routerFile{Bindings: map[string]binding{}, Follows: map[string]string{}},
	}
	if path == "" {
		return r, nil
	}
	if buf, err := os.ReadFile(path); err == nil {
		// Tolerate partial files; start fresh on corruption.
		_ = json.Unmarshal(buf, &r.data)
		if r.data.Bindings == nil {
			r.data.Bindings = map[string]binding{}
		}
		if r.data.Follows == nil {
			r.data.Follows = map[string]string{}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("gateway: load router: %w", err)
	}
	return r, nil
}

// Resolve returns the session id this chat should drive this turn.
// liveTUISession is the agent's current session id at call time; when a
// chat is in follow=tui mode we shadow it.
func (r *SessionRouter) Resolve(channel, chatKey, thread, liveTUISession string) (id string, isFollow bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := bindKey(channel, chatKey, thread)
	if target, ok := r.data.Follows[followKey(channel, chatKey)]; ok {
		if target == "tui" && liveTUISession != "" {
			return liveTUISession, true
		}
		if target != "" && target != "tui" {
			return target, true
		}
	}
	if b, ok := r.data.Bindings[k]; ok {
		return b.SessionID, false
	}
	return "", false
}

// Bind locks a chat to a specific session id and persists the binding.
// Touch is called on every turn to bump Updated for /sessions ordering.
func (r *SessionRouter) Bind(channel, chatKey, thread, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := bindKey(channel, chatKey, thread)
	now := time.Now().UTC()
	b := r.data.Bindings[k]
	if b.SessionID == "" {
		b.Created = now
	}
	b.SessionID = sessionID
	b.Channel = channel
	b.Updated = now
	r.data.Bindings[k] = b
	return r.persistLocked()
}

// Touch bumps Updated so /sessions can sort by recency. No-op if
// unbound.
func (r *SessionRouter) Touch(channel, chatKey, thread string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := bindKey(channel, chatKey, thread)
	if b, ok := r.data.Bindings[k]; ok {
		b.Updated = time.Now().UTC()
		r.data.Bindings[k] = b
		_ = r.persistLocked()
	}
}

// Follow puts the chat into follow mode. target = "tui" mirrors the
// live TUI session id; any other non-empty value pins to that session.
// Empty target clears follow mode. The channel prefix on the key
// prevents different channels (WhatsApp vs Discord) sharing the same
// chatKey from colliding on follow state.
func (r *SessionRouter) Follow(channel, chatKey, target string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	fk := followKey(channel, chatKey)
	if target == "" {
		delete(r.data.Follows, fk)
	} else {
		r.data.Follows[fk] = target
	}
	return r.persistLocked()
}

// FollowTarget returns the current follow target for the chat ("",
// "tui", or a session id).
func (r *SessionRouter) FollowTarget(channel, chatKey string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.Follows[followKey(channel, chatKey)]
}

// RecentEntry is one row in the /sessions list.
type RecentEntry struct {
	SessionID string
	Channel   string
	ChatKey   string
	Thread    string
	Updated   time.Time
}

// Recent returns up to limit bindings sorted newest-first. Used by
// /sessions to show "you were just here, want to resume?".
func (r *SessionRouter) Recent(limit int) []RecentEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecentEntry, 0, len(r.data.Bindings))
	for k, b := range r.data.Bindings {
		ch, chat, thread := splitKey(k)
		out = append(out, RecentEntry{
			SessionID: b.SessionID,
			Channel:   ch,
			ChatKey:   chat,
			Thread:    thread,
			Updated:   b.Updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// NewSessionID mints a router-tagged session id. Channels can pass
// their own ids; this helper is provided for convenience.
func NewSessionID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Crypto rand failed; combine time with an extra random suffix
		// so session IDs are not guessable via time-based probing.
		fallback := make([]byte, 4)
		_, _ = rand.Read(fallback)
		return fmt.Sprintf("%s-%d-%x", prefix, time.Now().UnixNano(), fallback)
	}
	return prefix + "-" + hex.EncodeToString(b)
}

func (r *SessionRouter) snapshotLocked() routerFile {
	out := routerFile{
		Bindings: make(map[string]binding, len(r.data.Bindings)),
		Follows:  make(map[string]string, len(r.data.Follows)),
	}
	for k, v := range r.data.Bindings {
		out.Bindings[k] = v
	}
	for k, v := range r.data.Follows {
		out.Follows[k] = v
	}
	return out
}

func (r *SessionRouter) persist(snap routerFile) error {
	if r.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o750); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// persistLocked persists the current in-memory state. Must be called
// with r.mu already held (unlike persist which snapshots separately).
func (r *SessionRouter) persistLocked() error {
	if r.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o750); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(r.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// followKey returns a namespaced key for the Follows map so that
// different channels (e.g. WhatsApp and Discord) sharing the same
// chatKey don't collide on follow state.
func followKey(channel, chatKey string) string {
	return channel + ":" + chatKey
}

// Keys are channel\x00chatKey\x00thread. Channel/chat/thread are user
// content; the NUL separator makes round-tripping unambiguous.
func bindKey(channel, chatKey, thread string) string {
	return channel + "\x00" + chatKey + "\x00" + thread
}

func splitKey(k string) (channel, chatKey, thread string) {
	parts := splitN(k, '\x00', 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

func splitN(s string, sep byte, n int) []string {
	out := make([]string, 0, n)
	start := 0
	for i := 0; i < len(s) && len(out) < n-1; i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
