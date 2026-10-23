package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
)

// Position is an LSP zero-based line/character position.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an LSP range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location pins a Range inside a URI-addressed file.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// SymbolInformation is the legacy LSP workspace/document symbol shape — the
// subset we actually consume.
type SymbolInformation struct {
	Name     string   `json:"name"`
	Kind     int      `json:"kind"`
	Location Location `json:"location"`
}

// Hover is a hover response. Markdown content is in `Contents` as a string.
type Hover struct {
	Contents string `json:"-"`
	Range    *Range `json:"range,omitempty"`
}

// Client is one language-server stdio connection.
type Client struct {
	language string
	cmd      *exec.Cmd
	conn     *jsonrpcConn

	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu        sync.RWMutex
	connected bool
	rootURI   string
	lastErr   error

	maxMessageBytes int
	command         string
	args            []string
	started         bool
	closing         bool
	startDone       chan struct{}
	startErr        error
	runCancel       context.CancelFunc
	runDone         chan struct{}
	readers         sync.WaitGroup
	closeOnce       sync.Once
}

// NewClient constructs (but does not start) a client.
func NewClient(language, command string, args []string, maxMessageBytes int) *Client {
	cmd := exec.Command(command, args...)
	cmd.Env = os.Environ()
	return &Client{language: language, cmd: cmd, command: command, args: append([]string(nil), args...), maxMessageBytes: maxMessageBytes}
}

// Language returns the language name this client serves.
func (c *Client) Language() string { return c.language }

// Connected returns true once the LSP initialize handshake has completed.
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// LastError returns the last connection error.
func (c *Client) LastError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastErr
}

// validatePath checks canonical workspace containment, including symlinked
// ancestors of files that do not exist yet. This constrains queries; it does
// not sandbox the configured language-server executable or prevent concurrent
// filesystem mutations by other processes.
func (c *Client) validatePath(file string) error {
	_, err := c.workspacePath(file)
	return err
}

func (c *Client) workspacePath(file string) (string, error) {
	c.mu.RLock()
	root := c.rootURI
	c.mu.RUnlock()
	if root == "" {
		return "", fmt.Errorf("lsp: workspace root unavailable")
	}
	if file == "" || strings.ContainsRune(file, 0) {
		return "", fmt.Errorf("lsp: invalid file path")
	}
	rootPath, err := filepath.Abs(URIToPath(root))
	if err != nil {
		return "", fmt.Errorf("lsp: cannot resolve workspace")
	}
	realRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", fmt.Errorf("lsp: cannot resolve workspace: %w", err)
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(rootPath, file)
	}
	file, err = filepath.Abs(file)
	if err != nil {
		return "", fmt.Errorf("lsp: cannot resolve path")
	}
	// Canonicalization accepts a configured root's spelling as well as its
	// canonical spelling (e.g. /tmp and /private/tmp on macOS).
	realFile, err := resolveExistingAncestor(file)
	if err != nil {
		return "", fmt.Errorf("lsp: cannot resolve file: %w", err)
	}
	rel, err := filepath.Rel(realRoot, realFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("lsp: path outside workspace: %s", file)
	}
	return realFile, nil
}

func resolveExistingAncestor(path string) (string, error) {
	var tail []string
	for {
		_, err := os.Lstat(path)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", err
			} // dangling links fail closed
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		tail = append(tail, filepath.Base(path))
		path = parent
	}
}

