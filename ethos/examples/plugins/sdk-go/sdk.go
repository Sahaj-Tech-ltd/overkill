// Package sdk provides a thin Go SDK for writing Ethos plugins.
//
// A minimal plugin looks like:
//
//	func main() {
//	    p := sdk.New(sdk.Manifest{
//	        Name: "hello", Version: "0.1.0",
//	    })
//	    p.OnTool("greet", func(ctx context.Context, args json.RawMessage) (any, error) {
//	        return map[string]string{"text": "hi!"}, nil
//	    })
//	    p.RegisterTool(sdk.ToolDecl{Name: "greet", Description: "say hi"})
//	    p.Run()
//	}
//
// The SDK speaks JSON-RPC 2.0 over stdio. It mirrors the wire format used
// by internal/plugin in the host so plugin authors don't need to read the
// host source.
package sdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// Manifest is what the plugin returns from plugin.initialize.
type Manifest struct {
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Description string      `json:"description,omitempty"`
	Permissions Permissions `json:"permissions"`
}

// Permissions is the declared scope of host capabilities the plugin needs.
type Permissions struct {
	ConfigKeys []string `json:"config_keys,omitempty"`
	ToolsCall  []string `json:"tools_call,omitempty"`
	Events     []string `json:"events,omitempty"`
}

// ToolDecl is the schema sent via host.register_tool.
type ToolDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	ArgsSchema  json.RawMessage `json:"args_schema,omitempty"`
	RiskLevel   string          `json:"risk_level,omitempty"`
}

// CommandDecl is the schema sent via host.register_command.
type CommandDecl struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// ContextSnippet is one item returned from a context provider.
type ContextSnippet struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// SessionInfo is what host.session_get returns.
type SessionInfo struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	MessageCount int    `json:"message_count"`
}

// ToolHandler runs a tool invocation. Return either the result value or an
// error; the SDK serializes both.
type ToolHandler func(ctx context.Context, args json.RawMessage) (any, error)

// CommandHandler runs a /command invocation.
type CommandHandler func(ctx context.Context, args string) error

// EventHandler is called when the host fires a subscribed event.
type EventHandler func(payload json.RawMessage)

// ContextHandler is called before each prompt to provide context snippets.
type ContextHandler func(ctx context.Context, promptSoFar, sessionID string) []ContextSnippet

// Plugin is the public type plugin authors interact with.
type Plugin struct {
	manifest Manifest

	tools    []ToolDecl
	cmds     []CommandDecl
	events   []string
	hasCtx   bool

	toolHandlers map[string]ToolHandler
	cmdHandlers  map[string]CommandHandler
	eventHandler EventHandler
	ctxHandler   ContextHandler

	in  io.Reader
	out io.Writer

	writeMu sync.Mutex
	nextID  atomic.Int64
	pending sync.Map // int64 -> chan *rpcMessage
}

// New constructs a Plugin with the given manifest.
func New(m Manifest) *Plugin {
	return &Plugin{
		manifest:     m,
		toolHandlers: make(map[string]ToolHandler),
		cmdHandlers:  make(map[string]CommandHandler),
		in:           os.Stdin,
		out:          os.Stdout,
	}
}

// RegisterTool declares a tool. Pair with OnTool to handle invocations.
func (p *Plugin) RegisterTool(t ToolDecl)   { p.tools = append(p.tools, t) }
func (p *Plugin) OnTool(name string, h ToolHandler) { p.toolHandlers[name] = h }

// RegisterCommand declares a slash-command. Pair with OnCommand.
func (p *Plugin) RegisterCommand(c CommandDecl)         { p.cmds = append(p.cmds, c) }
func (p *Plugin) OnCommand(id string, h CommandHandler) { p.cmdHandlers[id] = h }

// Subscribe declares interest in an event. Use OnEvent to receive them.
func (p *Plugin) Subscribe(event string) { p.events = append(p.events, event) }
func (p *Plugin) OnEvent(h EventHandler) { p.eventHandler = h }

// OnContext registers a context provider.
func (p *Plugin) OnContext(h ContextHandler) {
	p.ctxHandler = h
	p.hasCtx = true
}

// Toast asks the host to display a UI toast.
func (p *Plugin) Toast(ctx context.Context, kind, text string) error {
	_, err := p.call(ctx, "host.toast", map[string]string{"kind": kind, "text": text})
	return err
}

// Session fetches the current session info from the host.
func (p *Plugin) Session(ctx context.Context) (SessionInfo, error) {
	raw, err := p.call(ctx, "host.session_get", map[string]any{})
	if err != nil {
		return SessionInfo{}, err
	}
	var s SessionInfo
	_ = json.Unmarshal(raw, &s)
	return s, nil
}

