// Package mcp implements a Go client for the Model Context Protocol.
// MCP servers are subprocesses that speak JSON-RPC 2.0 over stdio.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// maxMCPLineBytes caps a single JSON-RPC message at 10MB — prevents
// OOM from a rogue MCP server sending a multi-GB response. 10 MiB is
// generous enough for large tool results (e.g. a full file read) while
// still bounding memory usage.
const maxMCPLineBytes = 10 * 1024 * 1024

// jsonrpcMessage is the wire envelope. Either Method+ID is set (request),
// Method only (notification), or ID + Result/Error (response).
type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *jsonrpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// jsonrpcConn is a minimal JSON-RPC 2.0 framer over a stdio pair using
// newline-delimited JSON (which the MCP stdio transport uses).
type jsonrpcConn struct {
	w       io.Writer
	r       *bufio.Reader
	writeMu sync.Mutex

	nextID  atomic.Int64
	pending sync.Map // id (int64) -> chan *jsonrpcMessage

	notifyMu sync.Mutex
	notify   func(method string, params json.RawMessage)

	closed atomic.Bool
}

func newJSONRPCConn(w io.Writer, r io.Reader) *jsonrpcConn {
	return &jsonrpcConn{
		w: w,
		r: bufio.NewReaderSize(r, 64*1024),
	}
}

func (c *jsonrpcConn) setNotifyHandler(fn func(string, json.RawMessage)) {
	c.notifyMu.Lock()
	c.notify = fn
	c.notifyMu.Unlock()
}

// readLoop should be run in a goroutine. It dispatches responses to pending
// callers and notifications to the registered handler. Lines are capped at
// maxMCPLineBytes to guard against OOM from a rogue server.
func (c *jsonrpcConn) readLoop() error {
	scanner := bufio.NewScanner(c.r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMCPLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg jsonrpcMessage
		if err := json.Unmarshal(line, &msg); err == nil {
			c.dispatch(&msg)
		}
	}
	c.closed.Store(true)
	err := scanner.Err()
	c.failAllPending(err)
	return err
}

func (c *jsonrpcConn) dispatch(msg *jsonrpcMessage) {
	if len(msg.ID) > 0 && (len(msg.Result) > 0 || msg.Error != nil) {
		var idNum int64
		if err := json.Unmarshal(msg.ID, &idNum); err != nil {
			return
		}
		if ch, ok := c.pending.LoadAndDelete(idNum); ok {
			ch.(chan *jsonrpcMessage) <- msg
		}
		return
	}
	if msg.Method != "" {
		c.notifyMu.Lock()
		fn := c.notify
		c.notifyMu.Unlock()
		if fn != nil {
			fn(msg.Method, msg.Params)
		}
	}
}

func (c *jsonrpcConn) failAllPending(err error) {
	if err == nil {
		err = fmt.Errorf("connection closed")
	}
	c.pending.Range(func(k, v any) bool {
		ch := v.(chan *jsonrpcMessage)
		select {
		case ch <- &jsonrpcMessage{Error: &jsonrpcError{Code: -32000, Message: err.Error()}}:
		default:
		}
		c.pending.Delete(k)
		return true
	})
}

// Call sends a request and waits for its response (or ctx cancel).
func (c *jsonrpcConn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("jsonrpc: connection closed")
	}
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)

	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("jsonrpc: marshal params: %w", err)
		}
		paramsRaw = b
	}

	req := jsonrpcMessage{
		JSONRPC: "2.0",
		ID:      idRaw,
		Method:  method,
		Params:  paramsRaw,
	}
	ch := make(chan *jsonrpcMessage, 1)
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

// Notify sends a notification (no response expected).
func (c *jsonrpcConn) Notify(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("jsonrpc: marshal params: %w", err)
		}
		paramsRaw = b
	}
	msg := jsonrpcMessage{JSONRPC: "2.0", Method: method, Params: paramsRaw}
	return c.writeMessage(&msg)
}

func (c *jsonrpcConn) writeMessage(msg *jsonrpcMessage) error {
	if c.closed.Load() {
		return fmt.Errorf("jsonrpc: connection closed")
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("jsonrpc: marshal: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("jsonrpc: write: %w", err)
	}
	return nil
}

func (c *jsonrpcConn) close() {
	c.closed.Store(true)
}
