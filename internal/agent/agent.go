package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/events"
	"github.com/Sahaj-Tech-ltd/overkill/internal/features"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/speculative"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// InputKind classifies user input routing (shell vs NL).
type InputKind int

const (
	InputKindNL        InputKind = iota
	InputKindShell
	InputKindAmbiguous
)

// ExtensionsManager is satisfied by extensions.Manager. Tiny interface so
// agent doesn't depend on the extensions package for listing.
type ExtensionsManager interface {
	ListEnabled() []ExtensionMeta
}

// ExtensionMeta is the minimal metadata for one extension, used in prompt
// rendering. Kept simple to avoid importing the extensions package.
type ExtensionMeta struct {
	ID          string
	Name        string
	Kind        string
	Description string
}

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
	// privilege is the optional write-gate. Stored as atomic.Pointer so
	// the hot read in react.go's tool dispatch path doesn't need a.mu
	// (SetPrivilegeGate writes via Store).
	privilege atomic.Pointer[security.PrivilegeGate]
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
	// rewriter, if set, is invoked once per turn against the most-recent user
	// message. The rewritten text replaces the message before the system
	// prompt is built. Errors fall back to the original message; never blocks.
	rewriter PromptRewriter
	// compactor, when set, owns the compaction strategy. When nil the agent
	// falls back to its legacy single-LLM-call summary path. Stored as an
	// atomic.Pointer because checkBudget reads it from the hot Run() loop
	// without taking a.mu (the path can fire while buildRequest already
	// holds RLock — RWMutex isn't reentrant, so RLocking again would
	// deadlock with a concurrent SetCompactor caller waiting for Lock).
	// The boxed struct lets us atomic-swap a Go interface (two-word value
	// — not safe under a plain field read).
	compactor atomic.Pointer[compactorBox]
	// useCompactor is an atomic Bool because checkBudget reads it from the
	// same hot path that motivated the compactor pointer. SetCompactor
	// writes it.
	useCompactor       atomic.Bool
	compactionInFlight atomic.Bool

	// userInputObserver, if set, is invoked once per Run with the raw user
	// input before scanners or rewriter. Used by the frustration detector
	// (and anything else that wants a non-blocking peek). Best-effort:
	// panics are recovered. Stored as atomic.Pointer so Run can read it
	// without holding a.mu (which the setter takes).
	userInputObserver atomic.Pointer[userInputObserverBox]

	// flowStore, if set, persists agent state when the loop hits
	// maxSteps so a follow-up alarm can resume the task. Nil-safe —
	// when unset, max-steps exits with EventError as before.
	flowStore FlowStore
	// flowAlarmSink, if set, is called when a checkpoint succeeds so
	// the wiring layer can schedule an alarm that picks up the flow.
	// Decoupled so the agent doesn't depend on automation.AlarmClock
	// directly.
	flowAlarmSink func(state *FlowState)

	// receipts is the cryptographic tool-call audit chain (§7.1
	// Emergency Controls). Always allocated. Append happens on every
	// tool dispatch; VerifyChain proves no entries were edited after
	// the fact.
	receipts *ReceiptChain

	// autoMode governs autonomous execution behavior (safe/yolo/auto).
	// When non-nil and Level == AutonomyAuto, the agent loads plans,
	// batches clarifying questions, and executes phases autonomously.
	// Set via SetAutoMode; nil means legacy behavior.
	autoMode *AutoMode

	// postWriteVerifier checks files written by tool calls for
	// well-formedness (Batch G2). Optional; nil disables. Separate
	// mutex from a.mu so the hot path isn't extended.
	verifierMu        sync.RWMutex
	postWriteVerifier PostWriteVerifier

	// reflector runs Reflexion-class self-correction (paper #51
	// AlphaGRPO recipe). On a failed tool result it produces a
	// structured "you tried X, it failed because Y, try Z" note
	// that gets injected as a system message before the next model
	// call. Optional; nil disables. Shares verifierMu since both
	// hook into the same post-tool-batch site.
	reflector Reflector
	// reflectionBudget caps how many reflection notes we inject per
	// turn (one tool batch can have many failures; we don't want to
	// flood the next prompt). 2 is the default; 0 disables.
	reflectionBudget int

	// hallucinationScanner annotates the assembled response with
	// [?] markers after unverified identifier references (Batch G3).
	// Optional; nil disables. Uses a.mu since reads happen on the
	// post-stream path that already holds the lock.
	hallucinationScanner HallucinationScanner

	// stopCh is closed when an external estop fires. The streaming
	// loop selects on it alongside ctx.Done() so an estop interrupts
	// in-flight tool dispatch as fast as cancellation.
	stopMu sync.Mutex
	stopCh chan struct{}

	// streamCancel is set when Stream() is called, wrapping the caller's
	// context with WithCancel. Interrupt() calls it to abort the current
	// stream. No-op when nil (no stream running).
	streamMu     sync.Mutex
	streamCancel context.CancelFunc

	// modelRouter, if set, is invoked at the start of each Run with a
	// classification of the user input + history. The returned model ID
	// replaces a.model for that turn only. Failures fall back to the static
	// model. See SetModelRouter.
	modelRouter atomic.Pointer[modelRouterBox]

	// usageObserver, if set, is fired after each step with the per-call
	// token usage. Wired by cmd/overkill to feed cost.Tracker.Record().
	// Best-effort: panics are recovered. atomic.Pointer for the same
	// reason as userInputObserver.
	usageObserver atomic.Pointer[usageObserverBox]

	// promptCompressor, if set, is invoked on the assembled system prompt
	// when token utilization is high (>= compressTrigger). Failures fall
	// back to the original prompt; never blocks Run().
	promptCompressor PromptCompressor
	compressTrigger  float64 // utilization fraction; default 0.7

	// skillRegistry, if set, contributes an "Active skills:" section to the
	// system prompt every turn. See skills_prompt.go for selection rules.
	skillRegistry *skills.Registry

	// memoryRetriever, if set, is consulted each turn for top-K memories
	// relevant to the latest user message. The result renders into the
	// system prompt between skills and personality. Best-effort: errors,
	// panics, and timeouts never block the turn (see renderMemorySection).
	memoryRetriever MemoryRetriever

	// personalityProviderFn, if set, returns a personality directive block
	// appended to the system prompt each turn. Separate from contextProviderFn
	// because personality is a long-lived character directive, while context
	// is per-turn factual (git status, jira ticket). Errors/panics are
	// recovered; empty return means "no personality directive this turn".
	personalityProviderFn func() string

	// diagEscalator climbs the 10-tier diagnostic ladder (§4.13) on each
	// agent step error. Lazily constructed by emitRecovery; per-session
	// state.
	diagEscalator *diagnosticEscalator

	// sessionCtx / sessionCancel parent every background goroutine the
	// agent spawns (auto-compaction, lifecycle workers). Shutdown()
	// cancels sessionCtx so leaked goroutines wind down promptly. Set
	// in New(); never replaced.
	sessionCtx    context.Context
	sessionCancel context.CancelFunc

	// checkpointSnapshotter, if set, is invoked automatically before
	// destructive tool calls so the user always has a rollback target.
	// §4.8: "AI WILL delete features, AI WILL go rogue — git is the
	// safety net." Nil-safe.
	checkpointSnapshotter CheckpointSnapshotter

	// learningRecorder receives a class key on each Run() that succeeds
	// after a prior Run() failed with the same class — the "I recovered
	// from this" signal (§6.2). Nil-safe.
	learningRecorder LearningRecorder
	// lastErrorClass holds the diagnostic class from the most recent
	// emitRecovery. Consumed (and cleared) by the next successful Run().
	lastErrorClass string

	// learningStore persists user corrections and retrieves them for
	// injection into the system prompt (§6.5). Nil-safe — when unset,
	// correction recording and retrieval are no-ops.
	learningStore *learning.Store

	// memoryArchiver, if set, receives each evicted message during
	// Compact so the original full-text survives in cold storage and
	// can be retrieved later via memory_search (master plan §6.1
	// hot/cold paging). Nil-safe — archive failures never block
	// compaction.
	memoryArchiver MemoryArchiver

	// lastPreCompactAt throttles the §4.4 pre-compaction checkpoint
	// at ≈48% so we don't compact more than once per minute even
	// when the user is firing back-to-back big tasks. Zero value =
	// never pre-compacted.
	lastPreCompactAt time.Time

	// beatRecorder fires §6.3 relationship milestones from inside
	// the hot path. Nil-safe — see recordBeat helper.
	beatRecorder BeatRecorder

	// responseFilter transforms the assembled assistant content
	// before it's committed to history (§4.10 sycophancy reducer).
	// Nil-safe; runs once per turn, post-stream.
	responseFilter ResponseFilter

	// completionEmitter, if set, receives a CompletionEvent when every
	// Run() exits (§8.7.2). Nil-safe — absent emitter is a no-op.
	completionEmitter *events.Emitter
	// costTracker, if set, is queried at session end to populate
	// CompletionEvent.CostUSD. When nil, cost is reported as 0.
	costTracker cost.Tracker

	// flightRecorder is the journal's append-only flight recorder (§4.19).
	// When set, every user input, agent reply, and tool call is logged to
	// ~/.overkill/journal/raw/ as JSONL. Nil-safe — absent recorder is a
	// no-op during Run(). Set via SetFlightRecorder.
	flightRecorder *journal.FlightRecorder

	// featureManager gates prompt sections behind feature flags (P1).
	// Nil-safe — when unset, all features are treated as enabled.
	featureManager *features.Manager

	// readCache caches file reads for speculative tool execution (P2).
	// Nil-safe — when unset, file reads go directly to disk.
	readCache *speculative.ReadCache

	// extensionsManager tracks loaded extensions for prompt rendering (P2).
	// Nil-safe — when unset, no extensions section is rendered.
	extensionsManager ExtensionsManager

	// inputClassifier, if set, is called to classify raw user input before
	// the agent loop. Nil-safe — unset means all input is treated as NL.
	inputClassifier func(string) InputKind

	// sessionMetrics accumulates per-Run stats for drift detection (P3).
	sessionMetrics sessionMetrics
}

