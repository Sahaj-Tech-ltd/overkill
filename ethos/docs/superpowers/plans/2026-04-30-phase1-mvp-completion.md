# Phase 1 MVP Completion Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete all remaining Phase 1 MVP items from the master plan — 18 features across 4 waves.

**Architecture:** Each wave groups independent features that can be built in parallel by separate agents. All features are new files or extensions to existing packages. No cross-wave dependencies.

**Tech Stack:** Go 1.22+, BadgerDB v4, existing internal packages (agent, providers, security, session, journal, personality, compaction, tools)

---

## Done (Wave 1 — already shipped)

| # | Feature | Files |
|---|---------|-------|
| 1 | Command completion marker | `internal/tools/shell.go` |
| 2 | EventBus (zero-dep) | `internal/agent/eventbus.go` |
| 3 | Error classifier (55 patterns) | `internal/providers/classifier.go` |
| 4 | Context budget pre-estimation | `internal/agent/budget.go`, `agent.go` |
| 5 | Permission escalation | `internal/security/permission.go`, `scanner.go` |
| 6 | Steering queue | `internal/agent/steering.go`, `react.go` |

---

## Wave 2: Agent Intelligence (4 features, independent)

### Task 1: Two-Step Forethought — Secondary Impact Check

**Files:**
- Create: `internal/agent/forethought.go`
- Create: `internal/agent/forethought_test.go`
- Modify: `internal/agent/react.go` — call Forethought before tool execution

**What:** Before executing a write tool (shell with write-like commands, fs write), calculate secondary impact: what files/directories are affected, what could break. If risk is high, inject a warning into the steering queue or modify the tool call to include a confirmation step.

**Implementation:**
- `type ImpactAssessment struct { AffectedPaths []string; RiskLevel ThreatLevel; Reasoning string }`
- `func AssessImpact(toolName string, input json.RawMessage, fileState *subagent.FileStateTracker) *ImpactAssessment`
- Checks: number of files affected, whether files are tracked in git, whether files are in a protected list (go.mod, go.sum, .github/)
- Low risk: proceed. Medium risk: log warning. High risk: inject steering message asking for confirmation.
- Integrate into `react.go:step()` between tool call resolution and execution

**Tests:**
- AssessImpact returns low for read commands
- AssessImpact returns high for `rm -rf` affecting many files
- AssessImpact detects protected file modification
- Integration: high-risk tool call gets steering warning injected

- [ ] Write tests
- [ ] Implement forethought.go
- [ ] Wire into react.go
- [ ] Run `go test -race ./internal/agent/ -v`

---

### Task 2: Spec-Driven Mode — Auto-Switch to Plan Creation

**Files:**
- Create: `internal/agent/specdriver.go`
- Create: `internal/agent/specdriver_test.go`
- Modify: `internal/agent/agent.go` — add specDriver field

**What:** When user input is detected as a complex multi-step task, auto-switch to plan creation mode. The agent creates a structured spec before executing.

**Implementation:**
- `type SpecDriver struct { enabled bool; complexityThreshold float64; classifier *routing.ComplexityClassifier }`
- `func (sd *SpecDriver) ShouldSpec(userInput string, history []providers.Message) bool` — uses routing classifier score > threshold (default 0.7)
- `func (sd *SpecDriver) BuildSpecPrompt(userInput string) string` — returns a prompt that asks the agent to create a spec before coding
- When ShouldSpec returns true, Agent.Run() injects a system message: "This is a complex task. Create a structured plan first: 1) Problem, 2) Approach, 3) Files to modify, 4) Test plan, 5) Edge cases. Then execute."
- Add `SpecDriver *SpecDriver` to Agent struct and Config

**Tests:**
- ShouldSpec returns false for simple queries ("what is 2+2")
- ShouldSpec returns true for complex tasks ("refactor the entire auth module")
- BuildSpecPrompt produces structured prompt
- Complex input causes spec injection in agent loop

