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
	"fmt"
	"os"
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
	started  bool
	startErr error
	handlers map[Subject][]chan Event
	debounce time.Duration

	// errors surfaces fsnotify-level problems. Buffered (16) so a
	// non-listening consumer doesn't stall the watcher.
	errors chan error

	watcher    *fsnotify.Watcher
	stopCh     chan struct{}
	stopped    chan struct{}
	ready      chan struct{}
	readyOnce  sync.Once
	stopOnce   sync.Once
	finishOnce sync.Once
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
		ready:    make(chan struct{}),
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

// Start waits until filesystem watches have been registered. The owner must
// cancel ctx or call Stop; an agent's short request context is not its owner.
func (b *Bus) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	go func() {
		if err := b.Run(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, ErrStopped) {
			select {
			case b.errors <- err:
			default:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ready:
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.RLock()
		defer b.mu.RUnlock()
		return b.startErr
	}
}

var ErrStopped = errors.New("hotreload: bus stopped")

// Run owns a single watcher lifetime. Repeated calls never create a second
// watcher or reopen a stopped bus.
func (b *Bus) Run(ctx context.Context) error {
	b.mu.Lock()
	if b.started {
		err := b.startErr
		b.mu.Unlock()
		return err
	}
	b.started, b.running = true, true
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.running = false
		if b.startErr == nil {
			b.startErr = ErrStopped
		}
		b.mu.Unlock()
		b.readyOnce.Do(func() { close(b.ready) })
		b.finishOnce.Do(func() { close(b.stopped) })
	}()
	failStart := func(err error) error {
		b.mu.Lock()
		b.startErr = err
		b.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		return failStart(err)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return failStart(err)
	}
	defer w.Close()
	b.mu.Lock()
	b.watcher = w
	b.mu.Unlock()
	register := func() error {
		for _, target := range b.watchablePaths() {
			if err := w.Add(target); err != nil {
				return fmt.Errorf("hotreload: register watch: %w", err)
			}
		}
		return nil
	}
	if err := register(); err != nil {
		return failStart(err)
	}
	b.readyOnce.Do(func() { close(b.ready) })

	type pending struct {
		ev    Event
		timer *time.Timer
	}
	var debounceMu sync.Mutex
	var callbacks sync.WaitGroup
	closing := false
	pendingMap := map[string]*pending{}
	defer func() {
		debounceMu.Lock()
		closing = true
		for key, item := range pendingMap {
			if item.timer.Stop() {
				callbacks.Done()
			}
			delete(pendingMap, key)
		}
		debounceMu.Unlock()
		callbacks.Wait()
	}()
	flush := func(key string, item *pending) {
		defer callbacks.Done()
		debounceMu.Lock()
		if closing || pendingMap[key] != item {
			debounceMu.Unlock()
			return
		}
		delete(pendingMap, key)
		debounceMu.Unlock()
		// Atomic rename/save may emit Remove before replacement Create.
		if item.ev.Kind == EventRemoved {
			if _, err := os.Stat(item.ev.Path); err == nil {
				item.ev.Kind = EventModified
			}
		}
		b.deliver(item.ev)
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
			// Register new ancestor and nested directories before classification:
			// an intermediate path may not itself belong to any subject.
			if fe.Op&fsnotify.Create != 0 {
				if info, err := os.Lstat(fe.Name); err == nil && info.IsDir() {
					if err := register(); err != nil {
						select {
						case b.errors <- err:
						default:
						}
					}
					// A whole tree can be moved into place in one event.
					for _, file := range []string{b.paths.UserConfigFile, b.paths.PermissionsFile} {
						if file != "" && withinDirectory(filepath.Clean(file), filepath.Clean(fe.Name)) {
							if _, err := os.Stat(file); err == nil {
								if subject, ok := b.classify(file); ok {
									b.deliver(Event{Subject: subject, Path: file})
								}
							}
						}
					}
				}
			}
			subject, ok := b.classify(fe.Name)
			if !ok {
				continue
			}
			event := Event{Subject: subject, Path: fe.Name}
			if fe.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				event.Kind = EventRemoved
			}
			key := string(subject) + "::" + fe.Name
			debounceMu.Lock()
			if old := pendingMap[key]; old != nil && old.timer.Stop() {
				callbacks.Done()
			}
			item := &pending{ev: event}
			callbacks.Add(1)
			item.timer = time.AfterFunc(b.debounce, func() { flush(key, item) })
			pendingMap[key] = item
			debounceMu.Unlock()
		case err, ok := <-w.Errors:
			if !ok {
				return errors.New("hotreload: watcher error channel closed")
			}
			select {
			case b.errors <- err:
			default:
			}
		}
	}
}

// Stop is terminal, idempotent and joins watcher/timer callbacks. It also works
// before Run, and concurrent callers cannot close a channel twice.
func (b *Bus) Stop() {
	b.mu.Lock()
	b.stopOnce.Do(func() { close(b.stopCh) })
	if b.startErr == nil {
		b.startErr = ErrStopped
	}
	if !b.started {
		b.started = true
		b.startErr = ErrStopped
		b.readyOnce.Do(func() { close(b.ready) })
		b.finishOnce.Do(func() { close(b.stopped) })
	}
	b.mu.Unlock()
	<-b.stopped
}

// SubscriberCount reports live subscriptions for lifecycle diagnostics.
func (b *Bus) SubscriberCount(subject Subject) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.handlers[subject])
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
	if b.paths.SkillsDir != "" && withinDirectory(clean, b.paths.SkillsDir) {
		return SubjectSkill, true
	}
	if b.paths.AgentsDir != "" && withinDirectory(clean, b.paths.AgentsDir) {
		return SubjectSubagent, true
	}
	if b.paths.PluginsDir != "" && withinDirectory(clean, b.paths.PluginsDir) {
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
// the bus should add to fsnotify. File parents and nested directories are
// watched so atomic saves and directory replacement remain observable.
func (b *Bus) watchablePaths() []string {
	candidates := []string{}
	// Watch parents so a missing file and atomic editor replacements remain
	// observable; classify filters unrelated sibling events.
	for _, file := range []string{b.paths.PermissionsFile, b.paths.UserConfigFile} {
		if file != "" {
			candidates = append(candidates, filepath.Dir(file))
		}
	}
	for _, directory := range []string{b.paths.SkillsDir, b.paths.AgentsDir, b.paths.PluginsDir} {
		if directory != "" {
			candidates = append(candidates, filepath.Dir(directory), directory)
			// WalkDir does not follow symlinks outside the configured tree.
			_ = filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
				if err == nil && entry.IsDir() {
					candidates = append(candidates, path)
				}
				return nil
			})
		}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		for {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				break
			}
			parent := filepath.Dir(candidate)
			if parent == candidate {
				break
			}
			candidate = parent
		}
		if !seen[candidate] {
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

func withinDirectory(file, directory string) bool {
	root := filepath.Clean(directory)
	return file == root || strings.HasPrefix(file, root+string(filepath.Separator))
}
