package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Sahaj-Tech-ltd/ethos/internal/hooks"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/security"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tools"
)

type Agent struct {
	mu              sync.RWMutex
	provider        providers.Provider
	toolRegistry    *tools.Registry
	compressors     *tools.CompressorRegistry
	hooks           *hooks.Registry
	scanners        []security.Scanner
	tokenizer       *tokenizer.Estimator
	budgetEstimator *BudgetEstimator
	forethinker     *Forethinker
	steering        *SteeringQueue
	specDriver      *SpecDriver
	recovery        *ErrorRecovery
	bus             *EventBus
	model           string
	maxTokens       int
	systemPrompt    string
	history         []providers.Message
	maxSteps        int
	sessionID       string
	approvalFn      ApprovalFunc
	allowedTools    map[string]bool
	questionFn      QuestionFunc
	permLedger      *security.Ledger
	// contextProviderFn, if set, is invoked once per turn before the model
	// call. The returned snippet is appended to the system prompt. Used by
	// plugins (and anything else) to inject per-turn context like git status,
	// jira ticket, etc. Errors and timeouts inside the callback are the
	// caller's responsibility — the agent treats an empty return as "nothing
	// to add" and never blocks the loop on it.
	contextProviderFn func(ctx context.Context, sessionID string) string
	// eventFn, if set, is fired (best-effort, fire-and-forget) at known
	// lifecycle moments: tool_call, compact, error. Plugins subscribe.
	eventFn func(event string, payload map[string]any)
}

type Config struct {
	Provider     providers.Provider
	Tools        *tools.Registry
	Compressors  *tools.CompressorRegistry
	Hooks        *hooks.Registry
	Scanners     []security.Scanner
	Tokenizer    *tokenizer.Estimator
	Forethinker  *Forethinker
	Steering     *SteeringQueue
	SpecDriver   *SpecDriver
	Model        string
	MaxTokens    int
	SystemPrompt string
	MaxSteps     int
	SessionID    string
}

func New(cfg Config) *Agent {
	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 20
	}

	var budgetEst *BudgetEstimator
	if cfg.Tokenizer != nil {
		budgetEst = NewBudgetEstimator(cfg.Tokenizer, cfg.MaxTokens)
	}

	forethinker := cfg.Forethinker
	if forethinker == nil {
		forethinker = NewForethinker()
	}

	specDrv := cfg.SpecDriver
	if specDrv == nil {
		specDrv = NewSpecDriver()
	}

	return &Agent{
		provider:        cfg.Provider,
		toolRegistry:    cfg.Tools,
		compressors:     cfg.Compressors,
		hooks:           cfg.Hooks,
		scanners:        cfg.Scanners,
		tokenizer:       cfg.Tokenizer,
		budgetEstimator: budgetEst,
		forethinker:     forethinker,
		steering:        cfg.Steering,
		specDriver:      specDrv,
		recovery:        NewErrorRecovery(nil),
		bus:             NewEventBus(),
		model:           cfg.Model,
		maxTokens:       cfg.MaxTokens,
		systemPrompt:    cfg.SystemPrompt,
		maxSteps:        maxSteps,
		sessionID:       cfg.SessionID,
		history:         make([]providers.Message, 0),
		allowedTools:    make(map[string]bool),
	}
}

// Bus returns the agent's internal event bus. Subscribers receive structured
// lifecycle events (tool_impact, budget_warning, recovery, confidence). Returns
// nil only if the agent itself is nil.
func (a *Agent) Bus() *EventBus {
	if a == nil {
		return nil
	}
	return a.bus
}

// SetRecoveryWriter installs a journal writer used by the error-recovery
// pipeline to persist lessons learned. Pass nil to disable persistence; the
// in-memory recovery report still flows through events either way.
func (a *Agent) SetRecoveryWriter(w JournalEntryWriter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recovery = NewErrorRecovery(w)
}

type RunResult struct {
	Response    string                `json:"response"`
	ToolCalls   int                   `json:"tool_calls"`
	TotalTokens int                   `json:"total_tokens"`
	Steps       int                   `json:"steps"`
	Model       string                `json:"model"`
	Blocked     bool                  `json:"blocked"`
	BlockReason string                `json:"block_reason,omitempty"`
	Confidence  *ConfidenceAssessment `json:"confidence,omitempty"`
}