- [ ] Write tests
- [ ] Implement specdriver.go
- [ ] Wire into agent.go Run()
- [ ] Run `go test -race ./internal/agent/ -v`

---

### Task 3: Tool Output Compression Middleware

**Files:**
- Create: `internal/tools/compressor.go`
- Create: `internal/tools/compressor_test.go`
- Modify: `internal/tools/tool.go` — add Compressor interface
- Modify: `internal/agent/react.go` — compress tool output before appending to history

**What:** Each tool registers an output compressor that reduces token count before it enters context. 60-90% savings on tool output.

**Implementation:**
```go
type Compressor interface {
    ToolName() string
    Compress(output json.RawMessage) (json.RawMessage, int, error)
}

type CompressorRegistry struct {
    compressors map[string]Compressor
    passthrough PassthroughCompressor
}
```
- Built-in compressors:
  - `ShellCompressor` — strips ANSI (already done in shell.go), extracts last N lines for errors, stats extraction for git commands
  - `GrepCompressor` — limits to first 50 matches, adds "... N more matches"
  - `GitCompressor` — stat-only for diff/log (+142/-89), full for status
  - `PassthroughCompressor` — identity compressor for unregistered tools
- Each Compress returns (compressed_output, tokens_saved, error)
- Fail-open: if compressor errors, use raw output
- Add `Compressors *CompressorRegistry` to Agent struct
- In react.go, after `executeTool`, compress output before `appendToolResultMessage`

**Tests:**
- ShellCompressor reduces verbose output
- GrepCompressor limits match count
- GitCompressor extracts stats from diff
- Passthrough returns raw output unchanged
- Failed compressor falls back to raw (fail-open)
- CompressorRegistry dispatches to correct compressor

- [ ] Write tests
- [ ] Implement compressor.go with all 4 built-in compressors
- [ ] Wire into react.go
- [ ] Run `go test -race ./internal/tools/ -v`

---

### Task 4: LLM-Based Prompt Compression (LLMLingua-style)

**Files:**
- Create: `internal/compaction/prompt_compress.go`
- Create: `internal/compaction/prompt_compress_test.go`

**What:** Before sending assembled prompt to expensive model, a cheap model strips low-salience tokens. Runs AFTER context assembly but BEFORE provider call.

**Implementation:**
```go
type PromptCompressor struct {
    provider   providers.Provider
    model      string
    minSavingRatio float64  // skip if savings < cost (default 0.3)
}

type CompressionResult struct {
    OriginalTokens  int
    CompressedTokens int
    Ratio           float64
    Skipped         bool
}
```
- `func (pc *PromptCompressor) Compress(ctx context.Context, prompt string) (string, *CompressionResult, error)`
- Sends prompt to cheap model with system instruction: "Strip filler words and redundancy from this prompt while preserving all factual content, code, and instructions. Return the compressed version."
- Compares token count before/after. If savings < minSavingRatio, use original.
- Budget-aware: if context is above soft threshold, prompt compression kicks in before escalation.
- Configurable: enable/disable, model choice, minSavingRatio

**Tests:**
- Compress reduces token count
- Compression skipped when saving ratio too low
- Compression skipped when disabled
- Original preserved on provider error (fail-open)
- CompressionResult tracks accurate metrics

- [ ] Write tests
- [ ] Implement prompt_compress.go
- [ ] Run `go test -race ./internal/compaction/ -v`

---

## Wave 3: Durability & Journal (3 features, independent)

### Task 5: BadgerDB Durability — Snapshots, Export, Graceful Degradation

**Files:**
- Create: `internal/session/snapshot.go`
- Create: `internal/session/snapshot_test.go`
- Create: `internal/session/export.go`
- Create: `internal/session/export_test.go`
- Modify: `internal/session/badger.go` — add Snapshot, Restore, IntegrityCheck methods

**What:** BadgerDB has built-in snapshot support. Auto daily snapshot, export ritual on session exit, graceful degradation on corruption.

