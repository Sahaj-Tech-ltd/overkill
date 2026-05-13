package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
}

// NewClient constructs (but does not start) a client.
func NewClient(language, command string, args []string) *Client {
	cmd := exec.Command(command, args...)
	cmd.Env = os.Environ()
	return &Client{language: language, cmd: cmd}
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

// Start spawns the server and runs the LSP `initialize` handshake. rootDir
// is sent as the workspace root.
func (c *Client) Start(ctx context.Context, rootDir string) error {
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return err
	}
	c.stdin, c.stdout, c.stderr = stdin, stdout, stderr

	if err := c.cmd.Start(); err != nil {
		c.setError(err)
		return fmt.Errorf("lsp: start %s: %w", c.language, err)
	}

	c.conn = newJSONRPCConn(stdin, stdout)
	go func() { _ = c.conn.readLoop() }()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := stderr.Read(buf); err != nil {
				return
			}
		}
	}()

	rootURI := pathToURI(rootDir)
	c.rootURI = rootURI
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
	if _, err := c.conn.Call(ctx, "initialize", initParams); err != nil {
		c.setError(err)
		return fmt.Errorf("lsp: initialize %s: %w", c.language, err)
	}
	if err := c.conn.Notify("initialized", map[string]any{}); err != nil {
		c.setError(err)
		return err
	}
	c.mu.Lock()
	c.connected = true
	c.lastErr = nil
	c.mu.Unlock()
	return nil
}

func (c *Client) setError(err error) {
	c.mu.Lock()
	c.lastErr = err
	c.connected = false
	c.mu.Unlock()
}

// Definition returns the definition site(s) for the symbol at file:line:col.
func (c *Client) Definition(ctx context.Context, file string, line, col int) ([]Location, error) {
	raw, err := c.conn.Call(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(file)},
		"position":     Position{Line: line, Character: col},
	})
	if err != nil {
		return nil, err
	}
	return decodeLocations(raw)
}

// References returns usage sites of the symbol at file:line:col.
func (c *Client) References(ctx context.Context, file string, line, col int) ([]Location, error) {
	raw, err := c.conn.Call(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(file)},
		"position":     Position{Line: line, Character: col},
		"context":      map[string]any{"includeDeclaration": true},
	})
	if err != nil {
		return nil, err
	}
	return decodeLocations(raw)
}

// Hover returns the hover doc for the symbol at file:line:col as plain text.
func (c *Client) Hover(ctx context.Context, file string, line, col int) (Hover, error) {
	raw, err := c.conn.Call(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(file)},
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
	raw, err := c.conn.Call(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(file)},
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
		return flat, nil
	}
	// Fallback: ignore the hierarchical DocumentSymbol shape — we just
	// return what we can parse rather than failing.
	return nil, nil
}

// WorkspaceSymbols searches across the workspace for matching symbols.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]SymbolInformation, error) {
	raw, err := c.conn.Call(ctx, "workspace/symbol", map[string]any{"query": query})
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
	return out, nil
}

// Close terminates the server subprocess.
func (c *Client) Close() error {
	if c.conn != nil {
		c.conn.close()
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
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
