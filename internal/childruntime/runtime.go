// Package childruntime builds isolated children using the application's agent
// loop. It lives above agent, tools, and subagent to avoid dependency cycles.
package childruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/instructions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/google/uuid"
)

// Resolver must resolve exactly the requested configured provider/model. It
// must not silently select another provider after a credentials failure.
type Resolver func(provider, model string) (providers.Provider, string, error)

type Factory struct {
	Scanners        func() []security.Scanner
	Messages        *subagent.CommunicationStore
	Terminals       *terminal.Manager
	TerminalSession string
	// CodingTools is a trusted application grant. Nil keeps legacy fs/patch.
	// It must return fresh adapters and cannot be supplied through model input.
	CodingTools    func() []tools.Tool
	ExecutionGuard tools.ExecutionGuard
	Autonomy       string
	Instructions   instructions.Config
	Workspace      string
	Resolve        Resolver
	Skills         *skills.Registry
	ReadOnly       bool
	AuditReadOnly  bool
	Personality    personality.Config
}

// Build constructs a fresh provider, history, registry, and receipts per child.
// The application grants configured coding capabilities. Unsupported tools
// fail before any provider request or filesystem mutation takes place.
func (f Factory) Build(cfg subagent.RealWorkerConfig) (subagent.WorkerRunner, error) {
	if f.Workspace == "" {
		return nil, fmt.Errorf("child runtime: workspace is required")
	}
	if f.Resolve == nil {
		return nil, fmt.Errorf("child runtime: provider resolver is not configured")
	}
	workspace, err := filepath.Abs(f.Workspace)
	if err != nil {
		return nil, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, fmt.Errorf("child runtime: workspace: %w", err)
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("child runtime: workspace must be a directory")
	}
	instructionSource, err := instructions.New(workspace, f.Instructions)
	if err != nil {
		return nil, err
	}
	available := map[string]tools.Tool{"fs": tools.NewFSTool(workspace), "patch": tools.NewPatchTool(workspace)}
	if f.CodingTools != nil {
		available = make(map[string]tools.Tool)
		for _, tool := range f.CodingTools() {
			if tool != nil {
				available[tool.Name()] = tool
			}
		}
	}
	if f.AuditReadOnly {
		available = map[string]tools.Tool{"fs": &auditReadTool{Tool: tools.NewFSTool(workspace)}, "audit_search": &auditInspectTool{name: "audit_search", workspace: workspace}, "audit_git": &auditInspectTool{name: "audit_git", workspace: workspace}}
	}
	workerID := cfg.WorkerID
	if workerID == "" {
		workerID = "child-" + uuid.NewString()
	}
	owner := terminal.Owner{Session: f.TerminalSession, Worker: workerID}
	if f.Terminals != nil && terminal.Supported() && owner.Session != "" && !f.AuditReadOnly {
		available["terminal"] = tools.NewTerminalTool(f.Terminals, owner, workspace)
	}
	if f.Messages != nil && !f.AuditReadOnly {
		available["worker_messages"] = tools.NewWorkerMessageTool(f.Messages, workerID)
	}
	selected := make(map[string]bool, len(cfg.Tools))
	selectedTools := make(map[string]tools.Tool, len(cfg.Tools))
	for _, name := range cfg.Tools {
		tool, ok := available[name]
		if !ok {
			return nil, fmt.Errorf("%w: child runtime tool %q is not granted by this session", subagent.ErrUnsupportedCapability, name)
		}
		selected[name] = true
		selectedTools[name] = tool
	}
	provider, model, err := f.Resolve(cfg.Provider, cfg.Model)
	if err != nil {
		return nil, err
	}
	if provider == nil || model == "" {
		return nil, fmt.Errorf("child runtime: provider and model must be configured")
	}
	if len(selected) > 0 {
		for _, candidate := range provider.Models() {
			if candidate.ID == model && !candidate.SupportsTools {
				return nil, fmt.Errorf("%w: model %q does not support tools", subagent.ErrUnsupportedCapability, model)
			}
		}
	}
	cfg.Model = model
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 15
	}
	if _, _, err := providers.ResolveTokenLimits(provider.Models(), model, cfg.MaxTokens); err != nil {
		return nil, fmt.Errorf("child runtime: token limits: %w", err)
	}
	if f.Messages != nil && !f.AuditReadOnly {
		modelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := f.Messages.SetModel(modelCtx, workerID, model)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	return &runner{scanners: f.Scanners, messages: f.Messages, id: workerID, terminals: f.Terminals, terminalOwner: owner, tools: selectedTools, guard: f.ExecutionGuard, autonomy: f.Autonomy, instructionSource: instructionSource, cfg: cfg, workspace: workspace, provider: provider, selected: selected, skills: f.Skills, auditReadOnly: f.AuditReadOnly, readOnly: f.ReadOnly, personality: f.Personality}, nil
}

