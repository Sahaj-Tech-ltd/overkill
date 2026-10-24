// Package terminal owns live terminal processes independently of individual
// model calls and UI connections. Handles are scoped to application-bound
// owners. State is intentionally not restored by replaying commands after a
// backend restart.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/pty"
	"github.com/google/uuid"
)

var (
	ErrNotFound     = errors.New("terminal: handle not found for this owner")
	ErrHumanControl = errors.New("terminal: human has control; agent input is paused")
	ErrClosed       = errors.New("terminal: closed")
)

// Owner is supplied by the application, never decoded from a model request.
// Session identifies the persisted chat; Worker identifies a particular run.
type Owner struct {
	Session string `json:"session"`
	Worker  string `json:"worker"`
}

func (o Owner) valid() bool { return o.Session != "" && o.Worker != "" }

type Options struct {
	ParentOwner   Owner
	RecordHandoff func(context.Context, Handoff) error
	Reserve       func(context.Context, Owner, string) (Reservation, error)
	MaxLive       int
	MaxRetained   int
	BufferBytes   int
	MaxLifetime   time.Duration
}

func defaults(o Options) Options {
	if o.MaxLive <= 0 {
		o.MaxLive = 8
	}
	if o.MaxRetained <= 0 {
		o.MaxRetained = 32
	}
	if o.BufferBytes <= 0 {
		o.BufferBytes = 256 * 1024
	}
	if o.MaxLifetime <= 0 {
		o.MaxLifetime = time.Hour
	}
	return o
}
func Supported() bool { return pty.Supported() }

type Info struct {
	CanHandoff      bool      `json:"can_handoff"`
	ControlRevision uint64    `json:"control_revision"`
	InputSequence   uint64    `json:"input_sequence"`
	CleanupError    string    `json:"cleanup_error,omitempty"`
	ID              string    `json:"id"`
	Owner           Owner     `json:"owner"`
	Command         string    `json:"command"`
	CWD             string    `json:"cwd"`
	State           string    `json:"state"`
	Controller      string    `json:"controller"`
	ExitCode        *int      `json:"exit_code,omitempty"`
	StartedAt       time.Time `json:"started_at"`
}

// Chunk cursors count raw bytes. Data encodes as base64 over JSON so terminal
// control sequences and split UTF-8 runes remain lossless. Output is a readable
// model-facing projection; clients should consume Data for exact rendering.
type Chunk struct {
	Info      Info   `json:"terminal"`
	Data      []byte `json:"data"`
	Output    string `json:"output"`
	Cursor    uint64 `json:"cursor"`
	Earliest  uint64 `json:"earliest"`
	Truncated bool   `json:"truncated"`
}

type entry struct {
	originalOwner Owner
	transfer      *transferAttempt
	lastInput     *inputAttempt
	controlFrom   uint64
	reservation   Reservation
	mu            sync.Mutex
	input         sync.Mutex
	info          Info
	session       *pty.Session
	output        []byte
	base          uint64
	notify        chan struct{}
	done          chan struct{}
	readerDone    chan struct{}
	timer         *time.Timer
	closeOnce     sync.Once
}

func (e *entry) changed() { close(e.notify); e.notify = make(chan struct{}) }
func (e *entry) snapshot() Info {
	i := e.info
	if i.ExitCode != nil {
		code := *i.ExitCode
		i.ExitCode = &code
	}
	return i
}
func (e *entry) retire() {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		if e.timer != nil {
			e.timer.Stop()
		}
		e.mu.Unlock()
		if err := e.session.Close(); err != nil {
			e.mu.Lock()
			e.info.CleanupError = "Terminal process cleanup is unconfirmed: " + err.Error()
			e.mu.Unlock()
		}
	})
}
func (e *entry) close() {
	e.mu.Lock()
	if e.info.State == "running" {
		e.info.State = "closing"
		e.changed()
	}
	e.mu.Unlock()
	e.retire()
}

type Manager struct {
	mu        sync.Mutex
	workspace string
	options   Options
	entries   map[string]*entry
	closed    bool
}