// sessionMetrics tracks per-session stats for drift detection (P3).
// Reset on each new session; aggregated by cmd/overkill on session end.
type sessionMetrics struct {
	mu               sync.Mutex
	toolCalls        int
	errors           int
	recoveries       int
	turns            int
	totalTurnDuration time.Duration
}

// SessionMetrics returns a snapshot of the current session's metrics.
func (a *Agent) SessionMetrics() (toolCalls int, errors int, recoveries int, turns int, totalTurnDuration time.Duration) {
	if a == nil {
		return
	}
	a.sessionMetrics.mu.Lock()
	defer a.sessionMetrics.mu.Unlock()
	return a.sessionMetrics.toolCalls, a.sessionMetrics.errors, a.sessionMetrics.recoveries, a.sessionMetrics.turns, a.sessionMetrics.totalTurnDuration
}

// PromptCompressor is the small interface the agent calls before assembling
// each turn's system prompt. compaction.PromptCompressor satisfies this via
// a thin shim defined in cmd/overkill (kept here so the agent stays free of
// the compaction → providers import chain on this code path).
type PromptCompressor interface {
	Compress(ctx context.Context, prompt string) (compressed string, savedTokens int, err error)
}

// SetPromptCompressor wires the compressor + trigger threshold (utilization
// fraction). threshold <= 0 defaults to 0.7. Pass nil to disable.
func (a *Agent) SetPromptCompressor(c PromptCompressor, threshold float64) {
	a.mu.Lock()
	a.promptCompressor = c
	if threshold <= 0 {
		threshold = 0.7
	}
	a.compressTrigger = threshold
	a.mu.Unlock()
}