type runner struct {
	scanners          func() []security.Scanner
	messages          *subagent.CommunicationStore
	id                string
	terminals         *terminal.Manager
	terminalOwner     terminal.Owner
	tools             map[string]tools.Tool
	guard             tools.ExecutionGuard
	autonomy          string
	instructionSource *instructions.Source
	cfg               subagent.RealWorkerConfig
	workspace         string
	provider          providers.Provider
	selected          map[string]bool
	skills            *skills.Registry
	auditReadOnly     bool
	readOnly          bool
	personality       personality.Config
}

func (r *runner) Run(ctx context.Context) (*subagent.Result, error) {
	start := time.Now()
	if r.terminals != nil {
		defer r.terminals.CloseOwner(r.terminalOwner)
	}
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}
	state := &runState{read: map[string]bool{}, written: map[string]bool{}}
	registry := tools.NewRegistry()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := registry.Register(&recordingTool{Tool: r.tools[name], guard: r.guard, workspace: r.workspace, state: state}); err != nil {
			return nil, err
		}
	}
	metered := &meteredProvider{Provider: r.provider, state: state, maxSteps: r.cfg.MaxSteps, allowed: r.selected}
	limits := providers.TokenLimitsFor(r.provider.Models(), r.cfg.MaxTokens)
	contextWindow, outputTokens, err := limits(r.cfg.Model)
	if err != nil {
		return nil, err
	}
	childSystemPrompt := personality.WithPromptStyle("Complete the delegated task using only the tools provided and the supplied context. All filesystem paths are relative to your workspace. Do not claim a filesystem change unless a successful tool invocation made it. Report unsupported requirements explicitly.", r.personality.Level, r.personality.Language)
	var inbox agent.BoundaryInbox
	if r.messages != nil {
		inbox = WorkerInbox(r.messages, r.id)
	}
	var scanners []security.Scanner
	if r.scanners != nil {
		scanners = r.scanners()
	}
	child := agent.New(agent.Config{Scanners: scanners, BoundaryInbox: inbox, InstructionSource: r.instructionSource, ContextWindow: contextWindow, TokenLimits: limits, Tokenizer: tokenizer.NewEstimator(), Provider: metered, Tools: registry, Model: r.cfg.Model, MaxTokens: outputTokens, MaxSteps: r.cfg.MaxSteps, SessionID: r.id, DisableAtMentions: true, SystemPrompt: childSystemPrompt})
	if r.readOnly {
		child.SetAutoMode("readonly")
	} else if r.autonomy != "" {
		child.SetAutoMode(r.autonomy)
	}
	child.SetSkillRegistry(r.skills)
	defer child.Shutdown()
	output, runErr := child.Run(ctx, "Delegated task:\n"+r.cfg.Goal+"\n\nSupplied context:\n"+r.cfg.Context)
	if r.terminals != nil {
		runErr = errors.Join(runErr, r.terminals.CloseOwner(r.terminalOwner))
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	result := &subagent.Result{TerminalHandoffs: append([]terminal.Handoff(nil), state.handoffs...), TaskIndex: r.cfg.TaskIndex, Status: "completed", ExitReason: "completed", TokensIn: state.in, TokensOut: state.out, CostUSD: state.cost, DurationMs: time.Since(start).Milliseconds(), ToolTrace: append([]subagent.ToolEntry(nil), state.trace...)}
	for path := range state.read {
		result.FilesRead = append(result.FilesRead, path)
	}
	sort.Strings(result.FilesRead)
	for path := range state.written {
		result.FilesWritten = append(result.FilesWritten, path)
	}
	sort.Strings(result.FilesWritten)
	if runErr == nil && state.failure != nil {
		runErr = state.failure
	}
	if output != nil {
		result.Summary = output.Response
		if output.Blocked && runErr == nil {
			runErr = fmt.Errorf("child runtime: blocked: %s", output.BlockReason)
		}
	}
	if len(r.selected) > 0 && len(state.trace) == 0 && runErr == nil {
		runErr = fmt.Errorf("child runtime: task finished without executing any requested tools; workspace work was not verified")
	}
	if output == nil && runErr == nil {
		runErr = fmt.Errorf("child runtime: agent returned no output")
	}
	if runErr != nil {
		result.Status = "error"
		result.ExitReason = "worker_error"
		result.Error = runErr.Error()
		result.Summary = "Task failed: " + runErr.Error()
		if errors.Is(runErr, context.Canceled) {
			result.Status = "interrupted"
			result.ExitReason = "interrupted"
		}
		if errors.Is(runErr, context.DeadlineExceeded) {
			result.Status = "timeout"
			result.ExitReason = "timeout"
		}
	}
	return result, runErr
}