**Implementation:**
- `func (s *BadgerStore) Snapshot(ctx context.Context, dir string) error` — uses badger DB.Backup()
- `func (s *BadgerStore) Restore(ctx context.Context, snapshotPath string) error` — uses badger DB.Load()
- `func (s *BadgerStore) IntegrityCheck() error` — checks DB health
- `type SnapshotManager struct { store *BadgerStore; dir string; maxSnapshots int }` — manages rolling snapshots (default 7)
- `func (sm *SnapshotManager) DailySnapshot(ctx context.Context) error` — create snapshot, prune old ones
- `func (sm *SnapshotManager) LatestSnapshot() (string, error)` — find most recent
- `type ExportRitual struct { store *BadgerStore; exportPath string }`
- `func (er *ExportRitual) Export(ctx context.Context, relationship *personality.RelationshipTracker) error` — writes memory-export.md
- Boot detection: if DB open fails, check for snapshots, offer restore

**Tests:**
- Snapshot creates file
- Restore from snapshot recovers data
- Rolling snapshots prune old ones
- IntegrityCheck detects corruption (hard to test, mock if needed)
- ExportRitual writes markdown file
- DailySnapshot is idempotent

- [ ] Write tests
- [ ] Implement snapshot.go, export.go
- [ ] Add methods to badger.go
- [ ] Run `go test -race ./internal/session/ -v`

---

### Task 6: Journal Query Protocol — 3-Layer Progressive Disclosure

**Files:**
- Create: `internal/journal/query.go`
- Create: `internal/journal/query_test.go`
- Create: `internal/journal/observation.go`
- Create: `internal/journal/observation_test.go`

**What:** Make the journal queryable mid-session. 3-layer progressive disclosure, structured observation types, content-hash dedup.

**Implementation:**
```go
type Observation struct {
    ID           string         `json:"id"`
    Type         ObservationType `json:"type"`
    Title        string         `json:"title"`
    Narrative    string         `json:"narrative"`
    Facts        []string       `json:"facts"`
    Concepts     []string       `json:"concepts"`
    FilesRead    []string       `json:"files_read"`
    FilesModified []string      `json:"files_modified"`
    SessionID    string         `json:"session_id"`
    Timestamp    time.Time      `json:"timestamp"`
    ContentHash  string         `json:"content_hash"`
}

type ObservationType string
const (
    ObsBugfix ObservationType = "bugfix"
    ObsFeature ObservationType = "feature"
    ObsDecision ObservationType = "decision"
    ObsDiscovery ObservationType = "discovery"
    ObsChange ObservationType = "change"
    ObsRefactor ObservationType = "refactor"
)
```
- `type JournalQuery struct { db *badger.DB }` (or file-based)
- `func (jq *JournalQuery) Search(query string, obsType ObservationType, limit int) []ObservationIndex` — Layer 1: compact index (~50 tokens per result)
- `func (jq *JournalQuery) Timeline(anchorID string, depth int) []Observation` — Layer 2: chronological context
- `func (jq *JournalQuery) Get(id string) (*Observation, error)` — Layer 3: full detail
- Content hash: `SHA256(session_id + title + narrative)[:16]`
- Dedup: same hash = same observation, stored once

**Tests:**
- Search returns compact index
- Timeline returns chronological context
- Get returns full observation
- Content hash dedup prevents double storage
- Search filters by type
- Empty query returns recent

- [ ] Write tests
- [ ] Implement query.go, observation.go
- [ ] Run `go test -race ./internal/journal/ -v`

---

### Task 7: Git Push Preview + Secret Scanning Before Push

**Files:**
- Create: `internal/tools/gitpreview.go`
- Create: `internal/tools/gitpreview_test.go`

**What:** Fancy ASCII preview of what will be pushed, plus secret scanning. Prevents credential leaks.