// SetUsageObserver wires a callback fired after every step with the
// per-step token usage. Pass nil to clear.
func (a *Agent) SetUsageObserver(fn func(modelID string, usage providers.Usage)) {
	if fn == nil {
		a.usageObserver.Store(nil)
		return
	}
	a.usageObserver.Store(&usageObserverBox{fn: fn})
}

// SetAutoMode configures autonomous execution behavior. Pass a level string
// ("safe", "yolo", "auto") to enable; pass "" or "supervised" for legacy
// behavior. Auto mode loads plan files, batches upfront questions, and
// chains phases without human input.
func (a *Agent) SetAutoMode(level string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch level {
	case "safe", "yolo", "auto":
		a.autoMode = NewAutoMode(level)
	default:
		a.autoMode = nil
	}
}

// AutoMode returns the current auto-mode controller, or nil if disabled.
func (a *Agent) AutoMode() *AutoMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autoMode
}

// usageObserverBox wraps the func so it can be stored in atomic.Pointer
// (which only accepts concrete struct pointers, not bare func values).
type usageObserverBox struct {
	fn func(modelID string, usage providers.Usage)
}

// ModelRouter is the small interface the agent calls into to pick a model.
// SmartRouter (internal/routing) satisfies a thin adapter; tests inject a fake.
type ModelRouter interface {
	// PickModel returns the model ID to use for this turn given a routing
	// snapshot. Implementations should be fast (<10ms) — they sit on the
	// hot path of every user message.
	PickModel(snap RouteSnapshot) (modelID string, reason string, ok bool)
}

// RouteSnapshot is the data the router sees per Run. Kept tiny so we can
// avoid importing the routing package from agent (one-way deps).
type RouteSnapshot struct {
	UserInput      string
	HistoryLen     int
	ToolCallCount  int
	HasAttachments bool
}

// SetModelRouter wires a per-turn model picker. Pass nil to disable.
func (a *Agent) SetModelRouter(r ModelRouter) {
	if r == nil {
		a.modelRouter.Store(nil)
		return
	}
	a.modelRouter.Store(&modelRouterBox{ModelRouter: r})
}

// SetInputClassifier wires a function that classifies raw user input into
// shell vs NL vs ambiguous. Pass nil to clear (all input treated as NL).
func (a *Agent) SetInputClassifier(fn func(string) InputKind) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inputClassifier = fn
}

// ClassifyInput runs the installed classifier against raw input. When no
// classifier is set, returns InputKindNL (everything is natural language).
func (a *Agent) ClassifyInput(raw string) InputKind {
	a.mu.RLock()
	fn := a.inputClassifier
	a.mu.RUnlock()
	if fn == nil {
		return InputKindNL
	}
	return fn(raw)
}

// modelRouterBox wraps the interface so it can be stored in
// atomic.Pointer (which only takes concrete pointer types).
type modelRouterBox struct{ ModelRouter }

// SetUserInputObserver wires a callback fired once per Run with the user's
// raw text. Pass nil to clear.
func (a *Agent) SetUserInputObserver(fn func(input string)) {
	if fn == nil {
		a.userInputObserver.Store(nil)
		return
	}
	a.userInputObserver.Store(&userInputObserverBox{fn: fn})
}

// userInputObserverBox: same box pattern as usageObserverBox.
type userInputObserverBox struct {
	fn func(input string)
}

// SetFlowStore wires durable flow checkpointing. When set, hitting
// maxSteps mid-stream saves the agent's state via the store and
// invokes the alarm sink (if also configured) so a follow-up alarm
// can resume the task. Pass nil to disable — max-steps exits as
// EventError, no checkpoint persisted.
func (a *Agent) SetFlowStore(store FlowStore, alarmSink func(*FlowState)) {
	a.mu.Lock()
	a.flowStore = store
	a.flowAlarmSink = alarmSink
	a.mu.Unlock()
}

// SetLearningStore wires the correction learning store (§6.5). When set,
// the agent queries for relevant past corrections before each Run() and
// records new corrections after successful turns. Pass nil to disable.
func (a *Agent) SetLearningStore(store *learning.Store) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.learningStore = store
}

// EStop broadcasts an emergency-stop to every in-flight run loop on
// this agent. The streaming/react paths select on the channel and
// abort within the next loop iteration (typically <1s for tool calls,
// instantaneous for token streaming). Idempotent — subsequent calls
// after the channel is closed are no-ops.
func (a *Agent) EStop() {
	if a == nil {
		return
	}
	a.stopMu.Lock()
	defer a.stopMu.Unlock()
	select {
	case <-a.stopCh:
		// already stopped; nothing to do
	default:
		close(a.stopCh)
	}
}

// Interrupt cancels the currently running stream for this agent by
// cancelling the per-stream context. Safe to call from any goroutine.
// No-op if no stream is running. Unlike EStop (which halts ALL future
// runs), Interrupt only cancels the in-flight turn — subsequent runs
// proceed normally.
func (a *Agent) Interrupt() {
	if a == nil {
		return
	}
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.streamCancel != nil {
		a.streamCancel()
		a.streamCancel = nil
	}
}

// StopCh exposes the estop channel for the streaming loop. Receiving
// on the returned channel signals "stop now"; the channel is closed
// on EStop.
func (a *Agent) StopCh() <-chan struct{} {
	if a == nil {
		// Return a never-closing channel for nil agents so callers
		// don't have to nil-check before selecting.
		return make(chan struct{})
	}
	a.stopMu.Lock()
	defer a.stopMu.Unlock()
	return a.stopCh
}

// ResetStop replaces the stop channel after an estop, so the agent
// can resume serving new runs. Called by Shutdown / Reconfigure paths
// when the wiring layer decides the agent is healthy again.
func (a *Agent) ResetStop() {
	if a == nil {
		return
	}
	a.stopMu.Lock()
	defer a.stopMu.Unlock()
	// Only replace if the current channel is closed — replacing an
	// open channel would leave any prior receivers blocked.
	select {
	case <-a.stopCh:
		a.stopCh = make(chan struct{})
	default:
		// still open, nothing to do
	}
}