func (a *Agent) Run(ctx context.Context, userInput string) (*RunResult, error) {
	for _, scanner := range a.scanners {
		result, err := scanner.Scan(userInput)
		if err != nil {
			return nil, fmt.Errorf("agent: security scan failed: %w", err)
		}
		if result.Blocked {
			return &RunResult{
				Blocked:     true,
				BlockReason: fmt.Sprintf("blocked by %s: %s", scanner.Name(), result.Findings[0].Description),
				Model:       a.model,
			}, nil
		}
	}

	a.mu.Lock()
	a.history = append(a.history, providers.Message{
		Role:    "user",
		Content: userInput,
	})
	a.mu.Unlock()

	// Pre-load any files the user referenced with @path so the model has
	// their contents in-context without needing to call a tool.
	if attached := loadAtMentions(userInput); attached != "" {
		a.appendMessage(providers.Message{
			Role:    "system",
			Content: "files referenced via @path:\n" + attached,
		})
	}

	if a.specDriver != nil && a.specDriver.IsEnabled() && a.specDriver.ShouldSpec(userInput) {
		a.appendMessage(providers.Message{
			Role:    "system",
			Content: a.specDriver.BuildSpecPrompt(userInput),
		})
	}

	result := &RunResult{
		Model: a.model,
	}

	for step := 0; step < a.maxSteps; step++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("agent: context cancelled: %w", ctx.Err())
		default:
		}

		// Pre-flight budget check. Best-effort; failure here never blocks.
		a.checkBudget()

		stepResult, err := a.step(ctx)
		if err != nil {
			if a.hooks != nil {
				a.hooks.Fire(ctx, hooks.OnError, hooks.Event{
					Error:     err,
					SessionID: a.sessionID,
				})
			}
			a.emitRecovery(err)
			return nil, fmt.Errorf("agent: step %d: %w", step+1, err)
		}

		result.Steps++
		result.ToolCalls += len(stepResult.ToolCalls)
		result.TotalTokens += stepResult.Tokens.InputTokens + stepResult.Tokens.OutputTokens

		if stepResult.Done {
			result.Response = stepResult.Thought
			result.Confidence = a.assessTurnConfidence(userInput)
			return result, nil
		}
	}

	return nil, fmt.Errorf("agent: exceeded max steps (%d)", a.maxSteps)
}

// emitImpact runs the forethought assessment for a pending tool call and
// publishes a tool_impact event. Defensive — never returns or panics.
func (a *Agent) emitImpact(toolName string, input json.RawMessage) {
	a.mu.RLock()
	f := a.forethinker
	a.mu.RUnlock()
	if f == nil {
		return
	}
	defer func() { _ = recover() }()
	ia := f.Assess(toolName, input)
	if ia == nil {
		return
	}
	risk := "low"
	switch ia.RiskLevel {
	case RiskMedium:
		risk = "medium"
	case RiskHigh:
		risk = "high"
	}
	a.emit("tool_impact", map[string]any{
		"tool":           toolName,
		"risk":           risk,
		"protected":      ia.Protected,
		"affected_paths": ia.AffectedPaths,
		"reasoning":      ia.Reasoning,
		"session_id":     a.sessionID,
	})
}

// checkBudget runs the budget estimator against current state and emits a
// budget_warning event when the warn threshold is crossed. No-op when the
// estimator isn't wired (e.g., in unit tests with a nil tokenizer).
func (a *Agent) checkBudget() {
	if a.budgetEstimator == nil {
		return
	}
	report := a.BudgetReport()
	if report == nil || !report.ShouldWarn {
		return
	}
	a.emit("budget_warning", map[string]any{
		"utilization":    report.Utilization,
		"total_estimate": report.TotalEstimate,
		"max_tokens":     report.MaxTokens,
		"should_compact": report.ShouldCompact,
		"session_id":     a.sessionID,
	})
}

// assessTurnConfidence scores how confident the agent should be in its own
// answer using AssessConfidence. Defensive: any panic returns nil.
func (a *Agent) assessTurnConfidence(userInput string) (out *ConfidenceAssessment) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
		}
	}()
	a.mu.RLock()
	hist := make([]providers.Message, len(a.history))
	copy(hist, a.history)
	model := a.model
	a.mu.RUnlock()
	return AssessConfidence(userInput, hist, model)
}

// emitRecovery generates a structured recovery report and ships it through
// the event channel + journal writer (if any). Errors here are swallowed —
// the original step error is what callers see.
func (a *Agent) emitRecovery(stepErr error) {
	a.mu.RLock()
	rec := a.recovery
	hist := make([]providers.Message, len(a.history))
	copy(hist, a.history)
	a.mu.RUnlock()
	if rec == nil {
		return
	}
	defer func() { _ = recover() }()
	report := rec.Analyze(stepErr, hist)
	if report == nil {
		return
	}
	_ = rec.RecordLesson(report)
	a.emit("recovery", map[string]any{
		"what_went_wrong": report.WhatWentWrong,
		"root_cause":      report.RootCause,
		"learning_plan":   report.LearningPlan,
		"immediate_fix":   report.ImmediateFix,
		"session_id":      a.sessionID,
	})
	a.emit("error", map[string]any{
		"error":      stepErr.Error(),
		"session_id": a.sessionID,
	})
}