**Implementation:**
```go
type PushPreview struct {
    Commits    []CommitInfo
    Files      []FileDiff
    HasSecrets bool
    SecretHits []SecretHit
}

type CommitInfo struct {
    Hash    string
    Message string
    Author  string
    Date    string
}

type FileDiff struct {
    Path     string
    Status   string  // added, modified, deleted
    Additions int
    Deletions int
}

type SecretHit struct {
    File    string
    Line    int
    Pattern string
    Preview string
}
```
- `func GeneratePushPreview(ctx context.Context, workDir string) (*PushPreview, error)` — parses `git log`, `git diff --stat`
- `func ScanForSecrets(ctx context.Context, workDir string) ([]SecretHit, error)` — runs secret patterns from security/secrets.go
- `func FormatPreview(preview *PushPreview) string` — ASCII formatted output
- Secret patterns: API keys, bearer tokens, private keys, .env contents, `sk-*`, AWS keys, database URLs with passwords

**Tests:**
- GeneratePushPreview parses git log output
- ScanForSecrets detects API keys
- ScanForSecrets detects bearer tokens
- FormatPreview produces readable ASCII
- Empty repo returns empty preview

- [ ] Write tests
- [ ] Implement gitpreview.go
- [ ] Run `go test -race ./internal/tools/ -v`

---

## Wave 4: Personality Deep Cuts (6 features, independent)

### Task 8: Working Style Inference

**Files:**
- Create: `internal/personality/style.go`
- Create: `internal/personality/style_test.go`

**What:** Infer how the user works from interaction patterns. 5 dimensions: communication style, response expectation, frustration patterns, working style, domain language.

**Implementation:**
```go
type WorkingStyle struct {
    Communication   CommunicationStyle `json:"communication"`    // direct, verbose, contextual
    ResponseExpect  ResponseStyle      `json:"response_expect"`  // synthesis, critique, action
    FrustrationTrigger string          `json:"frustration_trigger"` // pattern
    Approach        ApproachStyle      `json:"approach"`         // plans_first, dive_in
    DomainTerms     []string           `json:"domain_terms"`     // project-specific vocab
}

type StyleInferencer struct {
    baseline    *WorkingStyle
    shortTerm   *WorkingStyle
    sessionCount int
    sessionsForBaseline int  // default 5
}
```
- `func NewStyleInferencer() *StyleInferencer`
- `func (si *StyleInferencer) Observe(userInput string)` — updates short-term state
- `func (si *StyleInferencer) Baseline() *WorkingStyle` — long-term (slow update)
- `func (si *StyleInferencer) Current() *WorkingStyle` — short-term (fast update)
- `func (si *StyleInferencer) ShouldUpdateBaseline() bool` — after N consistent sessions
- Inference heuristics: message length → verbosity. Question marks → critique. Imperatives → direct. Explanations → contextual.

**Tests:**
- Observe with short messages infers terse communication
- Observe with questions infers critique response
- Baseline doesn't update after 1 session
- Baseline updates after N consistent sessions
- Short-term updates immediately

- [ ] Write tests
- [ ] Implement style.go
- [ ] Run `go test -race ./internal/personality/ -v`

---

### Task 9: Proactive Transparency — Pre-Execution Failure Warnings

**Files:**
- Create: `internal/personality/transparency.go`
- Create: `internal/personality/transparency_test.go`

**What:** Before executing a task, check failure history against the task type. If prior failures exist, surface a warning unprompted.

**Implementation:**
```go
type FailureRecord struct {
    TaskType    string    `json:"task_type"`
    Model       string    `json:"model"`
    Count       int       `json:"count"`
    LastFailed  time.Time `json:"last_failed"`
    Pattern     string    `json:"pattern"`
}

type TransparencyEngine struct {
    failures    []FailureRecord
    maxWarnings int  // per session, default 1
    warned      int
    currentModel string
}
```
- `func NewTransparencyEngine(model string) *TransparencyEngine`
- `func (te *TransparencyEngine) RecordFailure(taskType, model string)`
- `func (te *TransparencyEngine) Check(taskType string) (string, bool)` — returns warning message and whether to warn
- `func (te *TransparencyEngine) LoadFromJournal(entries []journal.Entry)` — populate from journal history
- Rate-limited: max 1 warning per session. It's a heads-up, not a personality.