// Receipts returns the cryptographic tool-call audit chain. Snapshot
// is a copy — safe for external persistence or display.
func (a *Agent) Receipts() []Receipt {
	if a == nil || a.receipts == nil {
		return nil
	}
	return a.receipts.Snapshot()
}

// RestoreHistory replaces the agent's in-memory history with the
// supplied messages. Used by flow resume to re-hydrate the conversation
// before continuing from a checkpoint. Caller owns the slice; we copy
// to avoid sharing the underlying array with the resume orchestrator.
func (a *Agent) RestoreHistory(history []providers.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = append([]providers.Message(nil), history...)
}

// FireSessionStart fires the on_session_start hook (master plan §6.3). Safe
// to call repeatedly; callers (cmd/overkill) typically fire once on agent boot.
func (a *Agent) FireSessionStart(ctx context.Context) {
	if a == nil || a.hooks == nil {
		return
	}
	_, _ = a.hooks.Fire(ctx, hooks.OnSessionStart, hooks.Event{
		Point:     hooks.OnSessionStart,
		SessionID: a.sessionID,
	})
}

// FireSessionEnd fires the on_session_end hook. Wired into the TUI's quit
// path so user-defined cleanup scripts get a chance to run.
func (a *Agent) FireSessionEnd(ctx context.Context) {
	if a == nil || a.hooks == nil {
		return
	}
	_, _ = a.hooks.Fire(ctx, hooks.OnSessionEnd, hooks.Event{
		Point:     hooks.OnSessionEnd,
		SessionID: a.sessionID,
	})
}

// HistoryCompactor abstracts the compaction strategy used by Agent.Compact.
// Tiny on purpose — keeps the agent free of the compaction package import.
type HistoryCompactor interface {
	Compact(ctx context.Context, msgs []providers.Message, model string, maxTokens int) (summary string, err error)
}

// SetCompactor installs a compaction strategy. When use is false the agent
// keeps its in-built ad-hoc compaction path even if c is non-nil — handy as a
// kill switch. Pass (nil, false) to revert to the legacy path.
func (a *Agent) SetCompactor(c HistoryCompactor, use bool) {
	if a == nil {
		return
	}
	if c == nil {
		a.compactor.Store(nil)
	} else {
		a.compactor.Store(&compactorBox{HistoryCompactor: c})
	}
	a.useCompactor.Store(use && c != nil)
}

// compactorBox wraps the HistoryCompactor interface so it can be stored
// atomically. Storing a bare interface in atomic.Pointer is impossible —
// atomic.Pointer wants a concrete pointer type.
type compactorBox struct{ HistoryCompactor }

// PromptRewriter is implemented by anything that can transform a user prompt
// before the agent ships it to the model. The agent itself does not depend on
// the rewriter package — a tiny interface here keeps the import graph clean.
type PromptRewriter interface {
	RewritePrompt(ctx context.Context, text string) (string, error)
}

// SetRewriter installs the prompt rewriter middleware. Pass nil to disable.
func (a *Agent) SetRewriter(r PromptRewriter) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rewriter = r
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

	// Session-scoped context so background goroutines (auto-compaction,
	// future async lifecycle work) can be cancelled on Shutdown without
	// having to plumb a ctx through every code path. Plain
	// context.Background() leaked goroutines when the TUI quit mid-Compact.
	sCtx, sCancel := context.WithCancel(context.Background())

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
		sessionCtx:      sCtx,
		sessionCancel:   sCancel,
		receipts:        NewReceiptChain(),
		stopCh:          make(chan struct{}),
	}
}

// Shutdown cancels the agent's session-scoped context so background
// goroutines (auto-compaction etc.) wind down. Safe to call multiple
// times. The TUI's quit defer should call this before FireSessionEnd to
// stop in-flight work cleanly.
func (a *Agent) Shutdown() {
	if a == nil || a.sessionCancel == nil {
		return
	}
	a.sessionCancel()
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
	// Preserve any previously-installed AlertSink + sessionID. Old code
	// replaced the whole ErrorRecovery, silently dropping a sink wired
	// by an earlier SetRecoveryAlertSink call. Wiring order in tui.go
	// (SetRecoveryAlertSink → SetRecoveryWriter) meant FireDeferralAlert
	// never fired in practice.
	var prevSink AlertSink
	var prevSessionID string
	if a.recovery != nil {
		prevSink = a.recovery.sink
		prevSessionID = a.recovery.sessionID
	}
	a.recovery = NewErrorRecovery(w)
	if prevSink != nil {
		a.recovery.SetAlertSink(prevSink, prevSessionID)
	}
}

// SetRecoveryAlertSink wires an AlertSink onto the active ErrorRecovery so
// emitRecovery fires AlertTaskDeferred. Safe to call multiple times.
func (a *Agent) SetRecoveryAlertSink(s AlertSink, sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.recovery == nil {
		a.recovery = NewErrorRecovery(nil)
	}
	a.recovery.SetAlertSink(s, sessionID)
}

// SetFlightRecorder wires the journal flight recorder (§4.19). When set, every
// user input, agent reply, and tool call is written to ~/.overkill/journal/raw/
// as append-only JSONL. Nil-safe — pass nil to disable flight recording.
func (a *Agent) SetFlightRecorder(fr *journal.FlightRecorder) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.flightRecorder = fr
}

// SetFeatureManager wires the feature-flag manager (P1). When set, the
// agent gates prompt sections behind feature flags. Pass nil to disable.
func (a *Agent) SetFeatureManager(fm *features.Manager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.featureManager = fm
}

// SetReadCache wires the speculative read cache (P2). When set, file-read
// tool paths check the cache before hitting disk. Pass nil to disable.
func (a *Agent) SetReadCache(c *speculative.ReadCache) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.readCache = c
}

// ReadCache returns the speculative read cache, or nil when unset.
func (a *Agent) ReadCache() *speculative.ReadCache {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.readCache
}