// Config reads a host config value (must be declared in permissions).
func (p *Plugin) Config(ctx context.Context, key string) (any, error) {
	raw, err := p.call(ctx, "host.config_get", map[string]string{"key": key})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Value any `json:"value"`
	}
	_ = json.Unmarshal(raw, &resp)
	return resp.Value, nil
}

// Run drives the JSON-RPC loop until stdin closes. Blocks.
func (p *Plugin) Run() error {
	r := bufio.NewReaderSize(p.in, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var msg rpcMessage
			if jerr := json.Unmarshal(line, &msg); jerr == nil {
				p.dispatch(&msg)
			}
		}
		if err != nil {
			return err
		}
	}
}

func (p *Plugin) dispatch(msg *rpcMessage) {
	if len(msg.ID) > 0 && (len(msg.Result) > 0 || msg.Error != nil) && msg.Method == "" {
		var idNum int64
		if err := json.Unmarshal(msg.ID, &idNum); err != nil {
			return
		}
		if ch, ok := p.pending.LoadAndDelete(idNum); ok {
			ch.(chan *rpcMessage) <- msg
		}
		return
	}
	if msg.Method == "" {
		return
	}
	go p.handle(msg)
}

func (p *Plugin) handle(msg *rpcMessage) {
	ctx := context.Background()
	switch msg.Method {
	case "plugin.initialize":
		// Auto-register everything declared via RegisterTool/Command/Subscribe.
		for _, t := range p.tools {
			_, _ = p.call(ctx, "host.register_tool", t)
		}
		for _, c := range p.cmds {
			_, _ = p.call(ctx, "host.register_command", c)
		}
		for _, ev := range p.events {
			_, _ = p.call(ctx, "host.subscribe", map[string]string{"event": ev})
		}
		if p.hasCtx {
			_, _ = p.call(ctx, "host.context_provider", map[string]string{"name": p.manifest.Name})
		}
		p.respond(msg, map[string]any{
			"name":        p.manifest.Name,
			"version":     p.manifest.Version,
			"description": p.manifest.Description,
			"manifest":    p.manifest,
		}, nil)
	case "tool.call":
		var params struct {
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		h, ok := p.toolHandlers[params.Name]
		if !ok {
			p.respond(msg, nil, fmt.Errorf("unknown tool %s", params.Name))
			return
		}
		res, err := h(ctx, params.Args)
		p.respond(msg, res, err)
	case "command.invoke":
		var params struct {
			ID   string `json:"id"`
			Args string `json:"args"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		h, ok := p.cmdHandlers[params.ID]
		if !ok {
			p.respond(msg, nil, fmt.Errorf("unknown command %s", params.ID))
			return
		}
		err := h(ctx, params.Args)
		p.respond(msg, map[string]bool{"ok": err == nil}, err)
	case "event.fire":
		if p.eventHandler != nil {
			p.eventHandler(msg.Params)
		}
	case "context.provide":
		if p.ctxHandler == nil {
			p.respond(msg, map[string]any{"snippets": []ContextSnippet{}}, nil)
			return
		}
		var params struct {
			PromptSoFar string `json:"prompt_so_far"`
			SessionID   string `json:"session_id"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		out := p.ctxHandler(ctx, params.PromptSoFar, params.SessionID)
		if out == nil {
			out = []ContextSnippet{}
		}
		p.respond(msg, map[string]any{"snippets": out}, nil)
	case "plugin.shutdown":
		p.respond(msg, map[string]bool{"ok": true}, nil)
		os.Exit(0)
	default:
		p.respond(msg, nil, fmt.Errorf("method not found: %s", msg.Method))
	}
}

func (p *Plugin) respond(msg *rpcMessage, result any, err error) {
	if len(msg.ID) == 0 {
		return // notification, no reply
	}
	resp := &rpcMessage{JSONRPC: "2.0", ID: msg.ID}
	if err != nil {
		resp.Error = &rpcError{Code: -32603, Message: err.Error()}
	} else {
		b, jerr := json.Marshal(result)
		if jerr != nil {
			resp.Error = &rpcError{Code: -32603, Message: jerr.Error()}
		} else {
			resp.Result = b
		}
	}
	_ = p.write(resp)
}

func (p *Plugin) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := p.nextID.Add(1)
	idRaw, _ := json.Marshal(id)
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		paramsRaw = b
	}
	req := rpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method, Params: paramsRaw}
	ch := make(chan *rpcMessage, 1)
	p.pending.Store(id, ch)
	if err := p.write(&req); err != nil {
		p.pending.Delete(id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		p.pending.Delete(id)
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (p *Plugin) write(msg *rpcMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.out.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