**Tests:**
- Check returns empty when no failure history
- Check returns warning after recording failures
- Check returns empty after maxWarnings reached
- FailureRecord is model-versioned (different model = no warning)
- LoadFromJournal populates failure history

- [ ] Write tests
- [ ] Implement transparency.go
- [ ] Run `go test -race ./internal/personality/ -v`

---

### Task 10: Cognitive Blind Spot Detection

**Files:**
- Create: `internal/personality/blindspot.go`
- Create: `internal/personality/blindspot_test.go`

**What:** Detect when user reaches for the same solution class repeatedly. One-line surface, rate-limited, undeniable pattern threshold.

**Implementation:**
```go
type BlindSpotDetector struct {
    patterns    map[string]int  // solution_class -> count
    threshold   int             // default 4
    alerted     map[string]bool // already alerted
    maxAlerts   int             // per session, default 1
    alertCount  int
}
```
- `func NewBlindSpotDetector() *BlindSpotDetector`
- `func (bsd *BlindSpotDetector) Observe(taskType string)` — increment pattern counter
- `func (bsd *BlindSpotDetector) Check() (string, bool)` — returns observation line if pattern is undeniable
- `func (bsd *BlindSpotDetector) LoadFromJournal(entries []journal.Entry)` — seed from historical data

**Tests:**
- Check returns empty below threshold
- Check returns observation at threshold
- Check deduplicates (same pattern alerts once)
- Check respects maxAlerts
- LoadFromJournal seeds pattern counts

- [ ] Write tests
- [ ] Implement blindspot.go
- [ ] Run `go test -race ./internal/personality/ -v`

---

### Task 11: Model Fingerprinting — Detect Model Swap, Recalibrate

**Files:**
- Create: `internal/personality/fingerprint.go`
- Create: `internal/personality/fingerprint_test.go`

**What:** Detect when underlying model family/version changes. Trigger capability recalibration. Version failure history by model.

**Implementation:**
```go
type ModelFingerprint struct {
    Family      string    `json:"family"`
    Version     string    `json:"version"`
    ContextWindow int     `json:"context_window"`
    DetectedAt  time.Time `json:"detected_at"`
}

type FingerprintTracker struct {
    current    *ModelFingerprint
    previous   *ModelFingerprint
    changed    bool
}
```
- `func NewFingerprintTracker() *FingerprintTracker`
- `func (ft *FingerprintTracker) Detect(modelID string) *ModelFingerprint` — extract family from model ID (e.g., "claude-3-opus" → family "claude-opus")
- `func (ft *FingerprintTracker) HasChanged(newModelID string) bool` — compare family with previous
- `func (ft *FingerprintTracker) CalibratePrompt() string` — "Model changed since last session. Running quick calibration."
- `func (ft *FingerprintTracker) Update(fp *ModelFingerprint)` — set current fingerprint

**Tests:**
- Detect extracts family from model ID
- HasChanged returns true on family change
- HasChanged returns false on same family
- CalibratePrompt returns calibration message
- Update stores fingerprint

- [ ] Write tests
- [ ] Implement fingerprint.go
- [ ] Run `go test -race ./internal/personality/ -v`

---

### Task 12: Confidence Scores

**Files:**
- Create: `internal/agent/confidence.go`
- Create: `internal/agent/confidence_test.go`

**What:** Attach confidence scores to agent outputs. When uncertain, signal low confidence honestly.

**Implementation:**
```go
type ConfidenceLevel int
const (
    ConfidenceHigh ConfidenceLevel = iota
    ConfidenceMedium
    ConfidenceLow
    ConfidenceUnknown
)

type ConfidenceAssessment struct {
    Level      ConfidenceLevel
    Score      float64  // 0-1
    Reasoning  string
    TaskType   string
}
```
- `func AssessConfidence(taskType string, history []providers.Message, model string) *ConfidenceAssessment`
- Heuristics: task has been done before → high. Novel combination → medium. No reference in context → low.
- `func FormatConfidence(ca *ConfidenceAssessment) string` — "I'm about 70% confident on this."
- Integrate into RunResult: add Confidence field

