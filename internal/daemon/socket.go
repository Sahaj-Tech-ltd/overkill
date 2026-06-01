// Package daemon — UNIX-socket RPC between the running daemon and CLI
// or TUI clients. Line-delimited JSON over a stream socket; one request
// per line, one response per line. Small surface on purpose — anything
// richer than ping/list/cancel either grows the protocol explicitly or
// reads/writes the Postgres DB directly (the daemon and clients share the
// same database).
//
// Why UNIX socket and not gRPC / HTTP?
//   - Zero dependencies; net package only
//   - Filesystem permissions act as the access boundary (0600 on the
//     socket file → same user only)
//   - No port allocation, no localhost binding to fight firewalls
//   - Trivial to test with a t.TempDir() socket path
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SocketPath returns the canonical daemon socket path. Distinct from
// the pidfile because some platforms (macOS) restrict socket paths to
// 104 chars; we keep the directory shallow.
func SocketPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".overkill", "daemon.sock"), nil
}

// Request is one inbound RPC. Op is required; Params is op-specific.
type Request struct {
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is one outbound RPC reply. Either Result or Err is set;
// never both. Code lets clients branch on machine-readable error kinds
// without parsing the human Err string.
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Err    string          `json:"error,omitempty"`
	Code   string          `json:"code,omitempty"`
}

// Handler resolves one request to a response. Returning a non-nil
// error short-circuits to a Response with the error's Error() as Err
// and Code "internal".
type Handler func(ctx context.Context, req Request) (Response, error)

// Server owns the listener + accept loop. Stop() is idempotent so
// shutdown paths (signal + explicit) can both call it without racing.
type Server struct {
	path     string
	ln       net.Listener
	handlers map[string]Handler
	wg       sync.WaitGroup
	mu       sync.Mutex
	closed   bool
	running  bool
}

// NewServer returns an unstarted server bound to path. The path is
// removed first if a stale socket exists from a prior process; we
// don't try to detect liveness — the caller's pidfile check is the
// authority for "another daemon is running."
func NewServer(path string) *Server {
	return &Server{
		path:     path,
		handlers: map[string]Handler{},
	}
}

// Register binds op → handler. Last write wins so the daemon's wiring
// can override the built-in ping if it ever wants to.
func (s *Server) Register(op string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[op] = h
}

// Start begins accepting connections. Returns after the listener is
// bound; the accept loop runs in a goroutine until Stop().
func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("daemon socket: already running")
	}
	s.running = true
	s.mu.Unlock()

	// Remove a leftover socket from a prior crash. ENOENT is fine.
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daemon socket: cleanup: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("daemon socket: mkdir: %w", err)
	}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("daemon socket: listen: %w", err)
	}
	// 0600 means the daemon's user owns the socket — anyone else can't
	// even connect. This is our access control.
	if err := os.Chmod(s.path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("daemon socket: chmod: %w", err)
	}
	s.ln = ln
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Stop tears down the listener and waits for in-flight conns. Safe to
// call multiple times.
func (s *Server) Stop() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.running = false
	ln := s.ln
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	s.wg.Wait()
	_ = os.Remove(s.path)
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			// Stop() closes the listener; that surfaces here as a
			// permanent error. Exit the loop quietly.
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			// Transient error — backoff briefly so we don't busy-loop.
			time.Sleep(100 * time.Millisecond)
			continue
		}
		s.wg.Add(1)
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()
	// Per-connection deadline guards against a misbehaving client that
	// opens the socket then never writes — 30s is generous for any
	// real RPC and short enough to free resources from a stuck client.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeErr(conn, "bad_request", "invalid JSON: "+err.Error())
		return
	}

	s.mu.Lock()
	h, ok := s.handlers[req.Op]
	s.mu.Unlock()
	if !ok {
		s.writeErr(conn, "unknown_op", "unknown op: "+req.Op)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	resp, err := h(ctx, req)
	if err != nil {
		s.writeErr(conn, "internal", err.Error())
		return
	}
	s.writeResp(conn, resp)
}

func (s *Server) writeResp(conn net.Conn, resp Response) {
	b, err := json.Marshal(resp)
	if err != nil {
		s.writeErr(conn, "marshal", err.Error())
		return
	}
	_, _ = conn.Write(append(b, '\n'))
}

func (s *Server) writeErr(conn net.Conn, code, msg string) {
	b, _ := json.Marshal(Response{Err: msg, Code: code})
	_, _ = conn.Write(append(b, '\n'))
}

// Client is a one-shot RPC caller. Each Call opens a fresh connection
// — the daemon socket is not heavily used and pooling adds complexity
// (idle conn timeouts, broken-pipe detection) for negligible win.
type Client struct {
	path    string
	timeout time.Duration
}

// NewClient returns a Client bound to the standard daemon socket path
// or a custom path (tests). Default timeout 5s; raise if a downstream
// op is known to be slow.
func NewClient(path string) *Client {
	return &Client{path: path, timeout: 5 * time.Second}
}

// WithTimeout returns a copy with a different per-call deadline.
func (c *Client) WithTimeout(d time.Duration) *Client {
	cp := *c
	cp.timeout = d
	return &cp
}

// Call sends op+params and returns the result bytes. ErrDaemonDown is
// returned when the socket can't be reached at all — useful for the
// CLI to surface "is the daemon running?" hints.
func (c *Client) Call(op string, params any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", c.path, c.timeout)
	if err != nil {
		return nil, ErrDaemonDown
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.timeout))

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("daemon client: marshal params: %w", err)
		}
		rawParams = b
	}
	reqBytes, err := json.Marshal(Request{Op: op, Params: rawParams})
	if err != nil {
		return nil, fmt.Errorf("daemon client: marshal req: %w", err)
	}
	if _, err := conn.Write(append(reqBytes, '\n')); err != nil {
		return nil, fmt.Errorf("daemon client: write: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("daemon client: read: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("daemon client: parse: %w", err)
	}
	if resp.Err != "" {
		return nil, &RPCError{Code: resp.Code, Message: resp.Err}
	}
	return resp.Result, nil
}

// ErrDaemonDown signals "couldn't reach the daemon socket at all" — as
// opposed to "the daemon reached returned an error". Lets the CLI tell
// the user "start the daemon first" instead of leaking a socket-not-
// found error from net.Dial.
var ErrDaemonDown = errors.New("daemon: not running (socket not reachable)")

// RPCError wraps a daemon-returned error. Code is the machine-readable
// kind; Message is the human description.
type RPCError struct {
	Code    string
	Message string
}

func (e *RPCError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("daemon: %s: %s", e.Code, e.Message)
	}
	return "daemon: " + e.Message
}