// SetExtensionsManager wires the extensions manager (P2). When set, the
// agent renders enabled extensions into the system prompt. Pass nil to disable.
func (a *Agent) SetExtensionsManager(em ExtensionsManager) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.extensionsManager = em
}

// SetCompletionEmitter wires the completion-event emitter (§8.7.2). When set,
// the emitter is called once at the end of every Run() with a populated
// CompletionEvent. Pass nil to disable. costTracker is optional; pass nil if
// per-session cost data is not available.
func (a *Agent) SetCompletionEmitter(e *events.Emitter, tracker cost.Tracker) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.completionEmitter = e
	a.costTracker = tracker
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
	runStart := time.Now()

	if obs := a.userInputObserver.Load(); obs != nil {
		func() {
			defer func() { _ = recover() }()
			obs.fn(userInput)
		}()
	}

	// §7.1 per-task complexity-based timeout: bound the rest of Run()
	// by an auto-scaled budget so simple tasks can't burn arbitrary
	// wall-clock. The caller's ctx still cancels first if shorter.
	a.mu.RLock()
	histLen := len(a.history)
	a.mu.RUnlock()
	taskBudget := taskTimeoutFor(userInput, histLen)
	taskCtx, taskCancel := context.WithTimeout(ctx, taskBudget)
	defer taskCancel()
	a.emit("task_timeout_armed", map[string]any{
		"timeout_ms": taskBudget.Milliseconds(),
		"session_id": a.sessionID,
	})
	ctx = taskCtx

	// §4.4 pre-compaction checkpoint: if utilization is in the
	// approaching-soft-trigger band (≈48–50%) AND the user just
	// queued a large task, compact NOW so the big task runs in a
	// fresh window. Best-effort — failures emit, never block.
	a.preCompactCheck(ctx, userInput)

	// Smart model routing (master plan §5.2): per-turn model selection
	// based on input complexity. Falls back silently to the static model.
	if box := a.modelRouter.Load(); box != nil {
		a.mu.RLock()
		hist := len(a.history)
		a.mu.RUnlock()
		snap := RouteSnapshot{
			UserInput:      userInput,
			HistoryLen:     hist,
			ToolCallCount:  0, // filled in by the live loop; first turn is 0
			HasAttachments: containsAtMention(userInput),
		}
		func() {
			defer func() { _ = recover() }()
			if id, reason, ok := box.PickModel(snap); ok && id != "" {
				prev := a.Model()
				a.SetModel(id)
				if prev != id {
					a.emit("model_routed", map[string]any{
						"from":   prev,
						"to":     id,
						"reason": reason,
					})
				}
			}
		}()
	}

	for _, scanner := range a.scanners {
		result, err := scanner.Scan(userInput)
		if err != nil {
			return nil, fmt.Errorf("agent: security scan failed: %w", err)
		}
		if result.Blocked {
			return &RunResult{
				Blocked:     true,
				BlockReason: fmt.Sprintf("blocked by %s: %s", scanner.Name(), result.Findings[0].Description),
				Model:       a.Model(),
			}, nil
		}
	}

	a.mu.Lock()
	a.history = append(a.history, providers.Message{
		Role:    "user",
		Content: userInput,
	})
	a.mu.Unlock()

	// §4.19 journal: record user input to flight recorder. Best-effort —
	// failure here never blocks the agent loop.
	a.recordFlight(journal.EntryUserInput, userInput)

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

	// §6.5 learning: inject relevant past corrections into conversation
	// so the model knows about user preferences before responding.
	a.injectLearningCorrections(userInput)

	result := &RunResult{
		Model: a.Model(),
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
			stepErr := fmt.Errorf("agent: step %d: %w", step+1, err)
			a.emitCompletion(ctx, userInput, "failure", runStart, []string{stepErr.Error()})
			return nil, stepErr
		}

		result.Steps++
		result.ToolCalls += len(stepResult.ToolCalls)
		result.TotalTokens += stepResult.Tokens.InputTokens + stepResult.Tokens.OutputTokens
		// Track per-session metrics for drift detection (P3).
		a.sessionMetrics.mu.Lock()
		a.sessionMetrics.toolCalls += len(stepResult.ToolCalls)
		a.sessionMetrics.turns++
		a.sessionMetrics.mu.Unlock()

		if obs := a.usageObserver.Load(); obs != nil && (stepResult.Tokens.InputTokens > 0 || stepResult.Tokens.OutputTokens > 0) {
			modelID := a.Model()
			func() {
				defer func() { _ = recover() }()
				obs.fn(modelID, stepResult.Tokens)
			}()
		}

		if stepResult.Done {
			result.Response = stepResult.Thought
			result.Confidence = a.assessTurnConfidence(userInput)
			// §6.2: if a prior Run failed with a known class and this one
			// completed cleanly, count it as a recovery. Self-learning's
			// "you've solved this 3 times" trigger fires from these.
			a.recordRecoverySuccess()
			// Track recoveries for drift detection (P3).
			a.sessionMetrics.mu.Lock()
			a.sessionMetrics.recoveries++
			a.sessionMetrics.mu.Unlock()
			// §6.5 learning: record a correction if the user's message
			// looks like one, so future turns benefit from the feedback.
			a.recordCorrectionIfNeeded(userInput, result.Response)
			a.emitCompletion(ctx, userInput, "success", runStart, nil)
			return result, nil
		}
	}

	maxStepsErr := fmt.Errorf("agent: exceeded max steps (%d)", a.maxSteps)
	a.emitCompletion(ctx, userInput, "failure", runStart, []string{maxStepsErr.Error()})
	return nil, maxStepsErr
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

	// Master plan §4.4 50% trigger: when ShouldCompact crosses (default 0.5),
	// trigger compaction proactively rather than waiting for the user to type
	// /compact. Best-effort — failure is logged via emit, never fatal.
	if report.ShouldCompact && a.useCompactor.Load() && a.compactor.Load() != nil && !a.compactionInFlight.Load() {
		a.compactionInFlight.Store(true)
		// Derive from sessionCtx (not Background()) so Shutdown cancels
		// in-flight auto-compaction instead of leaking the goroutine
		// past TUI exit. Fall back to Background only when sessionCtx
		// is nil (defensive — shouldn't happen with New()).
		parent := a.sessionCtx
		if parent == nil {
			parent = context.Background()
		}
		go func() {
			defer a.compactionInFlight.Store(false)
			defer func() { _ = recover() }()
			ctx, cancel := context.WithTimeout(parent, 60*time.Second)
			defer cancel()
			if res, err := a.Compact(ctx); err != nil {
				a.emit("auto_compact_failed", map[string]any{"error": err.Error()})
			} else if res != nil {
				a.emit("auto_compacted", map[string]any{
					"tokens_before": res.TokensBefore,
					"tokens_after":  res.TokensAfter,
				})
			}
		}()
	}
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
	if err := rec.RecordLesson(report); err != nil {
		// Lesson persistence failing silently meant the recovery system
		// could appear empty even when reports were being analysed. Fire
		// an event so the journal + telemetry catch it.
		a.emit("recovery_write_failed", map[string]any{
			"error":      err.Error(),
			"session_id": a.sessionID,
		})
	}
	rec.FireDeferralAlert(report)
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
	// Track errors for drift detection (P3).
	a.sessionMetrics.mu.Lock()
	a.sessionMetrics.errors++
	a.sessionMetrics.mu.Unlock()

	// §6.3 beat — first failure ever in this relationship. Recorder
	// dedups via the milestone map; this fires every call but only
	// the first-of-kind takes effect.
	a.recordBeat(BeatFirstFailure, stepErr.Error())

	// Master plan §4.13: auto-escalate the diagnostic ladder. We classify
	// the error, advance the per-class ladder, and emit a suggestion the
	// recovery report (or the human) can act on. Cheap — no LLM call, just
	// table lookup and counter increment.
	a.mu.Lock()
	if a.diagEscalator == nil {
		a.diagEscalator = newDiagnosticEscalator()
	}
	esc := a.diagEscalator
	a.mu.Unlock()
	sugg := esc.suggest(stepErr.Error())
	// Record the class so the NEXT successful Run() can fire a learning
	// signal ("the user / I recovered from a compile error"). Cleared by
	// recordRecoverySuccess on Done; overwritten by the next error.
	a.mu.Lock()
	a.lastErrorClass = sugg.Class
	a.mu.Unlock()
	a.emit("diagnostic_suggestion", map[string]any{
		"class":       sugg.Class,
		"tier":        sugg.Tier,
		"name":        sugg.Name,
		"description": sugg.Description,
		"command":     sugg.Command,
		"exhausted":   sugg.Exhausted,
		"session_id":  a.sessionID,
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
	rw := a.rewriter
	a.mu.RUnlock()

	// Rewriter middleware: pipe the most-recent user message through the
	// installed rewriter (if any). Errors and panics never block — fall back
	// to the original text. Mutations are confined to the local msgs slice
	// and pushed back into history so the conversation reflects what the
	// model actually saw.
	if rw != nil {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role != "user" {
				continue
			}
			func() {
				defer func() { _ = recover() }()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				rewritten, err := rw.RewritePrompt(ctx, msgs[i].Content)
				if err == nil && rewritten != "" && rewritten != msgs[i].Content {
					msgs[i].Content = rewritten
					a.mu.Lock()
					if i < len(a.history) && a.history[i].Role == "user" {
						a.history[i].Content = rewritten
					}
					a.mu.Unlock()
				}
			}()
			break
		}
	}

	prompt := BuildSystemPrompt(a.systemPrompt, a.toolRegistry)
	// Skills (master plan §6.4 wire-up): always-on skills plus trigger-matched
	// skills against the latest user message render into the prompt every turn.
	// No-op when no skill registry is installed.
	var latestUser string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			latestUser = msgs[i].Content
			break
		}
	}
	if skillBlock := a.renderSkillSection(latestUser); skillBlock != "" {
		prompt = prompt + "\n\n" + skillBlock
	}
	// Extensions (P2): render enabled extensions into the system prompt
	// so the model knows what plugins/skills/hooks are active.
	if extBlock := a.renderExtensionsSection(); extBlock != "" {
		prompt = prompt + "\n\n" + extBlock
	}
	// Memory recall (master plan §6.1): top-K retrieval against latest user
	// input. Slots between skills and personality so memory is framed as
	// reference data while identity directives keep the last word.
	if memBlock := a.renderMemorySection(context.Background(), latestUser); memBlock != "" {
		prompt = prompt + "\n\n" + memBlock
	}
	// Personality directive (§4.16): long-lived character/tone block. Comes
	// AFTER skills so skills can't be drowned by tone instructions, and BEFORE
	// per-turn context so factual data wins when it conflicts with vibes.
	if persona := a.personalitySection(); persona != "" {
		prompt = prompt + "\n\n" + persona
	}
	// Append plugin/host-supplied per-turn context (git status, jira ticket,
	// project conventions, etc.). The comment used to claim "the callback
	// is responsible for its own timeouts" — in practice nothing enforced
	// that, and a blocking plugin would stall every Run. Wrap in a 5s
	// budget so a misbehaving callback degrades to "no extra context" for
	// this turn instead of hanging the loop.
	pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
	if extra := a.providedContext(pctx); extra != "" {
		// Run plugin-supplied context through the same injection
		// scanner used on tool inputs. A compromised or malicious
		// plugin can otherwise inject "ignore previous instructions"
		// payloads straight into the system prompt and steer the
		// model. We redact-rather-than-drop so a noisy false positive
		// doesn't silently lose useful context.
		extra = scanInjection(a.scanners, extra)
		prompt = prompt + "\n\n" + extra
	}
	pcancel()
	// Caveman Mode (master plan §4.4): escalate bluntness as token budget
	// approaches the cap so the model voluntarily compresses its output.
	prompt = a.applyCaveman(prompt)
	// LLMLingua-style prompt compression (master plan §4.4): when budget
	// utilization is high, run the assembled prompt through a compressor
	// LLM call. Failures fall back to the original prompt silently.
	prompt = a.applyPromptCompression(prompt)

	return providers.Request{
		Model:        a.Model(),
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
			t, err := a.toolRegistry.Get(name)
			if err != nil || t == nil {
				// Registry.List returned a name the registry now refuses
				// to resolve — only happens if the registry was mutated
				// concurrently between List and Get. Skip the entry and
				// emit so the inconsistency is visible instead of
				// crashing on a nil t.Name() call below.
				a.emit("tool_registry_inconsistent", map[string]any{
					"name":  name,
					"error": fmt.Sprintf("%v", err),
				})
				continue
			}
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
	// Include skill section in token estimates so budget reports reflect the
	// actual prompt size the model will see.
	var latestUser string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			latestUser = msgs[i].Content
			break
		}
	}
	if skillBlock := a.renderSkillSection(latestUser); skillBlock != "" {
		systemPrompt = systemPrompt + "\n\n" + skillBlock
	}
	if memBlock := a.renderMemorySection(context.Background(), latestUser); memBlock != "" {
		systemPrompt = systemPrompt + "\n\n" + memBlock
	}
	if persona := a.personalitySection(); persona != "" {
		systemPrompt = systemPrompt + "\n\n" + persona
	}

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

	// Master plan §6.3: before/after compaction hooks.
	if a.hooks != nil {
		_, _ = a.hooks.Fire(ctx, hooks.BeforeCompaction, hooks.Event{
			Point:     hooks.BeforeCompaction,
			SessionID: a.sessionID,
			Metadata:  map[string]any{"messages": len(history)},
		})
		defer func(messagesBefore int) {
			_, _ = a.hooks.Fire(ctx, hooks.AfterCompaction, hooks.Event{
				Point:     hooks.AfterCompaction,
				SessionID: a.sessionID,
				Metadata:  map[string]any{"messages_before": messagesBefore},
			})
		}(len(history))
	}

	tokensBefore := 0
	if a.tokenizer != nil {
		for _, m := range history {
			tokensBefore += a.tokenizer.Estimate(m.Content) + 4
		}
	}

	var c HistoryCompactor
	if box := a.compactor.Load(); box != nil {
		c = box.HistoryCompactor
	}
	useC := a.useCompactor.Load()

	model := a.Model()
	var summary string
	if useC && c != nil {
		// Delegate to the LCM 3-level escalation compactor.
		s, err := c.Compact(ctx, history, model, a.maxTokens)
		if err != nil {
			return nil, fmt.Errorf("agent: compact (lcm): %w", err)
		}
		if s == "" {
			return nil, fmt.Errorf("agent: compact: lcm returned empty summary")
		}
		summary = s
	} else {
		// Legacy ad-hoc compact path (kept as a kill switch).
		compactPrompt := "Summarize the conversation above into a single concise paragraph. " +
			"Preserve: 1) key decisions made, 2) files changed and the nature of the changes, " +
			"3) outstanding tasks or open questions, 4) important context the user has shared. " +
			"Be specific. Do not greet or sign off. Output the summary text only."

		msgs := append([]providers.Message{}, history...)
		msgs = append(msgs, providers.Message{Role: "user", Content: compactPrompt})

		req := providers.Request{
			Model:        model,
			Messages:     msgs,
			MaxTokens:    a.maxTokens,
			SystemPrompt: "You produce dense factual summaries of prior conversation.",
		}

		resp, err := a.provider.Complete(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("agent: compact: %w", err)
		}

		summary = resp.Content
		if summary == "" {
			return nil, fmt.Errorf("agent: compact: provider returned empty summary")
		}
	}

	// §6.1 hot/cold paging: archive the evicted messages to cold
	// storage BEFORE we replace history. The summary becomes the hot
	// view; the raw text remains retrievable via memory_search.
	// Best-effort — failures are emitted, never abort compaction.
	a.archiveCompactedMessages(history)

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

