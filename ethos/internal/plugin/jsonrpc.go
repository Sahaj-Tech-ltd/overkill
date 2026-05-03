// Package plugin implements a subprocess-based plugin runtime for Ethos.
// Plugins are external executables that speak JSON-RPC 2.0 over stdio.
//
// Wire format mirrors internal/mcp: newline-delimited JSON, requests carry
// numeric ids, notifications carry no id, and responses echo the request id.
// Unlike MCP, the host both calls into the plugin (tool.call, event.fire,
// etc.) and accepts inbound calls from the plugin (host.register_tool,
// host.toast, host.config_get, etc.).
package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Standard JSON-RPC 2.0 error codes plus a few we use for permission and
// internal failures. Codes -32000 to -32099 are reserved for server-defined
// implementation-specific errors per the JSON-RPC spec.
const (
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternal       = -32603

	// ErrCodePermissionDenied is returned when a plugin tries to read a
	// config key, call a tool, or subscribe to an event it didn't declare
	// in its manifest.
	ErrCodePermissionDenied = -32001
	// ErrCodeUnknownPlugin is returned for routing failures.
	ErrCodeUnknownPlugin = -32002
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError mirrors the JSON-RPC 2.0 error object. Exported so call sites
// can inspect the code (e.g. permission denial vs transport failure).
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Handler executes an inbound RPC call. The returned value will be marshaled
// as the `result` field. To return a JSON-RPC error, return a *RPCError.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Conn is a bidirectional JSON-RPC 2.0 framer over a stdio pair.
type Conn struct {
	w        io.Writer
	r        *bufio.Reader
	writeMu  sync.Mutex
	nextID   atomic.Int64
	pending  sync.Map // int64 -> chan *rpcMessage
	handlers sync.Map // string -> Handler
	closed   atomic.Bool
}

// NewConn wraps an existing stdio pair. Call Serve in a goroutine to start
// processing inbound messages.
func NewConn(w io.Writer, r io.Reader) *Conn {
	return &Conn{
		w: w,
		r: bufio.NewReaderSize(r, 64*1024),
	}
}

// Handle registers an inbound RPC handler. Handlers run in their own
// goroutine so they can issue further calls without deadlocking the read
// loop.
func (c *Conn) Handle(method string, h Handler) {
	c.handlers.Store(method, h)
}

// Serve runs the read loop. Returns when the underlying reader yields an
// error (typically io.EOF when the peer exits).
func (c *Conn) Serve(ctx context.Context) error {
	for {
		line, err := c.r.ReadBytes('\n')
		if len(line) > 0 {
			var msg rpcMessage
			if jerr := json.Unmarshal(line, &msg); jerr == nil {
				c.dispatch(ctx, &msg)
			} else {
				// Best-effort parse error response — only if the line had
				// any id we could echo back.
				_ = c.writeMessage(&rpcMessage{
					JSONRPC: "2.0",
					Error:   &RPCError{Code: ErrCodeParse, Message: jerr.Error()},
				})
			}
		}
		if err != nil {
			c.closed.Store(true)
			c.failAllPending(err)
			return err
		}
	}
}

func (c *Conn) dispatch(ctx context.Context, msg *rpcMessage) {
	// Response to a call we made.
	if len(msg.ID) > 0 && (len(msg.Result) > 0 || msg.Error != nil) && msg.Method == "" {
		var idNum int64
		if err := json.Unmarshal(msg.ID, &idNum); err != nil {
			return
		}
		if ch, ok := c.pending.LoadAndDelete(idNum); ok {
			ch.(chan *rpcMessage) <- msg
		}
		return
	}
	// Inbound request or notification.
	if msg.Method == "" {
		return
	}
	hAny, ok := c.handlers.Load(msg.Method)
	if !ok {
		if len(msg.ID) > 0 {
			_ = c.writeMessage(&rpcMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error: &RPCError{
					Code:    ErrCodeMethodNotFound,
					Message: "method not found: " + msg.Method,
				},
			})
		}
		return
	}
	h := hAny.(Handler)
	go func() {
		result, err := h(ctx, msg.Params)
		if len(msg.ID) == 0 {
			// Notification: no response, regardless of outcome.
			return
		}
		resp := &rpcMessage{JSONRPC: "2.0", ID: msg.ID}
		if err != nil {
			if rerr, ok := err.(*RPCError); ok {
				resp.Error = rerr
			} else {
				resp.Error = &RPCError{Code: ErrCodeInternal, Message: err.Error()}
			}
		} else {
			b, jerr := json.Marshal(result)
			if jerr != nil {
				resp.Error = &RPCError{Code: ErrCodeInternal, Message: jerr.Error()}
			} else {
				resp.Result = b
			}
		}
		_ = c.writeMessage(resp)
	}()
}

func (c *Conn) failAllPending(err error) {
	c.pending.Range(func(k, v any) bool {
		ch := v.(chan *rpcMessage)
		select {
		case ch <- &rpcMessage{Error: &RPCError{Code: ErrCodeInternal, Message: err.Error()}}:
		default:
		}
		c.pending.Delete(k)
		return true
	})
}

// Call sends a request and waits for the response (or ctx cancellation).
func (c *Conn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("plugin: connection closed")
	}
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("plugin: marshal params: %w", err)
		}
		paramsRaw = b
	}
	req := rpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method, Params: paramsRaw}
	ch := make(chan *rpcMessage, 1)
	c.pending.Store(id, ch)

	if err := c.writeMessage(&req); err != nil {
		c.pending.Delete(id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.pending.Delete(id)
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// Notify sends a fire-and-forget notification.
func (c *Conn) Notify(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("plugin: marshal params: %w", err)
		}
		paramsRaw = b
	}
	return c.writeMessage(&rpcMessage{JSONRPC: "2.0", Method: method, Params: paramsRaw})
}

func (c *Conn) writeMessage(msg *rpcMessage) error {
	if c.closed.Load() {
		return fmt.Errorf("plugin: connection closed")
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("plugin: marshal: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("plugin: write: %w", err)
	}
	return nil
}

// Close marks the connection as closed; the next read or write fails.
func (c *Conn) Close() { c.closed.Store(true) }