**Tests:**
- AssessConfidence returns high for familiar task types
- AssessConfidence returns low for novel tasks
- FormatConfidence produces readable output
- Unknown model returns ConfidenceUnknown

- [ ] Write tests
- [ ] Implement confidence.go
- [ ] Run `go test -race ./internal/agent/ -v`

---

### Task 13: Cold Start Protocol

**Files:**
- Create: `internal/personality/coldstart.go`
- Create: `internal/personality/coldstart_test.go`

**What:** First-session detection and intake. One opening question, infer 5 dimensions from the response.

**Implementation:**
```go
type ColdStartState int
const (
    ColdStartUnknown ColdStartState = iota
    ColdStartPending
    ColdStartComplete
)

type ColdStartProfile struct {
    CommunicationStyle string
    VerbosityPreference string
    TechnicalDepth     string
    ToneTolerance      string
    UrgencyBaseline    string
    UserName           string
    Timezone           string
}

type ColdStartProtocol struct {
    state   ColdStartState
    profile *ColdStartProfile
}
```
- `func NewColdStartProtocol() *ColdStartProtocol`
- `func (csp *ColdStartProtocol) IsColdStart(relationshipFile string) bool` — checks if relationship data exists
- `func (csp *ColdStartProtocol) OpeningQuestion() string` — "I don't know you yet. What are you working on right now?"
- `func (csp *ColdStartProtocol) ProcessResponse(response string) *ColdStartProfile` — infer 5 dimensions from HOW they answer
- Inference heuristics: response length → verbosity. Technical terms → depth. Directness → communication style. Urgency words → urgency baseline.
- `func (csp *ColdStartProfile) ToRelationshipArc() map[string]string` — convert to relationship arc initial data

**Tests:**
- IsColdStart returns true when no relationship file
- OpeningQuestion returns non-empty string
- ProcessResponse infers terse from short response
- ProcessResponse infers verbose from long response
- ProcessResponse infers technical depth from technical terms
- ToRelationshipArc produces valid map

- [ ] Write tests
- [ ] Implement coldstart.go
- [ ] Run `go test -race ./internal/personality/ -v`

---

## Wave 5: Quality & Architecture (4 features, independent)

### Task 14: Independent Test Agent (Spider-Man Solution)

**Files:**
- Create: `internal/agent/testagent.go`
- Create: `internal/agent/testagent_test.go`

**What:** Separate agent that sees the spec, not the conversation. Writes tests independently from the implementation.

**Implementation:**
```go
type TestAgent struct {
    provider  providers.Provider
    model     string
    tokenizer *tokenizer.Estimator
}

type TestSpec struct {
    Description string
    FilesToTest []string
    SpecContent string  // the spec, NOT the conversation
    Language    string
}
```
- `func NewTestAgent(provider providers.Provider, model string) *TestAgent`
- `func (ta *TestAgent) GenerateTests(ctx context.Context, spec TestSpec) (string, error)` — generates tests from spec only
- `func (ta *TestAgent) ValidateTests(ctx context.Context, testCode string, implFiles []string) (string, error)` — checks tests against actual files
- The test agent never sees the conversation history or implementation intent. It sees: spec description + file paths + language.
- System prompt: "You are a test engineer. You have a spec and file paths. Write tests that verify the spec. You do NOT know how the code was implemented."

**Tests:**
- GenerateTests produces test code from spec
- ValidateTests catches spec violations
- TestAgent uses different model than main agent
- TestSpec excludes conversation history

- [ ] Write tests
- [ ] Implement testagent.go
- [ ] Run `go test -race ./internal/agent/ -v`

---

