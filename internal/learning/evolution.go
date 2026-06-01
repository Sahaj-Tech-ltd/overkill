// Package learning's evolution.go implements PicoClaw's self-improvement
// pipeline: hot-path recording → cold-path pattern clustering → draft
// generation → review (with secret scanning) → apply (with rollback).
//
// Stolen from PicoClaw's pkg/evolution/. Wires into Overkill's existing
// hooks system (§6.3) and self-learning loop (§6.2).
package learning

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ── LearningRecord (hot-path capture) ───────────────────────────────

// LearningRecord captures one agent turn for later pattern clustering.
// Written synchronously after every turn into JSONL.
type LearningRecord struct {
	TaskHash        string            `json:"taskHash"` // SHA256 of user input
	ToolExecutions  []ToolExecution   `json:"toolExecutions"`
	AttemptTrail    []AttemptSnapshot `json:"attemptTrail"` // what was tried
	FinalOutput     string            `json:"finalOutput"`
	WinningPath     []string          `json:"winningPath"`     // tool names that succeeded
	LateAddedSkills []string          `json:"lateAddedSkills"` // skills added mid-task
	MaturityScore   float64           `json:"maturityScore"`
	SuccessRate     float64           `json:"successRate"`
	ClusterReason   string            `json:"clusterReason,omitempty"`
	EventCount      int               `json:"eventCount"`
	SessionID       string            `json:"sessionId"`
	ModelID         string            `json:"modelId"`
	RecordedAt      time.Time         `json:"recordedAt"`
}

// ToolExecution records one tool call + result.
type ToolExecution struct {
	ToolName string `json:"toolName"`
	Args     string `json:"args"`
	Result   string `json:"result"`
	Duration int64  `json:"durationMs"`
	Error    string `json:"error,omitempty"`
}

// AttemptSnapshot captures state at each retry attempt.
type AttemptSnapshot struct {
	Attempt int      `json:"attempt"`
	Skills  []string `json:"skills"` // active skills at this point
	Path    []string `json:"path"`   // tool names tried so far
	Error   string   `json:"error,omitempty"`
}

// ── SkillDraft (cold-path generation) ────────────────────────────────

// SkillDraft is an AI-proposed skill change.
type SkillDraft struct {
	TargetSkillName string      `json:"targetSkillName"`
	DraftType       DraftType   `json:"draftType"`
	ChangeKind      ChangeKind  `json:"changeKind"`
	BodyOrPatch     string      `json:"bodyOrPatch"`
	Status          DraftStatus `json:"status"`
	ReviewNotes     string      `json:"reviewNotes,omitempty"`
	ScanFindings    []string    `json:"scanFindings,omitempty"`
	GeneratedAt     time.Time   `json:"generatedAt"`
}

type DraftType string

const (
	DraftWorkflow DraftType = "workflow"
	DraftShortcut DraftType = "shortcut"
)

type ChangeKind string

const (
	ChangeCreate  ChangeKind = "create"
	ChangeAppend  ChangeKind = "append"
	ChangeReplace ChangeKind = "replace"
	ChangeMerge   ChangeKind = "merge"
)

type DraftStatus string

const (
	DraftCandidate   DraftStatus = "candidate"
	DraftQuarantined DraftStatus = "quarantined"
	DraftAccepted    DraftStatus = "accepted"
	DraftRejected    DraftStatus = "rejected"
)

// ── Skill Lifecycle ──────────────────────────────────────────────────

// SkillProfile tracks a skill's usage and retention across sessions.
type SkillProfile struct {
	Name           string         `json:"name"`
	RetentionScore float64        `json:"retentionScore"`
	UseCount       int            `json:"useCount"`
	LastUsedAt     time.Time      `json:"lastUsedAt"`
	CreatedAt      time.Time      `json:"createdAt"`
	Status         SkillLifecycle `json:"status"`
	VersionHistory []SkillVersion `json:"versionHistory"`
}

type SkillLifecycle string

const (
	SkillActive   SkillLifecycle = "active"
	SkillCold     SkillLifecycle = "cold"     // 90d idle + retention < 0.3
	SkillArchived SkillLifecycle = "archived" // 180d + < 0.2
	SkillDeleted  SkillLifecycle = "deleted"  // 365d + < 0.1
)

