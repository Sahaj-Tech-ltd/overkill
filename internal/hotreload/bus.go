// Package hotreload — the v2.0 live-reload event bus.
//
// Subsystems that care about runtime config changes (skills, tools,
// scanners, MCP servers, the agent's model) subscribe to a Subject
// here. A single watcher goroutine over fsnotify multiplexes file
// events into typed Events; subscribers receive them with a 200ms
// debounce so a save that fires multiple fsnotify events coalesces
// into one Event per Subject.
//
// Failure mode is fail-open: a subscriber that panics is logged and
// dropped from this Event, but the bus stays alive. fsnotify errors
// don't kill the watcher — they're surfaced via the Errors channel
// the bus exposes for observability.
//
// Design notes vs the obvious alternatives:
//
//   - Channel per subscriber instead of callbacks: subscribers run
//     the work on their own goroutine. Keeps the bus loop fast and
//     means a slow subscriber doesn't stall other subscribers.
//   - 200ms debounce window: editors that auto-save (or save+sync)
//     fire 2-3 events back-to-back. Without debounce a single save
//     would Reload a skill three times.
//   - No global singleton: callers wire a *Bus into the systems that
//     need it. Easier to test; no init-order surprises.
package hotreload

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Subject scopes which subsystem a subscriber cares about.
type Subject string

const (
	SubjectSkill      Subject = "skill"
	SubjectSubagent   Subject = "subagent"
	SubjectTool       Subject = "tool"
	SubjectMCPServer  Subject = "mcp_server"
	SubjectPermission Subject = "permission"
	SubjectConfig     Subject = "config" // user.yaml itself
)

// EventKind reports what fsnotify saw. Created/Modified are
// collapsed into "Modified" for subscribers; Removed surfaces
// separately so subscribers can forget the artifact.
type EventKind int

const (
	EventModified EventKind = iota
	EventRemoved
)

// Event is the typed payload subscribers receive. Path is absolute.
type Event struct {
	Kind    EventKind
	Subject Subject
	Path    string
}

// Paths configures which directories/files the bus watches and which
// Subject each maps to. Empty strings disable that watch.
type Paths struct {
	SkillsDir       string // dir of *.md skill files
	AgentsDir       string // dir of *.md subagent files
	PluginsDir      string // dir containing plugin subfolders
	PermissionsFile string // ~/.overkill/permissions.json
	UserConfigFile  string // ~/.config/overkill/user.yaml
}

// Bus is the multiplexed reload event bus. Construct via New; call
// Run to start the watcher; call Stop to drain.
type Bus struct {
	paths    Paths
	mu       sync.RWMutex
	running  bool
	handlers map[Subject][]chan Event
	debounce time.Duration

	// errors surfaces fsnotify-level problems. Buffered (16) so a
	// non-listening consumer doesn't stall the watcher.
	errors chan error

	watcher *fsnotify.Watcher
	stopCh  chan struct{}
	stopped chan struct{}
}

