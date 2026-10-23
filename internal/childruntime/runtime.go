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
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/instructions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/google/uuid"
)

// Resolver must resolve exactly the requested configured provider/model. It
// must not silently select another provider after a credentials failure.
type Resolver func(provider, model string) (providers.Provider, string, error)

type Factory struct {
	Instructions  instructions.Config
	Workspace     string
	Resolve       Resolver
	Skills        *skills.Registry
	ReadOnly      bool
	AuditReadOnly bool
	Personality   personality.Config
}

// Build constructs a fresh provider, history, registry, and receipts per child.
// Only contained filesystem operations are currently supported. Unsupported
// tools fail before any provider request or filesystem mutation takes place.
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
	selected := make(map[string]bool, len(cfg.Tools))
	for _, name := range cfg.Tools {
		if (f.AuditReadOnly && name != "fs" && name != "audit_search" && name != "audit_git") || (!f.AuditReadOnly && name != "fs" && name != "patch") {
			available := "fs and patch"
			if f.AuditReadOnly {
				available = "fs(read/glob/stat), audit_search, and audit_git"
			}
			return nil, fmt.Errorf("%w: child runtime tool %q; available tools are %s", subagent.ErrUnsupportedCapability, name, available)
		}
		selected[name] = true
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
	return &runner{instructionSource: instructionSource, cfg: cfg, workspace: workspace, provider: provider, selected: selected, skills: f.Skills, auditReadOnly: f.AuditReadOnly, readOnly: f.ReadOnly, personality: f.Personality}, nil
}

type runner struct {
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
	if r.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
	}
	state := &runState{read: map[string]bool{}, written: map[string]bool{}}
	registry := tools.NewRegistry()
	for _, name := range []string{"fs", "patch", "audit_search", "audit_git"} {
		if !r.selected[name] {
			continue
		}
		var tool tools.Tool
		if name == "fs" {
			tool = tools.NewFSTool(r.workspace)
			if r.auditReadOnly {
				tool = &auditReadTool{Tool: tool}
			}
		} else if name == "patch" {
			tool = tools.NewPatchTool(r.workspace)
		} else {
			tool = &auditInspectTool{name: name, workspace: r.workspace}
		}
		if err := registry.Register(&recordingTool{Tool: tool, workspace: r.workspace, state: state}); err != nil {
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
	child := agent.New(agent.Config{InstructionSource: r.instructionSource, ContextWindow: contextWindow, TokenLimits: limits, Tokenizer: tokenizer.NewEstimator(), Provider: metered, Tools: registry, Model: r.cfg.Model, MaxTokens: outputTokens, MaxSteps: r.cfg.MaxSteps, SessionID: "child-" + uuid.NewString(), DisableAtMentions: true, SystemPrompt: childSystemPrompt})
	if r.readOnly {
		child.SetAutoMode("readonly")
	}
	child.SetSkillRegistry(r.skills)
	defer child.Shutdown()
	output, runErr := child.Run(ctx, "Delegated task:\n"+r.cfg.Goal+"\n\nSupplied context:\n"+r.cfg.Context)
	state.mu.Lock()
	defer state.mu.Unlock()
	result := &subagent.Result{TaskIndex: r.cfg.TaskIndex, Status: "completed", ExitReason: "completed", TokensIn: state.in, TokensOut: state.out, CostUSD: state.cost, DurationMs: time.Since(start).Milliseconds(), ToolTrace: append([]subagent.ToolEntry(nil), state.trace...)}
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
	output, err := t.Tool.Execute(ctx, input)
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
	if err != nil {
		entry.Error = err.Error()
		t.state.fail(fmt.Errorf("child runtime: %s failed: %w", t.Name(), err))
	} else {
		entry.Success = true
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
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func toolDefinition(name string) providers.Tool {
	if name == "patch" {
		return providers.Tool{Name: name, Description: "Apply a unified diff to an existing workspace file. Paths must stay inside the workspace.", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"patch":{"type":"string"}},"required":["path","patch"],"additionalProperties":false}`)}
	}
	return providers.Tool{Name: "fs", Description: "Read, write, edit, glob, mkdir, or stat files within the workspace. read supports one-based line offset and limit. edit replaces one exact old string.", Parameters: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["read","write","edit","glob","mkdir","stat"]},"path":{"type":"string"},"content":{"type":"string"},"old":{"type":"string"},"new":{"type":"string"},"pattern":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":0}},"required":["action"],"additionalProperties":false}`)}
}
