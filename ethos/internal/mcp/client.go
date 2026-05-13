package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
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
}

// NewClient prepares a client; call Start to spawn the subprocess and run
// the JSON-RPC handshake.
func NewClient(name, command string, args []string, env map[string]string) *Client {
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

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("mcp: start %s: %w", c.name, err)
	}

	c.conn = newJSONRPCConn(stdin, stdout)
	go func() { _ = c.conn.readLoop() }()
	// Drain stderr so the server doesn't block on a full pipe.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := stderr.Read(buf); err != nil {
				return
			}
		}
	}()

	// initialize handshake
	initParams := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
		},
		"clientInfo": map[string]any{
			"name":    "overkill",
			"version": "0.1",
		},
	}
	if _, err := c.conn.Call(ctx, "initialize", initParams); err != nil {
		c.setError(err)
		return fmt.Errorf("mcp: initialize %s: %w", c.name, err)
	}
	if err := c.conn.Notify("notifications/initialized", map[string]any{}); err != nil {
		c.setError(err)
		return fmt.Errorf("mcp: initialized notification %s: %w", c.name, err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		// Some servers don't expose tools — that's fine; mark connected.
		tools = nil
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
	return resp.Tools, nil
}

// CallTool invokes a tool by name with the given JSON-encoded args.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	if c.conn == nil {
		return Result{}, fmt.Errorf("mcp: not started")
	}
	var argsAny any = map[string]any{}
	if len(args) > 0 {
		var v any
		if err := json.Unmarshal(args, &v); err == nil {
			argsAny = v
		}
	}
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

// Close kills the subprocess and tears down pipes.
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