func (a *Agent) History() []providers.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()

	cp := make([]providers.Message, len(a.history))
	copy(cp, a.history)
	return cp
}

func (a *Agent) SetHistory(messages []providers.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.history = make([]providers.Message, len(messages))
	copy(a.history, messages)
}

func (a *Agent) ClearHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.history = make([]providers.Message, 0)
}

func (a *Agent) buildRequest() providers.Request {
	a.mu.RLock()
	msgs := make([]providers.Message, len(a.history))
	copy(msgs, a.history)
	a.mu.RUnlock()

	prompt := BuildSystemPrompt(a.systemPrompt, a.toolRegistry)
	// Append plugin/host-supplied per-turn context (git status, jira ticket,
	// project conventions, etc.). The callback is responsible for its own
	// timeouts; recover() inside providedContext keeps a misbehaving plugin
	// from killing the loop.
	if extra := a.providedContext(context.Background()); extra != "" {
		prompt = prompt + "\n\n" + extra
	}

	return providers.Request{
		Model:        a.model,
		Messages:     msgs,
		Tools:        a.buildToolDefs(),
		MaxTokens:    a.maxTokens,
		SystemPrompt: prompt,
	}
}

func (a *Agent) buildToolDefs() []providers.Tool {
	var toolDefs []providers.Tool
	if a.toolRegistry != nil {
		for _, name := range a.toolRegistry.List() {
			t, _ := a.toolRegistry.Get(name)
			toolDefs = append(toolDefs, providers.Tool{
				Name:        t.Name(),
				Description: "Execute tool: " + t.Name(),
			})
		}
	}
	return toolDefs
}

func (a *Agent) SetModel(modelID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = modelID
}

// ToolNames returns the names of registered tools, used by the TUI status
// dialog. Returns an empty slice if no registry is wired.
func (a *Agent) ToolNames() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.toolRegistry == nil {
		return nil
	}
	return a.toolRegistry.List()
}

func (a *Agent) Model() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

func (a *Agent) IsBusy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.history) > 0
}

func (a *Agent) SessionID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessionID
}

// SetSessionID assigns the active session id. Used after the agent is wired
// to associate streaming events and ledger entries with the right session.
func (a *Agent) SetSessionID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionID = id
}

func (a *Agent) BudgetReport() *BudgetReport {
	if a.budgetEstimator == nil {
		return nil
	}

	a.mu.RLock()
	msgs := make([]providers.Message, len(a.history))
	copy(msgs, a.history)
	a.mu.RUnlock()

	toolDefs := a.buildToolDefs()
	systemPrompt := BuildSystemPrompt(a.systemPrompt, a.toolRegistry)

	return a.budgetEstimator.Estimate(msgs, systemPrompt, toolDefs)
}

func (a *Agent) Inject(msg providers.Message) {
	a.appendMessage(msg)
}

// CompactResult summarizes the outcome of a Compact call.
type CompactResult struct {
	TokensBefore   int    `json:"tokens_before"`
	TokensAfter    int    `json:"tokens_after"`
	Summary        string `json:"summary"`
	MessagesBefore int    `json:"messages_before"`
	MessagesAfter  int    `json:"messages_after"`
}

// Compact summarizes the current conversation history into a single message,
// drastically reducing token usage while preserving key decisions and state.
func (a *Agent) Compact(ctx context.Context) (*CompactResult, error) {
	a.mu.RLock()
	history := make([]providers.Message, len(a.history))
	copy(history, a.history)
	a.mu.RUnlock()

	if len(history) == 0 {
		return &CompactResult{}, nil
	}

	tokensBefore := 0
	if a.tokenizer != nil {
		for _, m := range history {
			tokensBefore += a.tokenizer.Estimate(m.Content) + 4
		}
	}

	// Build a compaction request: include the existing history plus a system
	// instruction asking for a concise summary.
	compactPrompt := "Summarize the conversation above into a single concise paragraph. " +
		"Preserve: 1) key decisions made, 2) files changed and the nature of the changes, " +
		"3) outstanding tasks or open questions, 4) important context the user has shared. " +
		"Be specific. Do not greet or sign off. Output the summary text only."

	msgs := append([]providers.Message{}, history...)
	msgs = append(msgs, providers.Message{Role: "user", Content: compactPrompt})

	req := providers.Request{
		Model:        a.model,
		Messages:     msgs,
		MaxTokens:    a.maxTokens,
		SystemPrompt: "You produce dense factual summaries of prior conversation.",
	}

	resp, err := a.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("agent: compact: %w", err)
	}

	summary := resp.Content
	if summary == "" {
		return nil, fmt.Errorf("agent: compact: provider returned empty summary")
	}

	newHistory := []providers.Message{
		{Role: "assistant", Content: "[compacted history] " + summary},
	}

	a.mu.Lock()
	msgsBefore := len(a.history)
	a.history = newHistory
	a.mu.Unlock()

	tokensAfter := 0
	if a.tokenizer != nil {
		tokensAfter = a.tokenizer.Estimate(summary) + 4
	}

	a.emit("compact", map[string]any{
		"tokens_before": tokensBefore,
		"tokens_after":  tokensAfter,
		"session_id":    a.sessionID,
	})

	return &CompactResult{
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
		Summary:        summary,
		MessagesBefore: msgsBefore,
		MessagesAfter:  len(newHistory),
	}, nil
}