### Task 15: Vertical Slice Decomposition + PRD Template

**Files:**
- Create: `internal/pipeline/slicer.go`
- Create: `internal/pipeline/slicer_test.go`

**What:** Break plans into tracer-bullet issues cutting through ALL layers. HITL/AFK classification. Dependency-first publishing.

**Implementation:**
```go
type Slice struct {
    ID          string
    Title       string
    Description string
    Layers      []string  // schema → API → UI → tests
    Classification SliceClass // HITL or AFK
    Dependencies []string // slice IDs this depends on
    Priority    int
}

type SliceClass string
const (
    ClassHITL SliceClass = "HITL"  // needs human interaction
    ClassAFK  SliceClass = "AFK"   // agent can implement independently
)

type PRD struct {
    ProblemStatement string
    Solution         string
    UserStories      []UserStory
    ImplDecisions    []string
    TestDecisions    []string
    OutOfScope       []string
}

type UserStory struct {
    Actor      string
    Want       string
    Benefit    string
}
```
- `func DecomposeIntoSlices(spec string) ([]Slice, error)` — parse spec into vertical slices
- `func TopologicalSort(slices []Slice) ([]Slice, error)` — dependency-first ordering
- `func GeneratePRD(problem, solution string, stories []UserStory) *PRD`
- `func FormatSlice(slice Slice) string` — formatted output
- `func FormatPRD(prd *PRD) string` — formatted output

**Tests:**
- DecomposeIntoSlices produces vertical slices
- TopologicalSort orders by dependencies
- HITL/AFK classification works
- GeneratePRD produces complete PRD
- FormatSlice produces readable output

- [ ] Write tests
- [ ] Implement slicer.go
- [ ] Run `go test -race ./internal/pipeline/ -v`

---

### Task 16: Self-Aware Error Recovery

**Files:**
- Create: `internal/agent/recovery.go`
- Create: `internal/agent/recovery_test.go`

**What:** When agent makes a mistake, trace the bug chain, create a learning plan, and update the journal so it doesn't repeat.

**Implementation:**
```go
type ErrorRecovery struct {
    journal JournalWriter
}

type RecoveryReport struct {
    WhatWentWrong   string
    RootCause       string
    FaultChain      []string
    LearningPlan    string
    ImmediateFix    string
    Apology         string
}

type JournalWriter interface {
    WriteEntry(ctx context.Context, entry journal.Entry) error
}
```
- `func NewErrorRecovery(jw JournalWriter) *ErrorRecovery`
- `func (er *ErrorRecovery) Analyze(err error, history []providers.Message) *RecoveryReport`
- `func (er *ErrorRecovery) FormatRecovery(report *RecoveryReport) string` — honest apology + plan
- Analyze looks at: the error, recent tool calls, recent decisions, identifies where the reasoning went wrong
- The apology format: "Here's what I did wrong: X. Root cause: Y. My plan to not repeat: Z. What I can do right now: W."
- Write learning to journal for future sessions

**Tests:**
- Analyze produces recovery report from error
- FormatRecovery includes apology and plan
- FaultChain traces the error path
- Empty history still produces report

- [ ] Write tests
- [ ] Implement recovery.go
- [ ] Run `go test -race ./internal/agent/ -v`

---

### Task 17: Provider Selection UI (ZeroClaw-style)

**Files:**
- Create: `internal/config/setup.go`
- Create: `internal/config/setup_test.go`

**What:** First-run setup wizard. User selects provider → lists options → configures endpoint → tests connection.