func (c *Client) validateLocations(locations []Location) error {
	for _, location := range locations {
		u, err := url.Parse(location.URI)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(u.Path) {
			return fmt.Errorf("lsp: invalid local result URI")
		}
		if err := c.validatePath(u.Path); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) validateSymbols(symbols []SymbolInformation) error {
	for _, symbol := range symbols {
		if err := c.validateLocations([]Location{symbol.Location}); err != nil {
			return err
		}
	}
	return nil
}

// InitializeTimeout bounds readiness independently of the session lifetime.
const InitializeTimeout = 5 * time.Second
const shutdownTimeout = 250 * time.Millisecond

// Start owns the server until ctx is canceled or Close is called. Concurrent
// starts share one initialize attempt; Close is terminal and waits for reaping.
func (c *Client) Start(ctx context.Context, rootDir string) error {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return fmt.Errorf("lsp: client closed")
	}
	if c.started {
		done := c.startDone
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
		c.mu.RLock()
		err := c.startErr
		c.mu.RUnlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return err
	}
	c.started = true
	c.startDone = make(chan struct{})
	rootDir, err := filepath.Abs(rootDir)
	if err == nil {
		rootDir, err = filepath.EvalSymlinks(rootDir)
	}
	if err == nil {
		command := c.command
		if command != "" {
			if !filepath.IsAbs(command) && strings.ContainsAny(command, "/\\") {
				command = filepath.Join(rootDir, command)
			}
			c.cmd = exec.Command(command, c.args...)
			c.cmd.Env = os.Environ()
		}
		if c.cmd == nil {
			err = fmt.Errorf("no server command")
		}
	}
	if err == nil {
		c.cmd.Dir = rootDir
		c.stdin, err = c.cmd.StdinPipe()
	}
	if err == nil {
		c.stdout, err = c.cmd.StdoutPipe()
	}
	if err == nil {
		c.stderr, err = c.cmd.StderrPipe()
	}
	if err != nil {
		for _, pipe := range []io.Closer{c.stdin, c.stdout, c.stderr} {
			if pipe != nil {
				_ = pipe.Close()
			}
		}
		c.startErr = fmt.Errorf("lsp: setup %s: %.512s", c.language, err.Error())
		c.lastErr = c.startErr
		close(c.startDone)
		c.mu.Unlock()
		return c.startErr
	}
	c.rootURI = pathToURI(rootDir)
	rootURI := c.rootURI
	conn := newJSONRPCConn(c.stdin, c.stdout, c.maxMessageBytes)
	c.conn = conn
	lifeCtx, cancel := context.WithCancel(ctx)
	c.runCancel = cancel
	c.runDone = make(chan struct{})
	c.readers.Add(2)
	go func() {
		defer c.readers.Done()
		if err := conn.readLoop(); err != nil {
			c.setError(err)
		}
	}()
	go func() { defer c.readers.Done(); _, _ = io.Copy(io.Discard, c.stderr) }()
	go func() {
		defer close(c.runDone)
		err := processrunner.Run(lifeCtx, c.cmd)
		if err == nil {
			err = io.EOF
		}
		c.setError(err)
		conn.close()
		_ = c.stdout.Close()
		_ = c.stderr.Close()
		_ = c.stdin.Close()
	}()
	c.mu.Unlock()
	handshakeCtx, handshakeCancel := context.WithTimeout(lifeCtx, InitializeTimeout)
	defer handshakeCancel()
	initParams := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"definition":     map[string]any{"linkSupport": false},
				"references":     map[string]any{},
				"hover":          map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"documentSymbol": map[string]any{},
			},
			"workspace": map[string]any{
				"symbol": map[string]any{},
			},
		},
		"clientInfo": map[string]any{"name": "overkill", "version": "0.1"},
	}

	_, err = conn.Call(handshakeCtx, "initialize", initParams)
	if err == nil {
		err = conn.NotifyContext(handshakeCtx, "initialized", map[string]any{})
	}
	c.mu.Lock()
	if err == nil && (c.closing || lifeCtx.Err() != nil || conn.closed.Load()) {
		err = fmt.Errorf("connection closed during initialize")
	}
	if err == nil {
		c.connected = true
		c.lastErr = nil
	}
	c.mu.Unlock()
	if err != nil {
		// Do not retain an unbounded server error payload in readiness results.
		err = errors.Join(fmt.Errorf("lsp: initialize %s: %.512s", c.language, err.Error()), handshakeCtx.Err())
		c.setError(err)
		c.stopOwned()
	}
	c.mu.Lock()
	c.startErr = err
	close(c.startDone)
	c.mu.Unlock()
	return err
}

// stopOwned can run concurrently with Close/startup failure. Pipe closure and
// process cancellation unblock both reads and writes; only processrunner waits.
func (c *Client) stopOwned() {
	c.mu.RLock()
	cancel, conn, done := c.runCancel, c.conn, c.runDone
	stdin, stdout, stderr := c.stdin, c.stdout, c.stderr
	c.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		conn.close()
	}
	for _, pipe := range []io.Closer{stdin, stdout, stderr} {
		if pipe != nil {
			_ = pipe.Close()
		}
	}
	if done != nil {
		<-done
	}
	c.readers.Wait()
}

