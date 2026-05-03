package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// HostVersion is what the host advertises during plugin.initialize. Plugins
// can use it for back-compat decisions.
const HostVersion = "0.1.0"

// Client is one running plugin: a subprocess plus the JSON-RPC connection
// over its stdio. The client owns the manifest and the sets of tools,
// commands, and event subscriptions the plugin registered.
type Client struct {
	name string
	cmd  *exec.Cmd
	conn *Conn

	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	bridge HostBridge

	mu               sync.RWMutex
	connected        bool
	manifest         Manifest
	tools            map[string]ToolDecl
	commands         map[string]CommandDecl
	events           map[string]struct{}
	hasContext       bool
	lastErr          error
	disabled         bool

	// staticManifest, when set (from plugin.toml), is compared against the
	// manifest the plugin returns from initialize; mismatches mark the
	// client unhealthy.
	staticManifest *Manifest
}

// NewClient prepares a client; call Start to spawn it.
func NewClient(name string, command string, args []string, env map[string]string, bridge HostBridge) *Client {
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		envv := os.Environ()
		for k, v := range env {
			envv = append(envv, k+"="+v)
		}
		cmd.Env = envv
	}
	return &Client{
		name:     name,
		cmd:      cmd,
		bridge:   bridge,
		tools:    make(map[string]ToolDecl),
		commands: make(map[string]CommandDecl),
		events:   make(map[string]struct{}),
	}
}

// Name returns the plugin name from discovery (may differ from the manifest
// name; if so, the manifest name wins after initialize).
func (c *Client) Name() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.manifest.Name != "" {
		return c.manifest.Name
	}
	return c.name
}

// SetStaticManifest is called by discovery when a plugin.toml was loaded.
func (c *Client) SetStaticManifest(m Manifest) { c.staticManifest = &m }

