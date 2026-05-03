package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
)

// Manager couples a session.Store with a Backend and provides high-level
// push/pull/status operations. All methods are safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	store    session.Store
	backend  Backend
	hostname string
	lastPush time.Time
	lastPull time.Time
}

func NewManager(store session.Store, backend Backend) *Manager {
	host, _ := os.Hostname()
	return &Manager{store: store, backend: backend, hostname: host}
}

// Backend exposes the underlying backend (nil when sync is disabled).
func (m *Manager) Backend() Backend { return m.backend }

// Status is a snapshot of last push/pull times for the /sync UI.
type Status struct {
	Backend  string    `json:"backend"`
	LastPush time.Time `json:"last_push"`
	LastPull time.Time `json:"last_pull"`
	Local    int       `json:"local"`
	Remote   int       `json:"remote"`
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	if m.backend == nil {
		return Status{Backend: "disabled"}, nil
	}
	m.mu.Lock()
	st := Status{Backend: m.backend.Name(), LastPush: m.lastPush, LastPull: m.lastPull}
	m.mu.Unlock()
	if locals, err := m.store.List(ctx, session.ListOptions{}); err == nil {
		st.Local = len(locals)
	}
	if remotes, err := m.backend.List(ctx); err == nil {
		st.Remote = len(remotes)
	}
	return st, nil
}

// EncodeSession marshals + gzips a session for transport.
func EncodeSession(s *session.Session) ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("sync: marshal: %w", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		return nil, fmt.Errorf("sync: gzip: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("sync: gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

// DecodeSession reverses EncodeSession.
func DecodeSession(data []byte) (*session.Session, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("sync: gunzip: %w", err)
	}
	defer gz.Close()
	raw, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("sync: read: %w", err)
	}
	var s session.Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("sync: unmarshal: %w", err)
	}
	return &s, nil
}

func (m *Manager) PushOne(ctx context.Context, id string) error {
	if m.backend == nil {
		return fmt.Errorf("sync: backend disabled")
	}
	s, err := m.store.Load(ctx, id)
	if err != nil {
		return fmt.Errorf("sync: load %s: %w", id, err)
	}
	data, err := EncodeSession(s)
	if err != nil {
		return err
	}
	meta := SessionMeta{
		ID:           s.ID,
		Title:        s.Title,
		UpdatedAt:    s.UpdatedAt,
		MessageCount: len(s.Messages),
		OriginHost:   m.hostname,
		Size:         len(data),
	}
	if err := m.backend.Push(ctx, id, data, meta); err != nil {
		return err
	}
	m.mu.Lock()
	m.lastPush = time.Now().UTC()
	m.mu.Unlock()
	return nil
}

func (m *Manager) PushAll(ctx context.Context) (int, error) {
	if m.backend == nil {
		return 0, fmt.Errorf("sync: backend disabled")
	}
	sessions, err := m.store.List(ctx, session.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("sync: list local: %w", err)
	}
	pushed := 0
	for _, s := range sessions {
		if err := m.PushOne(ctx, s.ID); err != nil {
			return pushed, err
		}
		pushed++
	}
	return pushed, nil
}

// Resolve picks the winner between local and remote with last-write-wins on
// UpdatedAt. The loser is preserved as a side copy with `_conflict-<unix>`
// suffix in its ID and Title; both local and conflict copies are returned.
func (m *Manager) Resolve(local, remote *session.Session) (winner, loser *session.Session) {
	if local == nil {
		return remote, nil
	}
	if remote == nil {
		return local, nil
	}
	if remote.UpdatedAt.After(local.UpdatedAt) {
		conflict := *local
		conflict.ID = local.ID + "_conflict-" + fmt.Sprintf("%d", time.Now().Unix())
		conflict.Title = "[conflict] " + local.Title
		return remote, &conflict
	}
	conflict := *remote
	conflict.ID = remote.ID + "_conflict-" + fmt.Sprintf("%d", time.Now().Unix())
	conflict.Title = "[conflict] " + remote.Title
	return local, &conflict
}

func (m *Manager) PullOne(ctx context.Context, id string) error {
	if m.backend == nil {
		return fmt.Errorf("sync: backend disabled")
	}
	data, _, err := m.backend.Pull(ctx, id)
	if err != nil {
		return err
	}
	remote, err := DecodeSession(data)
	if err != nil {
		return err
	}
	local, err := m.store.Load(ctx, id)
	if err != nil {
		// Treat any load error as "local missing" — Create.
		if cerr := m.store.Create(ctx, remote); cerr != nil {
			return fmt.Errorf("sync: create from remote: %w", cerr)
		}
		m.mu.Lock()
		m.lastPull = time.Now().UTC()
		m.mu.Unlock()
		return nil
	}

	winner, loser := m.Resolve(local, remote)
	if loser != nil {
		_ = m.store.Create(ctx, loser)
	}
	winner.UpdatedAt = remote.UpdatedAt
	if err := m.store.Save(ctx, winner); err != nil {
		return fmt.Errorf("sync: save winner: %w", err)
	}
	m.mu.Lock()
	m.lastPull = time.Now().UTC()
	m.mu.Unlock()
	return nil
}

func (m *Manager) PullAll(ctx context.Context) (int, error) {
	if m.backend == nil {
		return 0, fmt.Errorf("sync: backend disabled")
	}
	metas, err := m.backend.List(ctx)
	if err != nil {
		return 0, err
	}
	pulled := 0
	for _, mt := range metas {
		if err := m.PullOne(ctx, mt.ID); err != nil {
			return pulled, err
		}
		pulled++
	}
	return pulled, nil
}