func (c *Client) setError(err error) {
	c.mu.Lock()
	c.lastErr = err
	c.connected = false
	c.mu.Unlock()
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.RLock()
	conn := c.conn
	closing := c.closing
	c.mu.RUnlock()
	if conn == nil || closing {
		return nil, fmt.Errorf("lsp: connection unavailable")
	}
	return conn.Call(ctx, method, params)
}

// Definition returns the definition site(s) for the symbol at file:line:col.
func (c *Client) Definition(ctx context.Context, file string, line, col int) ([]Location, error) {
	canonicalFile, err := c.workspacePath(file)
	if err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(canonicalFile)},
		"position":     Position{Line: line, Character: col},
	})
	if err != nil {
		return nil, err
	}
	locations, err := decodeLocations(raw)
	if err != nil {
		return nil, err
	}
	if err := c.validateLocations(locations); err != nil {
		return nil, err
	}
	return locations, nil
}

// References returns usage sites of the symbol at file:line:col.
func (c *Client) References(ctx context.Context, file string, line, col int) ([]Location, error) {
	canonicalFile, err := c.workspacePath(file)
	if err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(canonicalFile)},
		"position":     Position{Line: line, Character: col},
		"context":      map[string]any{"includeDeclaration": true},
	})
	if err != nil {
		return nil, err
	}
	locations, err := decodeLocations(raw)
	if err != nil {
		return nil, err
	}
	if err := c.validateLocations(locations); err != nil {
		return nil, err
	}
	return locations, nil
}

// Hover returns the hover doc for the symbol at file:line:col as plain text.
func (c *Client) Hover(ctx context.Context, file string, line, col int) (Hover, error) {
	canonicalFile, err := c.workspacePath(file)
	if err != nil {
		return Hover{}, err
	}
	raw, err := c.call(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(canonicalFile)},
		"position":     Position{Line: line, Character: col},
	})
	if err != nil {
		return Hover{}, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return Hover{}, nil
	}
	var resp struct {
		Contents any    `json:"contents"`
		Range    *Range `json:"range,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Hover{}, err
	}
	return Hover{Contents: flattenHover(resp.Contents), Range: resp.Range}, nil
}

// DocumentSymbols returns the outline for a single file.
func (c *Client) DocumentSymbols(ctx context.Context, file string) ([]SymbolInformation, error) {
	canonicalFile, err := c.workspacePath(file)
	if err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(canonicalFile)},
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	// Servers may return SymbolInformation[] or DocumentSymbol[] — try the
	// flat shape first since that's what we consume.
	var flat []SymbolInformation
	if err := json.Unmarshal(raw, &flat); err == nil && len(flat) > 0 {
		if err := c.validateSymbols(flat); err != nil {
			return nil, err
		}
		return flat, nil
	}
	// Fallback: ignore the hierarchical DocumentSymbol shape — we just
	// return what we can parse rather than failing.
	return nil, nil
}

// WorkspaceSymbols searches across the workspace for matching symbols.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]SymbolInformation, error) {
	raw, err := c.call(ctx, "workspace/symbol", map[string]any{"query": query})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out []SymbolInformation
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if err := c.validateSymbols(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Close attempts the LSP shutdown/exit protocol within a fixed grace period,
// then cancels the owned process group and waits for process/reader reaping.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closing = true
		connected, conn, started := c.connected, c.conn, c.started
		runDone := c.runDone
		done := c.startDone
		c.connected = false
		c.mu.Unlock()
		if connected && conn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			if _, err := conn.Call(ctx, "shutdown", nil); err == nil {
				if err := conn.NotifyContext(ctx, "exit", nil); err == nil && runDone != nil {
					select {
					case <-runDone:
					case <-ctx.Done():
					}
				}
			}
			cancel()
		}
		c.stopOwned()
		if started && done != nil {
			<-done
		}
	})
	return nil
}

// decodeLocations handles the union return of definition (Location | Location[]).
func decodeLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single Location
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return []Location{single}, nil
	}
	var arr []Location
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	return nil, fmt.Errorf("lsp: unrecognized location shape")
}

func flattenHover(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if s, ok := t["value"].(string); ok {
			return s
		}
	case []any:
		var parts []string
		for _, item := range t {
			parts = append(parts, flattenHover(item))
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func pathToURI(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "file://") {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	u := &url.URL{Scheme: "file", Path: abs}
	return u.String()
}

// URIToPath converts a file:// URI back to a local filesystem path.
func URIToPath(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return uri
	}
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	return u.Path
}