**Implementation:**
```go
type SetupWizard struct {
    config  *Config
    steps   []SetupStep
}

type SetupStep struct {
    ID       string
    Prompt   string
    Options  []string
    Default  string
    Validate func(string) error
}

type ProviderSetup struct {
    Name         string
    APIKeyEnv    string
    DefaultBase  string
    AltEndpoints []string
    Models       []string
}
```
- `func NewSetupWizard(cfg *Config) *SetupWizard`
- `func (sw *SetupWizard) Run(ctx context.Context) error` — executes setup steps
- `func (sw *SetupWizard) ProviderSteps(provider string) []SetupStep` — provider-specific steps
- Pre-configured providers: OpenAI, Anthropic, Gemini, DeepSeek, Ollama, OpenRouter
- Each provider has: API key prompt, base URL (with default), model selection
- `func TestConnection(ctx context.Context, provider string, apiKey string, baseURL string) error` — validates setup

**Tests:**
- NewSetupWizard creates wizard with steps
- ProviderSteps returns correct steps for each provider
- TestConnection validates API key format
- Default endpoints are correct per provider
- SetupSteps validate input

- [ ] Write tests
- [ ] Implement setup.go
- [ ] Run `go test -race ./internal/config/ -v`

---

### Task 18: Model Catalog as TOML (models.dev integration)

**Files:**
- Create: `internal/providers/catalog.go`
- Create: `internal/providers/catalog_test.go`
- Create: `internal/providers/toml_test.go`
- Modify: `internal/providers/types.go` — expand Model struct

**What:** Replace hardcoded Go model slices with TOML model catalog. Filename-as-ID, `extends` inheritance, capability flags.

**Implementation:**
```go
type TOMLModel struct {
    Name           string            `toml:"name"`
    Family         string            `toml:"family"`
    MaxTokens      int               `toml:"max_tokens"`
    Reasoning      bool              `toml:"reasoning"`
    ToolCall       bool              `toml:"tool_call"`
    StructuredOutput bool            `toml:"structured_output"`
    Temperature    bool              `toml:"temperature"`
    Attachment     bool              `toml:"attachment"`
    OpenWeights    bool              `toml:"open_weights"`
    Modalities     TOMLModalities    `toml:"modalities"`
    Cost           TOMLCost          `toml:"cost"`
    Extends        *TOMLExtends      `toml:"extends"`
}

type TOMLModalities struct {
    Input  []string `toml:"input"`
    Output []string `toml:"output"`
}

type TOMLCost struct {
    Input      float64 `toml:"input"`
    Output     float64 `toml:"output"`
    CacheRead  float64 `toml:"cache_read"`
    CacheWrite float64 `toml:"cache_write"`
}

type TOMLExtends struct {
    From string `toml:"from"`
}

type ModelCatalog struct {
    models map[string]*TOMLModel
}
```
- `func LoadCatalog(dir string) (*ModelCatalog, error)` — load all TOML files from dir
- `func (mc *ModelCatalog) Get(id string) (*providers.Model, error)` — resolve model (follow extends chain)
- `func (mc *ModelCatalog) List() []string` — all model IDs
- `func (mc *ModelCatalog) ByFamily(family string) []*providers.Model` — family-aware lookup
- `func (mc *ModelCatalog) ByCapability(requires ToolCall, requiresVision bool) []*providers.Model` — capability filtering
- Filename-as-ID: `models/openai/gpt-4o.toml` → ID `openai/gpt-4o`
- Extends: wrapper models inherit from canonical, override cost
- Expand providers.Model struct with new fields: Family, Reasoning, StructuredOutput, etc.

**Tests:**
- LoadCatalog parses TOML directory
- Get resolves simple model
- Get resolves extends chain
- ByFamily returns correct models
- ByCapability filters by boolean flags
- Invalid TOML returns error
- Circular extends returns error

- [ ] Write tests
- [ ] Implement catalog.go
- [ ] Expand Model struct in types.go
- [ ] Run `go test -race ./internal/providers/ -v`

---

## Execution Order

1. **Wave 2** (4 agents in parallel): Tasks 1-4
2. **Wave 3** (3 agents in parallel): Tasks 5-7
3. **Wave 4** (6 agents in parallel): Tasks 8-13
4. **Wave 5** (4 agents in parallel): Tasks 14-17 + Task 18

Total: 18 features, 4 waves, ~36 new files, ~54 new test files
