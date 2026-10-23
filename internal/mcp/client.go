package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2024-11-05"

// Tool describes an MCP tool exposed by a server.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Resource is an MCP resource (e.g. a file or document).
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// Result wraps a tool/call response. The MCP spec returns a list of content
// items; we keep the raw payload alongside a flattened text representation
// for callers that just want the output as a string.
type Result struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
	Text    string        `json:"-"`
}

// ToolExecutionError is an MCP tools/call result marked isError. The original
// content is retained for diagnostics while Go callers receive an actual failure.
type ToolExecutionError struct {
	Server string
	Tool   string
	Result Result
}

func (err *ToolExecutionError) Error() string {
	message := err.Result.Text
	if message == "" {
		message = "remote tool reported failure without text"
	}
	if len(message) > 2048 {
		message = message[:2048] + " [truncated]"
	}
	return fmt.Sprintf("mcp: %s/%s: %s", err.Server, err.Tool, message)
}

// ContentItem matches the MCP `content` block (text/image/resource).
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Client is a single MCP server connection over stdio.
type Client struct {
	name string
	cmd  *exec.Cmd
	conn *jsonrpcConn

	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu        sync.RWMutex
	connected bool
	tools     []Tool
	lastErr   error

	// wg tracks background goroutines (readLoop + stderr drainer) so
	// that Start can wait for them to exit on initialization failure.
	wg        sync.WaitGroup
	runCancel context.CancelFunc
	runDone   chan struct{}
	readDone  chan struct{}
	closeMu   sync.Mutex
}

// NewClient prepares a client; call Start to spawn the subprocess and run
// the JSON-RPC handshake.
func NewClient(name, command string, args []string, env map[string]string) *Client {
	// Validate that command resolves to an executable. exec.LookPath
	// checks PATH for bare names and verifies filesystem existence for
	// absolute/relative paths. A bad command is stored as lastErr so
	// Start() can surface the failure cleanly (B107).
	if _, err := exec.LookPath(command); err != nil {
		return &Client{name: name, lastErr: fmt.Errorf("mcp: command %q not found: %w", command, err)}
	}
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		envv := os.Environ()
		for k, v := range env {
			envv = append(envv, k+"="+v)
		}
		cmd.Env = envv
	}
	return &Client{name: name, cmd: cmd}
}

// Name returns the configured server name.
func (c *Client) Name() string { return c.name }

// Start launches the subprocess, runs the MCP handshake, and caches the
// tool list. Returns once the server has been initialized.
func (c *Client) Start(ctx context.Context) error {
	if c.cmd == nil {
		if c.lastErr != nil {
			return c.lastErr
		}
		return fmt.Errorf("mcp: no command configured for %s", c.name)
	}
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdin: %w", err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdout: %w", err)
	}
	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("mcp: stderr: %w", err)
	}
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr

	c.conn = newJSONRPCConn(stdin, stdout)
	runCtx, cancel := context.WithCancel(ctx)
	c.runCancel = cancel
	c.runDone = make(chan struct{})
	c.readDone = make(chan struct{})
	c.wg.Add(2)
	go func() {
		defer c.wg.Done()
		defer close(c.readDone)
		err := c.conn.readLoop()
		if err == nil {
			err = io.EOF
		}
		c.setError(err)
	}()
	go func() { defer c.wg.Done(); _, _ = io.Copy(io.Discard, stderr) }()
	go func() {
		defer close(c.runDone)
		err := processrunner.Run(runCtx, c.cmd)
		if err == nil {
			err = io.EOF
		}
		c.setError(err)
		c.conn.close()
		_ = stdout.Close()
		_ = stderr.Close()
	}()
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer handshakeCancel()
	initParams := map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{"tools": map[string]any{}, "resources": map[string]any{}}, "clientInfo": map[string]any{"name": "overkill", "version": "0.1"}}
	if _, err := c.conn.Call(handshakeCtx, "initialize", initParams); err != nil {
		_ = c.Close()
		return fmt.Errorf("mcp: initialization failed for %s", c.name)
	}
	if err := c.conn.Notify("notifications/initialized", map[string]any{}); err != nil {
		_ = c.Close()
		return fmt.Errorf("mcp: initialization notification failed for %s", c.name)
	}
	tools, err := c.ListTools(handshakeCtx)
	if err != nil {
		_ = c.Close()
		return fmt.Errorf("mcp: tool discovery failed for %s", c.name)
	}

	c.mu.Lock()
	c.connected = true
	c.tools = tools
	c.lastErr = nil
	c.mu.Unlock()
	return nil
}

// ListTools queries the server for its tool catalog.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("mcp: not started")
	}
	raw, err := c.conn.Call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("mcp: decode tools: %w", err)
	}
	if len(resp.Tools) > 256 {
		return nil, fmt.Errorf("mcp: catalog exceeds 256 tools")
	}
	seen := make(map[string]bool)
	for _, tool := range resp.Tools {
		if tool.Name == "" || len(tool.Name) > 128 || strings.ContainsAny(tool.Name, ":\r\n") || seen[tool.Name] || len(tool.Description) > 8192 || len(tool.InputSchema) > 1024*1024 {
			return nil, fmt.Errorf("mcp: unsupported or oversized tool catalog")
		}
		seen[tool.Name] = true
	}
	return resp.Tools, nil
}

// CallTool invokes a tool by name with the given JSON-encoded args.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	if c.conn == nil {
		return Result{}, fmt.Errorf("mcp: not started")
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if len(args) > maxMCPArgumentBytes {
		return Result{}, fmt.Errorf("mcp: arguments exceed transport byte limit")
	}
	value, err := decodeCallArguments(args)
	if err != nil {
		return Result{}, err
	}
	if _, err := encodedJSONSize(value, maxMCPArgumentBytes); err != nil {
		return Result{}, err
	}
	// Preserve numeric lexemes exactly; decoding into float64 would corrupt large integers.
	argsAny := args

	raw, err := c.conn.Call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": argsAny,
	})
	if err != nil {
		return Result{}, err
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		return Result{}, fmt.Errorf("mcp: decode call result: %w", err)
	}
	for _, ci := range res.Content {
		if ci.Type == "text" {
			if res.Text != "" {
				res.Text += "\n"
			}
			res.Text += ci.Text
		}
	}
	if res.IsError {
		return res, &ToolExecutionError{Server: c.name, Tool: name, Result: res}
	}
	return res, nil
}

// ListResources queries the server's resources catalog. Optional capability;
// returns an empty slice on -32601 (method not found).
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("mcp: not started")
	}
	raw, err := c.conn.Call(ctx, "resources/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Resources []Resource `json:"resources"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("mcp: decode resources: %w", err)
	}
	return resp.Resources, nil
}

// Close kills the subprocess and tears down pipes. Waits for background
// goroutines to drain before returning.
func (c *Client) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.runCancel != nil {
		c.runCancel()
	}
	if c.conn != nil {
		c.conn.close()
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
	if c.stderr != nil {
		_ = c.stderr.Close()
	}
	if c.runDone != nil {
		<-c.runDone
	}
	c.wg.Wait()
	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
	return nil
}

// Connected reports the live status.
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Tools returns the cached tool list (populated at Start).
func (c *Client) Tools() []Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Tool, len(c.tools))
	copy(out, c.tools)
	return out
}

// LastError surfaces the last connection error, if any.
func (c *Client) LastError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastErr
}

func (c *Client) setError(err error) {
	c.mu.Lock()
	c.lastErr = err
	c.connected = false
	c.mu.Unlock()
}