// SetPersonalityProvider installs a callback that returns the personality
// directive appended to the system prompt each turn (§4.16). Pass nil to
// disable. The callback is recovered: a panic returns "" silently.
func (a *Agent) SetPersonalityProvider(fn func() string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.personalityProviderFn = fn
}

// personalitySection returns the directive block from the installed provider,
// or "" when none is wired. Always safe to call.
func (a *Agent) personalitySection() (out string) {
	a.mu.RLock()
	fn := a.personalityProviderFn
	a.mu.RUnlock()
	if fn == nil {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			out = ""
		}
	}()
	return fn()
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

// scanInjection runs the injection-class scanners over plugin-supplied
// text and returns the Sanitized result. Non-injection scanners are
// skipped (command scanners would block legitimate text). On any
// error or absence of scanners, the original string is returned —
// fail-open is the right call here because dropping useful project
// context to a false positive is worse than the alternative.
func scanInjection(scanners []security.Scanner, text string) string {
	for _, sc := range scanners {
		if sc.Name() != "injection" {
			continue
		}
		res, err := sc.Scan(text)
		if err == nil && res != nil && res.Sanitized != "" {
			return res.Sanitized
		}
	}
	return text
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

// SetPrivilegeGate wires a privilege gate that pre-checks every tool call.
// Pass nil to disable (default). When wired in ReaderMode, write-like calls
// return security.ErrWriteDenied without ever reaching the tool.
func (a *Agent) SetPrivilegeGate(g *security.PrivilegeGate) {
	a.privilege.Store(g)
}

// PrivilegeMode returns the gate's current mode, or empty when no gate is wired.
func (a *Agent) PrivilegeMode() security.PrivilegeMode {
	g := a.privilege.Load()
	if g == nil {
		return ""
	}
	return g.Mode()
}

// SetPrivilegeMode flips the gate's mode if a gate is wired. No-op otherwise.
func (a *Agent) SetPrivilegeMode(m security.PrivilegeMode) {
	if g := a.privilege.Load(); g != nil {
		g.SetMode(m)
	}
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
	} else if a.compressors != nil && len(output) > 0 {
		// Tool-output compression middleware (master plan §4.4 / RTK pattern).
		// Per-tool compressors trim large outputs before they hit history.
		// On error or no-op the registry returns the original payload so the
		// agent never silently drops data.
		if compressed, saved, err := a.compressors.Compress(toolName, output); err == nil && saved > 0 {
			content = string(compressed)
			a.emit("tool_compressed", map[string]any{
				"tool":           toolName,
				"bytes_saved":    saved,
				"bytes_original": len(output),
				"bytes_after":    len(compressed),
			})
		}
	}

	a.appendMessage(providers.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
	})
}