// Start spawns the process, runs the initialize handshake, and registers
// the inbound host.* handlers.
func (c *Client) Start(ctx context.Context) error {
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("plugin: stdin: %w", err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("plugin: stdout: %w", err)
	}
	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("plugin: stderr: %w", err)
	}
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("plugin: start %s: %w", c.name, err)
	}
	c.conn = NewConn(stdin, stdout)
	// Seed permissions from the static plugin.toml manifest, if any, so
	// registrations the plugin issues during the initialize window are
	// checked against the user-installed manifest. The dynamic manifest
	// from plugin.initialize replaces this once it lands.
	if c.staticManifest != nil {
		c.mu.Lock()
		c.manifest = *c.staticManifest
		c.mu.Unlock()
	}
	c.installHostHandlers()
	go func() { _ = c.conn.Serve(ctx) }()

	// Drain stderr so a chatty plugin doesn't block on a full pipe.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := stderr.Read(buf); err != nil {
				return
			}
		}
	}()

	initParams := map[string]any{
		"host_version": HostVersion,
		"capabilities": map[string]any{
			"tools":             true,
			"commands":          true,
			"events":            true,
			"context_provider":  true,
		},
	}
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := c.conn.Call(initCtx, "plugin.initialize", initParams)
	if err != nil {
		c.setError(err)
		return fmt.Errorf("plugin: initialize %s: %w", c.name, err)
	}
	var initResp struct {
		Name        string   `json:"name"`
		Version     string   `json:"version"`
		Description string   `json:"description"`
		Manifest    Manifest `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &initResp); err != nil {
		c.setError(err)
		return fmt.Errorf("plugin: decode initialize %s: %w", c.name, err)
	}
	manifest := initResp.Manifest
	if manifest.Name == "" {
		manifest.Name = initResp.Name
	}
	if manifest.Version == "" {
		manifest.Version = initResp.Version
	}
	if manifest.Description == "" {
		manifest.Description = initResp.Description
	}
	if err := ValidateManifest(manifest); err != nil {
		c.setError(err)
		return err
	}
	if c.staticManifest != nil {
		if c.staticManifest.Name != "" && c.staticManifest.Name != manifest.Name {
			err := fmt.Errorf("plugin: manifest name mismatch (toml=%s init=%s)", c.staticManifest.Name, manifest.Name)
			c.setError(err)
			return err
		}
	}
	c.mu.Lock()
	c.manifest = manifest
	c.connected = true
	c.lastErr = nil
	c.mu.Unlock()
	return nil
}

// installHostHandlers wires the host.* methods plugins are allowed to call.
func (c *Client) installHostHandlers() {
	c.conn.Handle("host.register_tool", func(_ context.Context, params json.RawMessage) (any, error) {
		var t ToolDecl
		if err := json.Unmarshal(params, &t); err != nil {
			return nil, &RPCError{Code: ErrCodeInvalidParams, Message: err.Error()}
		}
		if t.Name == "" {
			return nil, &RPCError{Code: ErrCodeInvalidParams, Message: "tool name required"}
		}
		c.mu.Lock()
		c.tools[t.Name] = t
		c.mu.Unlock()
		return map[string]bool{"ok": true}, nil
	})

	c.conn.Handle("host.register_command", func(_ context.Context, params json.RawMessage) (any, error) {
		var d CommandDecl
		if err := json.Unmarshal(params, &d); err != nil {
			return nil, &RPCError{Code: ErrCodeInvalidParams, Message: err.Error()}
		}
		if d.ID == "" {
			return nil, &RPCError{Code: ErrCodeInvalidParams, Message: "command id required"}
		}
		c.mu.Lock()
		c.commands[d.ID] = d
		c.mu.Unlock()
		return map[string]bool{"ok": true}, nil
	})

	c.conn.Handle("host.subscribe", func(_ context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &RPCError{Code: ErrCodeInvalidParams, Message: err.Error()}
		}
		c.mu.RLock()
		allowed := c.manifest.Permissions.AllowsEvent(p.Event)
		c.mu.RUnlock()
		if !allowed {
			return nil, permissionError("event " + p.Event + " not declared in manifest.permissions.events")
		}
		c.mu.Lock()
		c.events[p.Event] = struct{}{}
		c.mu.Unlock()
		return map[string]bool{"ok": true}, nil
	})

	c.conn.Handle("host.context_provider", func(_ context.Context, params json.RawMessage) (any, error) {
		c.mu.Lock()
		c.hasContext = true
		c.mu.Unlock()
		return map[string]bool{"ok": true}, nil
	})

	c.conn.Handle("host.toast", func(_ context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &RPCError{Code: ErrCodeInvalidParams, Message: err.Error()}
		}
		if c.bridge != nil {
			c.bridge.Toast(p.Kind, p.Text)
		}
		return map[string]bool{"ok": true}, nil
	})

	c.conn.Handle("host.session_get", func(_ context.Context, _ json.RawMessage) (any, error) {
		if c.bridge == nil {
			return SessionInfo{}, nil
		}
		return c.bridge.SessionInfo(), nil
	})

	c.conn.Handle("host.config_get", func(_ context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &RPCError{Code: ErrCodeInvalidParams, Message: err.Error()}
		}
		c.mu.RLock()
		allowed := c.manifest.Permissions.AllowsConfigKey(p.Key)
		c.mu.RUnlock()
		if !allowed {
			return nil, permissionError("config key " + p.Key + " not declared in manifest.permissions.config_keys")
		}
		if c.bridge == nil {
			return map[string]any{"value": nil}, nil
		}
		v, _ := c.bridge.ConfigValue(p.Key)
		return map[string]any{"value": v}, nil
	})
}

// CallTool invokes a tool registered by this plugin.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("plugin: not started")
	}
	var argsAny any = map[string]any{}
	if len(args) > 0 {
		var v any
		if err := json.Unmarshal(args, &v); err == nil {
			argsAny = v
		}
	}
	return c.conn.Call(ctx, "tool.call", map[string]any{
		"name": name,
		"args": argsAny,
	})
}

// InvokeCommand fires a slash-command invocation at the plugin.
func (c *Client) InvokeCommand(ctx context.Context, id, args string) error {
	if c.conn == nil {
		return fmt.Errorf("plugin: not started")
	}
	_, err := c.conn.Call(ctx, "command.invoke", map[string]any{
		"id":   id,
		"args": args,
	})
	return err
}

// FireEvent delivers a notification for a subscribed event.
func (c *Client) FireEvent(event string, payload any) error {
	if c.conn == nil {
		return fmt.Errorf("plugin: not started")
	}
	c.mu.RLock()
	_, ok := c.events[event]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	return c.conn.Notify("event.fire", map[string]any{
		"event":   event,
		"payload": payload,
	})
}

// Provide asks the plugin's context provider for snippets. Returns nil when
// the plugin didn't register a context provider or if the call times out.
func (c *Client) Provide(ctx context.Context, prompt, sessionID string) ([]ContextSnippet, error) {
	c.mu.RLock()
	ok := c.hasContext
	c.mu.RUnlock()
	if !ok || c.conn == nil {
		return nil, nil
	}
	raw, err := c.conn.Call(ctx, "context.provide", map[string]any{
		"prompt_so_far": prompt,
		"session_id":    sessionID,
	})
	if err != nil {
		return nil, err
	}
	var resp ContextResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return resp.Snippets, nil
}

// Shutdown sends plugin.shutdown then SIGTERMs the process with a grace
// period before SIGKILL.
func (c *Client) Shutdown(ctx context.Context) error {
	if c.conn != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, _ = c.conn.Call(shutdownCtx, "plugin.shutdown", map[string]any{})
		cancel()
		c.conn.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- c.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = c.cmd.Process.Kill()
			<-done
		}
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
	return nil
}

// Connected reports whether the plugin completed initialize and hasn't died.
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Manifest returns the post-initialize manifest.
func (c *Client) Manifest() Manifest {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.manifest
}

// Tools returns a snapshot of registered tools.
func (c *Client) Tools() []ToolDecl {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ToolDecl, 0, len(c.tools))
	for _, t := range c.tools {
		out = append(out, t)
	}
	return out
}

// Commands returns a snapshot of registered commands.
func (c *Client) Commands() []CommandDecl {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]CommandDecl, 0, len(c.commands))
	for _, d := range c.commands {
		out = append(out, d)
	}
	return out
}

// SubscribedEvents returns the current subscription set.
func (c *Client) SubscribedEvents() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.events))
	for ev := range c.events {
		out = append(out, ev)
	}
	return out
}

// HasContextProvider reports whether the plugin opted into context provision.
func (c *Client) HasContextProvider() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hasContext
}

// LastError surfaces the last failure for the /plugins dialog.
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

// Wait blocks until the underlying process exits, returning its error (or
// nil for a clean exit).
func (c *Client) Wait() error {
	if c.cmd == nil {
		return nil
	}
	return c.cmd.Wait()
}
