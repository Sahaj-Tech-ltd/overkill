// Package lsp implements a minimal Language Server Protocol client over stdio.
// LSP uses JSON-RPC 2.0 with Content-Length framed messages.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

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
	return fmt.Sprintf("lsp error %d: %s", e.Code, e.Message)
}

type jsonrpcConn struct {
	w       io.Writer
	r       *bufio.Reader
	writeMu sync.Mutex

	nextID  atomic.Int64
	pending sync.Map // int64 -> chan *jsonrpcMessage

	closed atomic.Bool
	// maxMessageBytes caps Content-Length. Zero means use default (32 MiB).
	maxMessageBytes int
}

func newJSONRPCConn(w io.Writer, r io.Reader, maxMessageBytes int) *jsonrpcConn {
	return &jsonrpcConn{w: w, r: bufio.NewReaderSize(r, 64*1024), maxMessageBytes: maxMessageBytes}
}

// readLoop reads Content-Length framed messages.
func (c *jsonrpcConn) readLoop() error {
	for {
		var contentLength int
		// Read headers
		for {
			line, err := c.r.ReadString('\n')
			if err != nil {
				c.closed.Store(true)
				c.failAllPending(err)
				return err
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				v := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
				n, err := strconv.Atoi(v)
				if err == nil {
					contentLength = n
				}
			}
		}
		// Cap message size. A rogue or buggy language server sending
		// `Content-Length: 2147483647` would otherwise trigger a 2 GB
		// allocation and OOM the agent. 32 MB is comfortably above
		// real LSP traffic (the largest legitimate payloads are
		// completion/symbol responses around a few MB).
		cap := c.maxMessageBytes
		if cap <= 0 {
			cap = 32 * 1024 * 1024
		}
		if contentLength <= 0 || contentLength > cap {
			if contentLength > cap {
				c.closed.Store(true)
				err := fmt.Errorf("lsp: Content-Length %d exceeds %d byte cap", contentLength, cap)
				c.failAllPending(err)
				return err
			}
			continue
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(c.r, body); err != nil {
			c.closed.Store(true)
			c.failAllPending(err)
			return err
		}
		var msg jsonrpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		c.dispatch(&msg)
	}
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
	}
	// We ignore server-initiated requests (window/showMessage etc.) for now.
}

func (c *jsonrpcConn) failAllPending(err error) {
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

func (c *jsonrpcConn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("lsp: connection closed")
	}
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		paramsRaw = b
	}
	req := jsonrpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method, Params: paramsRaw}
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

func (c *jsonrpcConn) Notify(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		paramsRaw = b
	}
	msg := jsonrpcMessage{JSONRPC: "2.0", Method: method, Params: paramsRaw}
	return c.writeMessage(&msg)
}

func (c *jsonrpcConn) writeMessage(msg *jsonrpcMessage) error {
	if c.closed.Load() {
		return fmt.Errorf("lsp: connection closed")
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.w.Write([]byte(header)); err != nil {
		return err
	}
	if _, err := c.w.Write(body); err != nil {
		return err
	}
	return nil
}

func (c *jsonrpcConn) close() { c.closed.Store(true) }