type runState struct {
	handoffs      []terminal.Handoff
	mu            sync.Mutex
	in, out       int64
	cost          float64
	trace         []subagent.ToolEntry
	read, written map[string]bool
	failure       error
	calls         int
}

func (s *runState) fail(err error) {
	if s.failure == nil {
		s.failure = err
	}
}

type meteredProvider struct {
	providers.Provider
	state    *runState
	maxSteps int
	allowed  map[string]bool
}

func (p *meteredProvider) Complete(ctx context.Context, req providers.Request) (providers.Response, error) {
	p.state.mu.Lock()
	if p.state.calls >= p.maxSteps {
		p.state.mu.Unlock()
		return providers.Response{}, fmt.Errorf("child runtime: provider-call budget exhausted (%d)", p.maxSteps)
	}
	p.state.calls++
	p.state.mu.Unlock()
	response, err := p.Provider.Complete(ctx, req)
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.in += int64(response.Usage.InputTokens)
	p.state.out += int64(response.Usage.OutputTokens)
	model := response.Model
	if model == "" {
		model = req.Model
	}
	for _, priced := range p.Provider.Models() {
		if priced.ID == model {
			p.state.cost += cost.CalculateCost(response.Usage, priced)
			break
		}
	}
	for _, call := range response.ToolCalls {
		if !p.allowed[call.Name] {
			p.state.fail(fmt.Errorf("child runtime: provider requested unsupported tool %q", call.Name))
		}
	}
	return response, err
}

// Child execution uses the non-streaming Agent.Run path. Refuse accidental
// streaming so usage/call-limit accounting cannot be bypassed by a future caller.
func (p *meteredProvider) Stream(context.Context, providers.Request) (<-chan providers.Chunk, error) {
	return nil, fmt.Errorf("child runtime: streaming is not supported")
}

type recordingTool struct {
	tools.Tool
	guard     tools.ExecutionGuard
	workspace string
	state     *runState
}

// These optional metadata methods are consumed by Agent.buildToolDefs, so
// provider requests and budget estimation see identical tool schemas.
func (t *recordingTool) Description() string {
	if metadata, ok := t.Tool.(interface{ Description() string }); ok {
		return metadata.Description()
	}
	return toolDefinition(t.Name()).Description
}
func (t *recordingTool) Schema() json.RawMessage {
	if metadata, ok := t.Tool.(interface{ Schema() json.RawMessage }); ok {
		return metadata.Schema()
	}
	return toolDefinition(t.Name()).Parameters
}