func New(workspace string, options Options) (*Manager, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("terminal: workspace is not a directory")
	}
	return &Manager{workspace: canonical, options: defaults(options), entries: make(map[string]*entry)}, nil
}
func (m *Manager) directory(cwd string) (string, error) {
	if cwd == "" {
		cwd = m.workspace
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(m.workspace, cwd)
	}
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(m.workspace, real)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("terminal: cwd is outside workspace")
	}
	return real, nil
}

// Start owns the process after successful admission. The call context only
// gates launch; explicit owner/session cancellation closes owned terminals.
// Callers must apply their execution policy before calling Start.
func (m *Manager) Start(ctx context.Context, owner Owner, command, cwd string, rows, cols uint16) (result Info, resultErr error) {
	if !Supported() {
		return Info{}, fmt.Errorf("terminal: native interactive terminal support is unavailable on this host")
	}
	if !owner.valid() {
		return Info{}, fmt.Errorf("terminal: authenticated owner is required")
	}
	if rows > 1000 || cols > 1000 {
		return Info{}, fmt.Errorf("terminal: dimensions exceed 1000")
	}
	if strings.TrimSpace(command) == "" || len(command) > 64000 {
		return Info{}, fmt.Errorf("terminal: command must contain 1–64000 bytes")
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	dir, err := m.directory(cwd)
	if err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Info{}, ErrClosed
	}
	live := 0
	for _, e := range m.entries {
		e.mu.Lock()
		if e.info.State == "running" || e.info.State == "closing" {
			live++
		}
		e.mu.Unlock()
	}
	if live >= m.options.MaxLive {
		return Info{}, fmt.Errorf("terminal: live process limit reached")
	}
	if len(m.entries) >= m.options.MaxRetained {
		return Info{}, fmt.Errorf("terminal: retained handle limit reached; forget a closed terminal first")
	}
	e := &entry{originalOwner: owner, info: Info{ID: uuid.NewString(), Owner: owner, Command: command, CWD: dir, CanHandoff: m.HandoffAvailable(owner), State: "running", Controller: "agent", ControlRevision: 1, StartedAt: time.Now().UTC()}, session: pty.New(), notify: make(chan struct{}), done: make(chan struct{}), readerDone: make(chan struct{})}
	if m.options.Reserve != nil {
		e.reservation, err = m.options.Reserve(ctx, owner, e.info.ID)
		if err != nil {
			return Info{}, fmt.Errorf("terminal: workspace reservation: %w", err)
		}
		if e.reservation == nil {
			return Info{}, fmt.Errorf("terminal: missing workspace reservation")
		}
	}
	started, processStarted := false, false
	defer func() {
		if !started {
			if processStarted {
				if err := e.session.Close(); err != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("terminal: launch cleanup unconfirmed; workspace reservation retained: %w", err))
					return
				}
			}
			resultErr = errors.Join(resultErr, releaseReservation(e.reservation))
		}
	}()
	cmd := pty.ShellCommand(command)
	cmd.Dir = dir
	// Never inherit shell startup injection from the parent environment.
	for _, pair := range os.Environ() {
		key, _, _ := strings.Cut(pair, "=")
		switch strings.ToUpper(key) {
		case "BASH_ENV", "ENV", "PROMPT_COMMAND", "SHELLOPTS", "BASHOPTS", "ZDOTDIR":
			continue
		}
		cmd.Env = append(cmd.Env, pair)
	}
	if err := e.session.Start(cmd); err != nil {
		return Info{}, err
	}
	processStarted = true
	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 80
	}
	if err := e.session.Resize(rows, cols); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	m.entries[e.info.ID] = e
	started = true
	e.mu.Lock()
	e.timer = time.AfterFunc(m.options.MaxLifetime, e.close)
	initial := e.snapshot()
	e.mu.Unlock()
	go m.read(e)
	go e.watchReservation()
	go func() {
		code, _ := e.session.WaitExit()
		// Let the reader drain final bytes. A background descendant may keep the
		// PTY open; closing the owned process group bounds that case.
		select {
		case <-e.readerDone:
		case <-time.After(250 * time.Millisecond):
		}
		e.retire()
		<-e.readerDone
		e.mu.Lock()
		cleanupUnconfirmed := e.info.CleanupError != ""
		e.mu.Unlock()
		var cleanupErr error
		if !cleanupUnconfirmed {
			cleanupErr = releaseReservation(e.reservation)
		}
		e.mu.Lock()
		if cleanupErr != nil {
			e.info.CleanupError = "Workspace reservation cleanup failed; conflicting work remains blocked: " + cleanupErr.Error()
		}
		e.info.ExitCode = &code
		if e.info.State == "running" {
			e.info.State = "exited"
		} else {
			e.info.State = "closed"
		}
		e.changed()
		e.mu.Unlock()
		close(e.done)
	}()
	return initial, nil
}