// Approval represents a user decision on a tool execution request.
type Approval struct {
	Allow   bool
	Persist bool // remember choice for the rest of the session
}

// ApprovalFunc is invoked by the agent before executing a risky tool. It must
// block until the user decides. If nil, the agent auto-allows.
type ApprovalFunc func(toolName string, args string, risk string) Approval

// SetContextProvider installs a callback that returns extra system-prompt
// text to inject before each turn. Used by the TUI to bridge plugin context
// providers without the agent depending on the plugin package. Empty return
// means "nothing to add". The callback should respect ctx for cancellation.
func (a *Agent) SetContextProvider(fn func(ctx context.Context, sessionID string) string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contextProviderFn = fn
}

// SetEventFn installs a fire-and-forget event callback used to notify
// subscribers (plugins, journal, telemetry) about lifecycle moments.
// Known events: "tool_call", "compact", "error", "chat_message".
func (a *Agent) SetEventFn(fn func(event string, payload map[string]any)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.eventFn = fn
}

// emit fires the event callback if installed and publishes a parallel event
// on the internal bus. Non-blocking, no error path. Either delivery channel
// (callback or bus) being nil is fine — best-effort fanout.
func (a *Agent) emit(event string, payload map[string]any) {
	a.mu.RLock()
	fn := a.eventFn
	bus := a.bus
	a.mu.RUnlock()
	if fn != nil {
		func() {
			defer func() { _ = recover() }()
			fn(event, payload)
		}()
	}
	if bus != nil {
		bus.Emit(EventKind(event), payload)
	}
}

// providedContext returns extra system-prompt content from the context
// provider callback, or empty string if none. Wrapped in recover so a
// misbehaving plugin can't take down the agent.
func (a *Agent) providedContext(ctx context.Context) (out string) {
	a.mu.RLock()
	fn := a.contextProviderFn
	a.mu.RUnlock()
	if fn == nil {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			out = ""
		}
	}()
	return fn(ctx, a.sessionID)
}

// SetApprovalFunc installs a callback to gate risky tool calls. Pass nil to
// disable approval prompts.
func (a *Agent) SetApprovalFunc(fn ApprovalFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.approvalFn = fn
	if a.allowedTools == nil {
		a.allowedTools = make(map[string]bool)
	}
}

// SetPermissionLedger attaches a ledger that records every approval decision
// for the /permissions overlay.
func (a *Agent) SetPermissionLedger(l *security.Ledger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permLedger = l
}

// PermissionLog returns a snapshot of decisions for the active session.
// Returns nil if no ledger is attached.
func (a *Agent) PermissionLog() []security.LedgerEntry {
	a.mu.RLock()
	l := a.permLedger
	a.mu.RUnlock()
	if l == nil {
		return nil
	}
	return l.Entries()
}

// approvalCheck is invoked from the react/stream loops to ask the user before
// running a risky tool. It returns true if the call may proceed.
func (a *Agent) approvalCheck(toolName, args, risk string) bool {
	a.mu.RLock()
	fn := a.approvalFn
	if a.allowedTools[toolName] {
		a.mu.RUnlock()
		return true
	}
	a.mu.RUnlock()

	if fn == nil {
		return true
	}

	dec := fn(toolName, args, risk)
	if dec.Persist && dec.Allow {
		a.mu.Lock()
		a.allowedTools[toolName] = true
		a.mu.Unlock()
	}
	a.mu.RLock()
	ledger := a.permLedger
	a.mu.RUnlock()
	if ledger != nil {
		decision := "deny"
		if dec.Allow {
			if dec.Persist {
				decision = "allow_session"
			} else {
				decision = "allow_once"
			}
		}
		_ = ledger.Append(security.LedgerEntry{
			Tool:     toolName,
			Args:     args,
			Decision: decision,
			Risk:     risk,
		})
	}
	return dec.Allow
}

func (a *Agent) appendMessage(msg providers.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = append(a.history, msg)
}

func (a *Agent) appendToolResultMessage(toolCallID, toolName string, output json.RawMessage, toolErr error) {
	content := string(output)
	if toolErr != nil {
		content = fmt.Sprintf("error: %s", toolErr.Error())
	}

	a.appendMessage(providers.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
	})
}