// emitCompletion fires the completion event if an emitter is wired. It is
// best-effort: errors are emitted on the event bus but never returned.
func (a *Agent) emitCompletion(ctx context.Context, intent, outcome string, startedAt time.Time, errs []string) {
	a.mu.RLock()
	emitter := a.completionEmitter
	tracker := a.costTracker
	sessionID := a.sessionID
	a.mu.RUnlock()

	if emitter == nil {
		return
	}

	var costUSD float64
	if tracker != nil {
		if summary, err := tracker.SessionCost(ctx, sessionID); err == nil {
			costUSD = summary.TotalUSD
		}
	}

	artefacts := a.collectArtefacts()

	evt := events.CompletionEvent{
		SessionID:  sessionID,
		Intent:     intent,
		Outcome:    outcome,
		Artefacts:  artefacts,
		DurationMs: time.Since(startedAt).Milliseconds(),
		CostUSD:    costUSD,
		Errors:     errs,
		EmittedAt:  time.Now(),
	}

	go func() {
		defer func() { _ = recover() }()
		emitCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := emitter.Emit(emitCtx, evt); err != nil {
			a.emit("completion_emit_failed", map[string]any{
				"error":      err.Error(),
				"session_id": sessionID,
			})
		}
	}()
}

// collectArtefacts inspects the current conversation history and extracts
// write-class tool calls as file artefacts. Lightweight — no LLM calls.
func (a *Agent) collectArtefacts() []events.Artefact {
	a.mu.RLock()
	hist := make([]providers.Message, len(a.history))
	copy(hist, a.history)
	a.mu.RUnlock()

	var artefacts []events.Artefact
	seen := make(map[string]struct{})

	for _, msg := range hist {
		for _, tc := range msg.ToolCalls {
			ref := extractArtefactRef(tc.Name, tc.Arguments)
			if ref == "" {
				continue
			}
			if _, dup := seen[ref]; dup {
				continue
			}
			seen[ref] = struct{}{}
			artefacts = append(artefacts, events.Artefact{
				Kind: artefactKind(tc.Name),
				Ref:  ref,
			})
		}
	}
	return artefacts
}