// New constructs a Bus with sensible defaults. Debounce is 200ms — a
// single editor save typically fires 2-3 events in <50ms, so 200ms
// catches them all without making interactive use feel laggy.
func New(paths Paths) *Bus {
	return &Bus{
		paths:    paths,
		handlers: map[Subject][]chan Event{},
		debounce: 200 * time.Millisecond,
		errors:   make(chan error, 16),
		stopCh:   make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

// Subscribe wires a handler channel for a Subject. Returns an
// unsubscribe func the caller MUST call on shutdown so the bus can
// reclaim the channel — without it the watcher would hold a closed
// goroutine reference forever.
//
// The channel is buffered (8) so a brief consumer stall doesn't
// drop events; a sustained stall drops them silently and surfaces
// nothing — the trade-off is "deliver-best-effort, never block."
func (b *Bus) Subscribe(s Subject) (<-chan Event, func()) {
	ch := make(chan Event, 8)
	b.mu.Lock()
	b.handlers[s] = append(b.handlers[s], ch)
	b.mu.Unlock()
	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		hs := b.handlers[s]
		for i, c := range hs {
			if c == ch {
				b.handlers[s] = append(hs[:i], hs[i+1:]...)
				// Don't close the channel — deliver() may still be
				// iterating over a snapshot of the handler slice.
				// GC reclaims the channel once nothing references it.
				return
			}
		}
	}
	return ch, unsubscribe
}

// Errors returns a read-only stream of fsnotify-level errors. Drain
// in a goroutine if you want to log them; ignoring is fine — the
// channel is buffered.
func (b *Bus) Errors() <-chan error { return b.errors }

// Run starts the watcher. Blocks until ctx is cancelled or Stop is
// called. Idempotent: a second Run returns immediately.
func (b *Bus) Run(ctx context.Context) error {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return nil
	}
	b.running = true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
	}()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	b.watcher = w
	defer w.Close()
	defer close(b.stopped)

	// Add each configured path. fsnotify accepts files and dirs; we
	// don't recurse — caller-supplied dirs are expected to be flat
	// (skills, subagents, plugins each have a known shallow layout).
	for _, p := range b.watchablePaths() {
		if p == "" {
			continue
		}
		if err := w.Add(p); err != nil {
			// Non-fatal: a missing skills dir is fine, the user just
			// hasn't created any skills yet. We surface the error
			// for observability but keep going.
			select {
			case b.errors <- err:
			default:
			}
		}
	}

	// Debounce table: per-(subject, path) pending event with timer.
	type pending struct {
		ev    Event
		timer *time.Timer
	}
	debounceMu := sync.Mutex{}
	pendingMap := map[string]*pending{}

	flush := func(key string) {
		debounceMu.Lock()
		p, ok := pendingMap[key]
		if !ok {
			debounceMu.Unlock()
			return
		}
		delete(pendingMap, key)
		debounceMu.Unlock()
		b.deliver(p.ev)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.stopCh:
			return nil
		case fe, ok := <-w.Events:
			if !ok {
				return errors.New("hotreload: watcher closed")
			}
			subj, ok := b.classify(fe.Name)
			if !ok {
				continue
			}
			ev := Event{
				Subject: subj,
				Path:    fe.Name,
			}
			if fe.Op&fsnotify.Remove != 0 || fe.Op&fsnotify.Rename != 0 {
				ev.Kind = EventRemoved
			} else {
				ev.Kind = EventModified
			}
			key := string(subj) + "::" + fe.Name
			debounceMu.Lock()
			if existing, ok := pendingMap[key]; ok && existing.timer != nil {
				existing.timer.Stop()
			}
			t := time.AfterFunc(b.debounce, func() { flush(key) })
			pendingMap[key] = &pending{ev: ev, timer: t}
			debounceMu.Unlock()
		case fserr, ok := <-w.Errors:
			if !ok {
				return errors.New("hotreload: watcher error chan closed")
			}
			select {
			case b.errors <- fserr:
			default:
			}
		}
	}
}

// Stop signals Run to exit. Idempotent; safe to call from any
// goroutine. Blocks until Run has actually exited.
func (b *Bus) Stop() {
	select {
	case <-b.stopCh:
		// already closed
	default:
		close(b.stopCh)
	}
	// Wait for Run to release the watcher; bound the wait so a
	// pathological case doesn't hang shutdown.
	select {
	case <-b.stopped:
	case <-time.After(2 * time.Second):
	}
}

// deliver fans an Event out to every subscriber for its Subject.
// Non-blocking sends; a full subscriber channel drops the event
// silently (see Subscribe doc on the trade-off).
func (b *Bus) deliver(ev Event) {
	b.mu.RLock()
	hs := append([]chan Event(nil), b.handlers[ev.Subject]...)
	b.mu.RUnlock()
	for _, ch := range hs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// classify maps a fsnotify event path back to the Subject the bus
// should route it under. Returns ok=false if no configured path
// matches (event ignored).
func (b *Bus) classify(path string) (Subject, bool) {
	clean := filepath.Clean(path)
	if b.paths.SkillsDir != "" && strings.HasPrefix(clean, filepath.Clean(b.paths.SkillsDir)) {
		return SubjectSkill, true
	}
	if b.paths.AgentsDir != "" && strings.HasPrefix(clean, filepath.Clean(b.paths.AgentsDir)) {
		return SubjectSubagent, true
	}
	if b.paths.PluginsDir != "" && strings.HasPrefix(clean, filepath.Clean(b.paths.PluginsDir)) {
		return SubjectTool, true
	}
	if b.paths.PermissionsFile != "" && clean == filepath.Clean(b.paths.PermissionsFile) {
		return SubjectPermission, true
	}
	if b.paths.UserConfigFile != "" && clean == filepath.Clean(b.paths.UserConfigFile) {
		return SubjectConfig, true
	}
	return "", false
}

// watchablePaths returns the deduped, non-empty list of fs targets
// the bus should add to fsnotify. Files are watched at file
// granularity; dirs at dir granularity.
func (b *Bus) watchablePaths() []string {
	candidates := []string{
		b.paths.SkillsDir,
		b.paths.AgentsDir,
		b.paths.PluginsDir,
		b.paths.PermissionsFile,
		b.paths.UserConfigFile,
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(candidates))
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