func (m *Manager) read(e *entry) {
	defer close(e.readerDone)
	buf := make([]byte, 8192)
	for {
		n, err := e.session.Read(buf)
		if n > 0 {
			e.mu.Lock()
			e.output = append(e.output, buf[:n]...)
			if excess := len(e.output) - m.options.BufferBytes; excess > 0 {
				copy(e.output, e.output[excess:])
				e.output = e.output[:m.options.BufferBytes]
				e.base += uint64(excess)
			}
			e.changed()
			e.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}
func (m *Manager) get(owner Owner, id string, human bool) (*entry, error) {
	m.mu.Lock()
	e := m.entries[id]
	m.mu.Unlock()
	if e == nil || !owner.valid() {
		return nil, ErrNotFound
	}
	if err := e.authorized(owner, human); err != nil {
		return nil, err
	}
	return e, nil
}
func (m *Manager) List(owner Owner, human bool) []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []Info{}
	if !owner.valid() {
		return result
	}
	for _, e := range m.entries {
		e.mu.Lock()
		if e.info.Owner.Session == owner.Session && (human || e.info.Owner == owner) {
			result = append(result, e.snapshot())
		}
		e.mu.Unlock()
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt.Before(result[j].StartedAt) })
	return result
}
func (m *Manager) Read(ctx context.Context, owner Owner, id string, human bool, cursor uint64, limit int, wait time.Duration) (Chunk, error) {
	e, err := m.get(owner, id, human)
	if err != nil {
		return Chunk{}, err
	}
	if limit <= 0 || limit > 64*1024 {
		limit = 64 * 1024
	}
	if wait < 0 || wait > time.Second {
		wait = time.Second
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		e.mu.Lock()
		if e.info.Owner.Session != owner.Session || (!human && e.info.Owner != owner) {
			e.mu.Unlock()
			if !human && e.originalOwner == owner {
				return Chunk{}, ErrOwnershipTransferred
			}
			return Chunk{}, ErrNotFound
		}

		end := e.base + uint64(len(e.output))
		if cursor > end {
			e.mu.Unlock()
			return Chunk{}, fmt.Errorf("terminal: cursor is beyond output")
		}
		lost := cursor < e.base
		if lost {
			cursor = e.base
		}
		available := int(end - cursor)
		if available > limit {
			available = limit
		}
		data := append([]byte(nil), e.output[int(cursor-e.base):int(cursor-e.base)+available]...)
		chunk := Chunk{Info: e.snapshot(), Data: data, Output: strings.ToValidUTF8(string(data), "�"), Cursor: cursor + uint64(available), Earliest: e.base, Truncated: lost}
		notify := e.notify
		e.mu.Unlock()
		if len(data) > 0 || wait == 0 || chunk.Info.State != "running" {
			return chunk, nil
		}
		select {
		case <-ctx.Done():
			return Chunk{}, ctx.Err()
		case <-deadline.C:
			return chunk, nil
		case <-notify:
		}
	}
}