type SkillVersion struct {
	Version    int       `json:"version"`
	Hash       string    `json:"hash"`
	UpdatedAt  time.Time `json:"updatedAt"`
	RollbackTo int       `json:"rollbackTo,omitempty"` // version to restore
}

// ── Interfaces ───────────────────────────────────────────────────────

// PatternClusterer groups learning records into clusters that share
// common failure patterns or successful strategies.
type PatternClusterer interface {
	BuildPatterns(ctx context.Context, workspace string,
		tasks []LearningRecord, existing []LearningRecord) ([]LearningRecord, []string, error)
}

// DraftGenerator produces SkillDraft proposals from clustered patterns.
// The Evidence-aware variant receives supporting evidence (similar tasks,
// past corrections) for richer draft generation.
type DraftGenerator interface {
	GenerateDraft(ctx context.Context, rule LearningRecord, matches []SkillProfile) (SkillDraft, error)
}

// EvidenceAwareDraftGenerator receives evidence context.
type EvidenceAwareDraftGenerator interface {
	DraftGenerator
	GenerateDraftWithEvidence(ctx context.Context, rule LearningRecord,
		matches []SkillProfile, evidence DraftEvidence) (SkillDraft, error)
}

// DraftEvidence carries supporting context for draft generation.
type DraftEvidence struct {
	SimilarRecords  []LearningRecord `json:"similarRecords"`
	PastCorrections []Correction     `json:"pastCorrections"`
	WorkspaceFiles  []string         `json:"workspaceFiles"`
}

// SuccessJudge evaluates whether a task record represents a success.
type SuccessJudge interface {
	JudgeTaskRecord(ctx context.Context, record LearningRecord) (TaskSuccessDecision, error)
}

