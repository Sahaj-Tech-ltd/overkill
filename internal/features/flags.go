// Package features is a small runtime feature-flag layer for Overkill
// (Phase 1.5 #4). Lighter than LaunchDarkly — no network, no
// dependencies — but richer than static config because flags can gate
// on USER, CHANNEL, or a PERCENTAGE rollout per request.
//
// Three layers (highest precedence first):
//  1. Per-user override   (Flag.UserOverrides[userID])
//  2. Per-channel override (Flag.ChannelOverrides[channelID])
//  3. Percentage rollout   (Flag.Percent, deterministic hash of context)
//  4. Default              (Flag.Default)
//
// All flags are evaluated against an EvalContext. A missing flag is
// treated as disabled (safe default — code paths gated behind a flag
// shouldn't activate just because the flag name was typo'd).
package features

import (
	"hash/fnv"
	"strings"
	"sync"
)

// Flag is a single named gate.
type Flag struct {
	Name             string            `toml:"-" json:"name"`
	Default          bool              `toml:"default" json:"default"`
	Percent          int               `toml:"percent" json:"percent"`       // 0..100; 0 = disabled rollout
	UserOverrides    map[string]bool   `toml:"users" json:"users,omitempty"` // userID → enabled
	ChannelOverrides map[string]bool   `toml:"channels" json:"channels,omitempty"`
	Tags             map[string]string `toml:"tags" json:"tags,omitempty"` // free-form metadata for UI
}

// EvalContext supplies the inputs the layered rules evaluate against.
// All fields optional — an empty context evaluates only the Default.
type EvalContext struct {
	UserID  string
	Channel string
	// Subject is the optional unit-of-rollout key. When set, percent
	// rollout hashes against this string so a given subject (e.g.
	// session ID, project name) gets a STABLE bucket. When empty,
	// UserID is used as the subject. When both empty, percent rollout
	// is treated as disabled (no stable bucket = unsafe to flip).
	Subject string
}

// Manager holds a set of flags and evaluates them concurrently.
type Manager struct {
	mu    sync.RWMutex
	flags map[string]*Flag
}

// NewManager returns an empty manager. Use Register / LoadDefaults /
// LoadFromTOML to populate.
func NewManager() *Manager {
	return &Manager{flags: make(map[string]*Flag)}
}

// Register adds or replaces a flag. Pass a copy if the caller might
// mutate the original — Manager stores the pointer.
func (m *Manager) Register(f *Flag) {
	if f == nil || f.Name == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flags[f.Name] = f
}

// Get returns a copy of the named flag, or nil when not registered.
func (m *Manager) Get(name string) *Flag {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.flags[name]
	if !ok {
		return nil
	}
	cp := *f
	return &cp
}

// List returns all registered flags. Order is undefined.
func (m *Manager) List() []*Flag {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Flag, 0, len(m.flags))
	for _, f := range m.flags {
		cp := *f
		out = append(out, &cp)
	}
	return out
}

// Enabled evaluates the named flag against ctx and returns the
// decision. Unknown flag → false (safe default).
//
// Layering (highest precedence first):
//  1. UserOverrides[ctx.UserID]    if entry present
//  2. ChannelOverrides[ctx.Channel] if entry present
//  3. Percent rollout              if Percent > 0 AND subject available.
//     Subject is the primary roll-out key (e.g. a user ID). When Subject
//     is empty, UserID is used as a fallback. When both are empty the
//     percent check is silently skipped — no one gets the flag via
//     percent rollout. This is intentional: a flag with Percent > 0
//     but no identifiable user simply won't roll out to that context.
//     Set a ChannelOverride or Default=true if you need anonymous
//     contexts to receive the flag.
//  4. Default
func (m *Manager) Enabled(name string, ctx EvalContext) bool {
	m.mu.RLock()
	f, ok := m.flags[name]
	// Copy the relevant fields while holding the lock so a concurrent
	// Register can't replace the pointer underneath us (§LOW-11).
	defaultVal := false
	percentVal := 0
	var userOverrides map[string]bool
	var channelOverrides map[string]bool
	if ok {
		defaultVal = f.Default
		percentVal = f.Percent
		userOverrides = f.UserOverrides
		channelOverrides = f.ChannelOverrides
	}
	m.mu.RUnlock()
	if !ok {
		return false
	}
	if ctx.UserID != "" {
		if v, present := userOverrides[ctx.UserID]; present {
			return v
		}
	}
	if ctx.Channel != "" {
		if v, present := channelOverrides[ctx.Channel]; present {
			return v
		}
	}
	if percentVal > 0 && percentVal <= 100 {
		subject := ctx.Subject
		if subject == "" {
			subject = ctx.UserID
		}
		if subject != "" {
			if bucket(name, subject) < percentVal {
				return true
			}
		}
	}
	return defaultVal
}

// bucket maps (flag, subject) → integer in [0, 100). Deterministic so
// a subject's bucket is stable across calls and process restarts.
// fnv32 is cheap and good enough — we're not building a CDN.
func bucket(flag, subject string) int {
	h := fnv.New32a()
	h.Write([]byte(flag))
	h.Write([]byte{':'})
	h.Write([]byte(strings.ToLower(subject)))
	return int(h.Sum32() % 100)
}