// extractArtefactRef pulls a meaningful reference (path, URL, SHA) out of
// tool call arguments for write-class tools. Returns "" for unknown tools.
func extractArtefactRef(toolName, args string) string {
	switch toolName {
	case "fs_write", "write_file", "edit_file", "patch":
		var a struct {
			Path string `json:"path"`
			File string `json:"file"`
		}
		_ = json.Unmarshal([]byte(args), &a)
		if a.Path != "" {
			return a.Path
		}
		return a.File
	case "fs_delete":
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal([]byte(args), &a)
		return a.Path
	case "git":
		var a struct {
			Args []string `json:"args"`
		}
		if err := json.Unmarshal([]byte(args), &a); err == nil {
			for i, arg := range a.Args {
				if arg == "commit" && i+1 < len(a.Args) {
					return "git:commit"
				}
			}
		}
	}
	return ""
}

func artefactKind(toolName string) string {
	switch toolName {
	case "fs_write", "write_file", "edit_file", "patch", "fs_delete":
		return "file"
	case "git":
		return "commit"
	}
	return "file"
}

// recordFlight appends a flight-recorder entry. Best-effort only — failures
// are silently dropped so the journal never blocks the agent loop (§4.19).
// Safe to call with a nil flightRecorder; nil receivers are no-ops.
func (a *Agent) recordFlight(entryType journal.EntryType, content string) {
	if a.flightRecorder == nil {
		return
	}
	// Recover from panics so a journal bug can't crash the agent.
	defer func() { _ = recover() }()
	_ = a.flightRecorder.Record(entryType, content, nil)
}

// injectLearningCorrections queries the learning store for past corrections
// relevant to the user's input and appends them as a system message so the
// model can adjust its response.
func (a *Agent) injectLearningCorrections(userInput string) {
	a.mu.RLock()
	store := a.learningStore
	a.mu.RUnlock()
	if store == nil {
		return
	}

	corrections, err := store.FindCorrections(userInput, 3)
	if err != nil || len(corrections) == 0 {
		return
	}

	prompt := learning.FormatPrompt(corrections)
	if prompt != "" {
		a.appendMessage(providers.Message{
			Role:    "system",
			Content: prompt,
		})
	}
}

// recordCorrectionIfNeeded checks whether the user's message is a correction
// and, if so, stores it in the learning store for future reference.
func (a *Agent) recordCorrectionIfNeeded(userInput, assistantResponse string) {
	a.mu.RLock()
	store := a.learningStore
	a.mu.RUnlock()
	if store == nil {
		return
	}

	if !learning.IsCorrection(userInput) {
		return
	}

	correct := learning.ExtractCorrect(userInput)
	if correct == "" {
		return
	}

	corr := learning.NewCorrection(userInput, assistantResponse, correct)
	if err := store.Save(corr); err != nil {
		a.emit("learning_save_failed", map[string]any{
			"error":      err.Error(),
			"session_id": a.sessionID,
		})
	}
}