func (t *recordingTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var output json.RawMessage
	var err error
	// Native language servers may open absolute files. Bind delegated queries
	// to canonical workspace bytes before reaching the shared parent service.
	if strings.HasPrefix(t.Name(), "lsp_") {
		input, err = scopedLSPInput(t.workspace, input)
	}
	if err != nil { // recorded below; do not invoke the adapter
	} else if t.guard != nil {
		output, err = t.guard(ctx, t.Tool, input)
	} else {
		output, err = t.Tool.Execute(ctx, input)
	}
	var args struct{ Action, Path string }
	_ = json.Unmarshal(input, &args)
	path := args.Path
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(t.workspace, path)
	}
	if path != "" {
		path = filepath.Clean(path)
	}
	entry := subagent.ToolEntry{Tool: t.Name(), ArgsLen: len(input), Path: path, InputHash: digest(input), OutputHash: digest(output)}
	t.state.mu.Lock()
	defer t.state.mu.Unlock()
	if t.Name() == "terminal" && err == nil {
		var transfer terminal.TransferResult
		if json.Unmarshal(output, &transfer) == nil && transfer.Handoff.ID != "" {
			t.state.handoffs = append(t.state.handoffs, transfer.Handoff)
			if !transfer.Success {
				t.state.fail(fmt.Errorf("child runtime: terminal handoff receipt is unconfirmed"))
			}
		}
	}
	if err != nil {
		entry.Error = err.Error()
		// Human takeover is a temporary input fence, not failure of the
		// entire delegated task. Keep the rejected attempt in the trace;
		// the worker may observe output and resume after control returns.
		if !errors.Is(err, terminal.ErrHumanControl) && !errors.Is(err, terminal.ErrControlChanged) && !errors.Is(err, terminal.ErrInputOrder) && !errors.Is(err, terminal.ErrOwnershipTransferred) {
			t.state.fail(fmt.Errorf("child runtime: %s failed: %w", t.Name(), err))
		}
	} else {
		entry.Success = true
		var outcome struct {
			Outcome  string `json:"outcome"`
			ExitCode *int   `json:"exit_code"`
			TimedOut bool   `json:"timed_out"`
			Success  *bool  `json:"success"`
			Error    string `json:"error"`
		}
		if json.Unmarshal(output, &outcome) == nil {
			if t.Name() == "terminal" && outcome.Outcome == "unknown" {
				t.state.fail(fmt.Errorf("child runtime: terminal input delivery is uncertain; reconcile effects before another attempt"))
			}
			if outcome.ExitCode != nil && *outcome.ExitCode != 0 {
				entry.Success = false
				entry.Error = fmt.Sprintf("command exited with code %d", *outcome.ExitCode)
			}
			if outcome.TimedOut {
				entry.Success = false
				entry.Error = "command timed out"
			}
			if outcome.Success != nil && !*outcome.Success {
				entry.Success = false
				entry.Error = outcome.Error
				if entry.Error == "" {
					entry.Error = "tool reported failure"
				}
			}
		}
		if t.Name() == "patch" || args.Action == "write" || args.Action == "edit" || args.Action == "mkdir" {
			if path != "" {
				t.state.written[path] = true
			}
		} else if args.Action == "read" {
			t.state.read[path] = true
		}
	}
	t.state.trace = append(t.state.trace, entry)
	return output, err
}
func scopedLSPInput(workspace string, input json.RawMessage) (json.RawMessage, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(input, &args); err != nil {
		return input, err
	}
	var path string
	if err := json.Unmarshal(args["file"], &path); err != nil || path == "" {
		return input, fmt.Errorf("child runtime: LSP file is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return input, fmt.Errorf("child runtime: LSP file: %w", err)
	}
	relative, err := filepath.Rel(workspace, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return input, fmt.Errorf("child runtime: LSP file is outside workspace")
	}
	args["file"], _ = json.Marshal(resolved)
	return json.Marshal(args)
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func toolDefinition(name string) providers.Tool {
	schemas := map[string]string{
		"shell":          `{"type":"object","properties":{"command":{"type":"string"},"working_dir":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":900},"env":{"type":"object","additionalProperties":{"type":"string"}}},"required":["command"],"additionalProperties":false}`,
		"pty_shell":      `{"type":"object","properties":{"command":{"type":"string"},"cwd":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":900}},"required":["command"],"additionalProperties":false}`,
		"grep":           `{"type":"object","properties":{"pattern":{"type":"string"},"include":{"type":"string"},"max_results":{"type":"integer","minimum":1,"maximum":1000}},"required":["pattern"],"additionalProperties":false}`,
		"git":            `{"type":"object","properties":{"action":{"type":"string","enum":["status","diff","log","add","commit","push","reset","stash"]},"staged":{"type":"boolean"},"stat":{"type":"boolean"},"count":{"type":"integer"},"paths":{"type":"array","items":{"type":"string"}},"message":{"type":"string"},"ref":{"type":"string"},"stash_action":{"type":"string"}},"required":["action"],"additionalProperties":false}`,
		"lsp_hover":      `{"type":"object","properties":{"file":{"type":"string"},"line":{"type":"integer"},"column":{"type":"integer"}},"required":["file","line","column"],"additionalProperties":false}`,
		"lsp_definition": `{"type":"object","properties":{"file":{"type":"string"},"line":{"type":"integer"},"column":{"type":"integer"}},"required":["file","line","column"],"additionalProperties":false}`,
		"lsp_references": `{"type":"object","properties":{"file":{"type":"string"},"line":{"type":"integer"},"column":{"type":"integer"}},"required":["file","line","column"],"additionalProperties":false}`,
		"lsp_symbols":    `{"type":"object","properties":{"file":{"type":"string"}},"required":["file"],"additionalProperties":false}`,
	}
	if schema, ok := schemas[name]; ok {
		return providers.Tool{Name: name, Description: "Use the session's " + name + " capability in the delegated workspace. Shell commands run on the trusted host; they are not OS-sandboxed. Report actual results and exit codes.", Parameters: json.RawMessage(schema)}
	}
	if name == "patch" {
		return providers.Tool{Name: name, Description: "Apply a unified diff to an existing workspace file. Paths must stay inside the workspace.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"patch":{"type":"string"}},"required":["path","patch"],"additionalProperties":false}`)}
	}
	return providers.Tool{Name: "fs", Description: "Read, write, edit, glob, mkdir, or stat files within the workspace. read supports one-based line offset and limit. edit replaces one exact old string.", Parameters: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["read","write","edit","glob","mkdir","stat"]},"path":{"type":"string"},"content":{"type":"string"},"old":{"type":"string"},"new":{"type":"string"},"pattern":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":0}},"required":["action"],"additionalProperties":false}`)}
}