type TaskSuccessDecision struct {
	Success    bool    `json:"success"`
	Verified   bool    `json:"verified"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// ── Evolution Engine ─────────────────────────────────────────────────

// Engine orchestrates the hot-path recording and cold-path batch
// processing for self-improvement.
type Engine struct {
	mu        sync.Mutex
	recordDir string // ~/.overkill/learning/records/
	skillDir  string // ~/.overkill/skills/
	backupDir string // ~/.overkill/learning/backups/

	clusterer PatternClusterer
	generator DraftGenerator
	judge     SuccessJudge
}

// NewEngine creates an evolution engine.
func NewEngine(recordDir, skillDir string, clusterer PatternClusterer, generator DraftGenerator, judge SuccessJudge) *Engine {
	if clusterer == nil {
		clusterer = &HeuristicClusterer{}
	}
	if judge == nil {
		judge = &HeuristicJudge{}
	}
	return &Engine{
		recordDir: recordDir,
		skillDir:  skillDir,
		backupDir: filepath.Join(recordDir, "backups"),
		clusterer: clusterer,
		generator: generator,
		judge:     judge,
	}
}

// RecordTurn is called synchronously at the end of every agent turn
// (hot path). Appends a LearningRecord to JSONL.
func (e *Engine) RecordTurn(ctx context.Context, record LearningRecord) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	record.RecordedAt = time.Now().UTC()
	if record.TaskHash == "" {
		record.TaskHash = fmt.Sprintf("%x", sha256.Sum256([]byte(record.FinalOutput)))[:16]
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("evolution: marshal record: %w", err)
	}

	path := filepath.Join(e.recordDir, "turns.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("evolution: mkdir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("evolution: open turns: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("evolution: write turn: %w", err)
	}
	return nil
}

// RunColdPath is the batch job. It:
//  1. Loads all task records
//  2. Runs pattern clustering
//  3. Generates draft skills for each cluster
//  4. Reviews drafts (secret scanning)
//  5. Applies accepted drafts (with rollback capability)
func (e *Engine) RunColdPath(ctx context.Context, workspace string, applyMode bool) (*ColdPathResult, error) {
	// Load records. Release the mutex before I/O so concurrent RecordTurn
	// calls (which need the lock to append) are not blocked for the entire
	// duration of reading a potentially large JSONL file.
	result := &ColdPathResult{RunAt: time.Now().UTC()}
	e.mu.Lock()
	recordDir := e.recordDir
	e.mu.Unlock()
	records, err := e.loadRecordsFrom(recordDir)
	if err != nil {
		return nil, fmt.Errorf("evolution: load records: %w", err)
	}
	result.RecordsLoaded = len(records)

	// Judge each record.
	for _, r := range records {
		decision, err := safeJudge(ctx, e.judge, r)
		if err != nil {
			result.JudgeErrors++
			continue
		}
		if decision.Success {
			result.SuccessfulTurns++
			r.SuccessRate = decision.Confidence
		}
		if !decision.Verified {
			result.UnverifiedTurns++
		}
	}
	result.JudgedTurns = len(records) - result.JudgeErrors

	// Cluster patterns.
	clusters, reasons, err := e.clusterer.BuildPatterns(ctx, workspace, records, nil)
	if err != nil {
		return nil, fmt.Errorf("evolution: cluster: %w", err)
	}
	result.ClustersFound = len(clusters)
	result.ClusterReasons = reasons

	// Generate drafts.
	if e.generator == nil {
		return result, nil
	}
	for _, cluster := range clusters {
		// Gather evidence for evidence-aware draft generators.
		var evidence *DraftEvidence
		if eag, ok := e.generator.(EvidenceAwareDraftGenerator); ok {
			evidence = &DraftEvidence{
				SimilarRecords: clusters,
			}
			_ = eag // suppress unused warning — used in type assertion
		}
		draft, err := safeGenerate(ctx, e.generator, cluster, nil, evidence)
		if err != nil {
			result.GenerationErrors++
			continue
		}

		// Review: scan for secrets.
		findings := scanDraftContent(draft.BodyOrPatch)
		if len(findings) > 0 {
			draft.Status = DraftQuarantined
			draft.ScanFindings = findings
			result.QuarantinedDrafts++
			result.Drafts = append(result.Drafts, draft)
			continue
		}

		draft.Status = DraftCandidate
		result.CandidateDrafts++

		// Apply if in apply mode.
		if applyMode {
			if err := e.applyDraft(draft); err != nil {
				result.ApplyErrors++
				continue
			}
			draft.Status = DraftAccepted
			result.AppliedDrafts++
		}
		result.Drafts = append(result.Drafts, draft)
	}

	return result, nil
}

// applyDraft writes the draft to the skills directory with atomic
// temp+rename. Backs up the existing skill if present.
func (e *Engine) applyDraft(draft SkillDraft) error {
	skillPath := filepath.Join(e.skillDir, draft.TargetSkillName, "SKILL.md")

	// Backup existing.
	if _, err := os.Stat(skillPath); err == nil {
		backupPath := filepath.Join(e.backupDir, draft.TargetSkillName,
			fmt.Sprintf("%d", time.Now().UnixNano()), "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
			return err
		}
		data, _ := os.ReadFile(skillPath)
		_ = os.WriteFile(backupPath, data, 0o644)
	}

	// Write new skill atomically.
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		return err
	}
	tmp := skillPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(draft.BodyOrPatch), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, skillPath)
}

// ── ColdPathResult ───────────────────────────────────────────────────

type ColdPathResult struct {
	RunAt             time.Time    `json:"runAt"`
	RecordsLoaded     int          `json:"recordsLoaded"`
	JudgedTurns       int          `json:"judgedTurns"`
	SuccessfulTurns   int          `json:"successfulTurns"`
	UnverifiedTurns   int          `json:"unverifiedTurns"`
	JudgeErrors       int          `json:"judgeErrors"`
	ClustersFound     int          `json:"clustersFound"`
	ClusterReasons    []string     `json:"clusterReasons"`
	CandidateDrafts   int          `json:"candidateDrafts"`
	QuarantinedDrafts int          `json:"quarantinedDrafts"`
	AppliedDrafts     int          `json:"appliedDrafts"`
	GenerationErrors  int          `json:"generationErrors"`
	ApplyErrors       int          `json:"applyErrors"`
	Drafts            []SkillDraft `json:"drafts"`
}

// ── Internal helpers ─────────────────────────────────────────────────

func (e *Engine) loadRecordsFrom(recordDir string) ([]LearningRecord, error) {
	path := filepath.Join(recordDir, "turns.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var records []LearningRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var r LearningRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			log.Printf("learning: corrupt line in %s: %v", path, err)
			continue
		}
		records = append(records, r)
	}
	if err := scanner.Err(); err != nil {
		return records, err
	}
	return records, nil
}

// scanDraftContent checks for secrets in draft content.
func scanDraftContent(content string) []string {
	var findings []string
	patterns := []string{
		"sk-live-", "sk_test_", "-----BEGIN PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----", "api_key", "password",
	}
	for _, pat := range patterns {
		if strings.Contains(strings.ToLower(content), strings.ToLower(pat)) {
			findings = append(findings, fmt.Sprintf("potential secret pattern: %q", pat))
		}
	}
	return findings
}

func safeJudge(ctx context.Context, judge SuccessJudge, record LearningRecord) (TaskSuccessDecision, error) {
	defer func() {
		if r := recover(); r != nil {
			// Don't crash the cold path for one bad judge.
		}
	}()
	if judge == nil {
		return TaskSuccessDecision{Success: false, Reason: "no judge configured"}, nil
	}
	return judge.JudgeTaskRecord(ctx, record)
}

// safeGenerate wraps generator.GenerateDraft with panic recovery.
// If the generator implements EvidenceAwareDraftGenerator and evidence
// is provided, it uses the richer GenerateDraftWithEvidence path.
func safeGenerate(ctx context.Context, gen DraftGenerator, record LearningRecord, matches []SkillProfile, evidence *DraftEvidence) (SkillDraft, error) {
	var draft SkillDraft
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("generator panic: %v", r)
				draft = SkillDraft{}
			}
		}()
		if gen == nil {
			err = fmt.Errorf("no generator configured")
			return
		}
		if eag, ok := gen.(EvidenceAwareDraftGenerator); ok && evidence != nil {
			draft, err = eag.GenerateDraftWithEvidence(ctx, record, matches, *evidence)
		} else {
			draft, err = gen.GenerateDraft(ctx, record, matches)
		}
	}()
	return draft, err
}

// ── Heuristic fallbacks (fast, no LLM) ───────────────────────────────

// HeuristicClusterer groups records by task hash similarity.
type HeuristicClusterer struct{}

func (h *HeuristicClusterer) BuildPatterns(ctx context.Context, workspace string, tasks []LearningRecord, existing []LearningRecord) ([]LearningRecord, []string, error) {
	// Simple: return tasks that share tool execution patterns.
	if len(tasks) < 3 {
		return nil, nil, nil
	}
	clusters := make(map[string][]LearningRecord)
	for _, t := range tasks {
		key := signature(t.ToolExecutions)
		clusters[key] = append(clusters[key], t)
	}
	var out []LearningRecord
	var reasons []string
	for key, group := range clusters {
		if len(group) >= 3 {
			out = append(out, group[0])
			reasons = append(reasons, fmt.Sprintf("pattern: %d turns with tool signature %s", len(group), key))
		}
	}
	return out, reasons, nil
}

func signature(execs []ToolExecution) string {
	parts := make([]string, len(execs))
	for i, e := range execs {
		parts[i] = e.ToolName
	}
	return strings.Join(parts, "→")
}

// HeuristicJudge classifies success by checking for error markers.
type HeuristicJudge struct{}

func (h *HeuristicJudge) JudgeTaskRecord(ctx context.Context, record LearningRecord) (TaskSuccessDecision, error) {
	// Failures: empty output or tool errors.
	if record.FinalOutput == "" {
		return TaskSuccessDecision{Success: false, Verified: true, Confidence: 0.9, Reason: "empty output"}, nil
	}
	for _, e := range record.ToolExecutions {
		if e.Error != "" {
			return TaskSuccessDecision{Success: false, Verified: true, Confidence: 0.7, Reason: fmt.Sprintf("tool %s errored", e.ToolName)}, nil
		}
	}
	// All tools succeeded with non-empty output, but no explicit user
	// verification signal — downgrade to unverified so hallucinated
	// plausible output is not fed into the cold path.
	return TaskSuccessDecision{Success: false, Verified: false, Confidence: 0.3, Reason: "tools passed but unverified — explicit user confirmation required"}, nil
}