// Write is a trusted in-process compatibility API. Model and human-client
// protocols use WriteInput, with a revision and sequence for retry safety.
func (m *Manager) Write(ctx context.Context, owner Owner, id string, human bool, data []byte) (int, error) {
	if len(data) > 16*1024 {
		return 0, fmt.Errorf("terminal: input exceeds 16 KiB")
	}
	e, err := m.get(owner, id, human)
	if err != nil {
		return 0, err
	}
	e.input.Lock()
	defer e.input.Unlock()
	if err := e.inputAdmission(ctx, owner, human, 0); err != nil {
		return 0, err
	}
	return e.writeBytes(ctx, data)
}
func (m *Manager) Resize(owner Owner, id string, human bool, rows, cols uint16) error {
	return m.resize(owner, id, human, 0, rows, cols)
}
func (m *Manager) resize(owner Owner, id string, human bool, revision uint64, rows, cols uint16) error {
	if rows == 0 || cols == 0 || rows > 1000 || cols > 1000 {
		return fmt.Errorf("terminal: rows/columns must be between 1 and 1000")
	}
	e, err := m.get(owner, id, human)
	if err != nil {
		return err
	}
	e.input.Lock()
	defer e.input.Unlock()
	if err := e.authorized(owner, human); err != nil {
		return err
	}
	e.mu.Lock()
	current := e.info.ControlRevision
	e.mu.Unlock()
	if revision != 0 && revision != current {
		return ErrControlChanged
	}

	if err := e.validateReservation(context.Background()); err != nil {
		return err
	}
	e.mu.Lock()
	humanControl := e.info.Controller == "human"
	e.mu.Unlock()
	if !human && humanControl {
		return ErrHumanControl
	}
	return e.session.Resize(rows, cols)
}
func (m *Manager) TakeControl(owner Owner, id string, human bool, release bool) error {
	_, err := m.takeControl(owner, id, human, 0, release)
	return err
}
func (m *Manager) CloseTerminal(owner Owner, id string, human bool) error {
	return m.closeTerminal(owner, id, human, 0)
}
func (m *Manager) closeTerminal(owner Owner, id string, human bool, revision uint64) error {
	e, err := m.get(owner, id, human)
	if err != nil {
		return err
	}
	e.input.Lock()
	defer e.input.Unlock()
	if err := e.authorized(owner, human); err != nil {
		return err
	}
	e.mu.Lock()
	current := e.info.ControlRevision
	e.mu.Unlock()
	if revision != 0 && revision != current {
		return ErrControlChanged
	}

	e.mu.Lock()
	humanControl := e.info.Controller == "human"
	e.mu.Unlock()
	if !human && humanControl {
		return ErrHumanControl
	}
	e.close()
	select {
	case <-e.done:
		e.mu.Lock()
		message := e.info.CleanupError
		e.mu.Unlock()
		if message != "" {
			return fmt.Errorf("%s", message)
		}
		return nil
	case <-time.After(3 * time.Second):
		return fmt.Errorf("terminal: process termination not confirmed")
	}
}
func (m *Manager) Forget(owner Owner, id string, human bool) error {
	e, err := m.get(owner, id, human)
	if err != nil {
		return err
	}
	e.input.Lock()
	defer e.input.Unlock()
	if err := e.authorized(owner, human); err != nil {
		return err
	}
	e.mu.Lock()
	state := e.info.State
	e.mu.Unlock()
	if state == "running" {
		return fmt.Errorf("terminal: close before forgetting")
	}
	select {
	case <-e.done:
	default:
		return fmt.Errorf("terminal: process termination not confirmed")
	}
	m.mu.Lock()
	delete(m.entries, id)
	m.mu.Unlock()
	return nil
}

// Stop retires live processes but keeps the manager available for later turns.
func (m *Manager) Stop() error {
	m.mu.Lock()
	entries := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	m.mu.Unlock()
	for _, e := range entries {
		e.close()
	}
	var result error
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for _, e := range entries {
		select {
		case <-e.done:
			e.mu.Lock()
			message := e.info.CleanupError
			e.mu.Unlock()
			if message != "" {
				result = errors.Join(result, fmt.Errorf("%s", message))
			}
		case <-deadline.C:
			return errors.Join(result, fmt.Errorf("terminal: owned process termination not confirmed"))
		}
	}
	return result
}
func (m *Manager) CloseOwner(owner Owner) error {
	var result error
	for _, i := range m.List(owner, false) {
		result = errors.Join(result, m.closeOwned(owner, i.ID))
	}
	return result
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	entries := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	m.mu.Unlock()
	for _, e := range entries {
		e.close()
	}
	var result error
	for _, e := range entries {
		select {
		case <-e.done:
		case <-time.After(3 * time.Second):
			result = errors.Join(result, fmt.Errorf("terminal: process termination not confirmed for %s", e.info.ID))
		}
	}
	return result
}
