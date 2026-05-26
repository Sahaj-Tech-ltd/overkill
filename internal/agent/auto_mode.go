// Package agent — Auto Mode: full-autonomy plan execution (master plan §7.3).
//
// Three autonomy levels:
//   safe  — ask permission for EVERY tool call; subagents need confirmation
//   yolo  — only dangerous ops get prompted; subagents auto-start
//   auto  — zero human input: load plan, batch all clarifying questions,
//           execute phases sequentially, auto-compress, auto-chain
//
// In auto mode the agent loads a plan file (markdown, one phase per ## heading),
// asks ALL questions upfront in a single batch, then executes autonomously
// until the plan is complete or an error blocks progress.

package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// AutonomyLevel maps config strings to behavior.
type AutonomyLevel string

const (
	AutonomyReadonly   AutonomyLevel = "readonly"
	AutonomySupervised AutonomyLevel = "supervised"
	AutonomyFull       AutonomyLevel = "full"
	AutonomySafe       AutonomyLevel = "safe"
	AutonomyYolo       AutonomyLevel = "yolo"
	AutonomyAuto       AutonomyLevel = "auto"
)

// NeedsApproval reports whether a tool call should trigger the approval dialog.
func (l AutonomyLevel) NeedsApproval(isDangerous bool) bool {
	switch l {
	case AutonomySafe:
		return true // EVERY tool call
	case AutonomyYolo:
		return isDangerous // only dangerous ops
	case AutonomyAuto:
		return false // never ask
	case AutonomyReadonly:
		return true // can't execute writes anyway
	default:
		return isDangerous // supervised, full: default behavior
	}
}

// DangerousOps are tool patterns that trigger approval in yolo mode.
var dangerousOps = map[string]bool{
	"git push":           true,
	"git push --force":   true,
	"rm -rf":             true,
	"rm -r":              true,
	"chmod 777":          true,
	"docker rm":          true,
	"docker system prune": true,
	"shutdown":           true,
	"reboot":             true,
	"mkfs":               true,
	"dd if=":             true,
	":(){ :|:& };:":      true, // fork bomb
	"curl.*|.*sh":        true, // pipe to shell
	"wget.*|.*sh":        true,
}

// IsDangerousTool checks a command string against the dangerous pattern list.
func IsDangerousTool(command string) bool {
	cmd := strings.TrimSpace(command)
	for pattern := range dangerousOps {
		if strings.Contains(cmd, pattern) {
			return true
		}
	}
	// Also match regex patterns
	dangerousRes := []*regexp.Regexp{
		regexp.MustCompile(`curl\s+.*\|\s*(ba)?sh`),
		regexp.MustCompile(`wget\s+.*\|\s*(ba)?sh`),
		regexp.MustCompile(`>\s*/dev/sd[a-z]`),
	}
	for _, re := range dangerousRes {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// Plan represents a parsed execution plan with sequential phases.
type Plan struct {
	Title  string
	Phases []PlanPhase
	Source string // file path
}

// PlanPhase is one phase/step in an execution plan.
type PlanPhase struct {
	Index       int
	Title       string
	Description string
	Status      PhaseStatus
}

type PhaseStatus int

const (
	PhasePending PhaseStatus = iota
	PhaseRunning
	PhaseDone
	PhaseFailed
	PhaseSkipped
)

func (s PhaseStatus) String() string {
	switch s {
	case PhasePending:
		return "pending"
	case PhaseRunning:
		return "running"
	case PhaseDone:
		return "done"
	case PhaseFailed:
		return "failed"
	case PhaseSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// AutoMode manages autonomous plan execution state.
type AutoMode struct {
	Level         AutonomyLevel
	Plan          *Plan
	CurrentPhase  int
	Questions     []string // batched clarifying questions
	QuestionsDone bool
	ContextBudget int // token budget for auto-compress
}

// NewAutoMode creates an auto-mode controller from config settings.
func NewAutoMode(level string) *AutoMode {
	return &AutoMode{
		Level:         AutonomyLevel(level),
		CurrentPhase:  -1, // not started
		ContextBudget: 100000,
	}
}

// IsAuto reports whether we're in fully-autonomous mode.
func (am *AutoMode) IsAuto() bool {
	return am.Level == AutonomyAuto
}

// IsYolo reports whether we're in yolo mode.
func (am *AutoMode) IsYolo() bool {
	return am.Level == AutonomyYolo
}

// IsSafe reports whether we're in safe mode.
func (am *AutoMode) IsSafe() bool {
	return am.Level == AutonomySafe
}

// LoadPlan reads a plan from a markdown file. Plans use ## headings for phases.
// Format:
//
//	# Plan Title
//	## Phase 1: Description
//	Details here...
//
//	## Phase 2: Description
//	Details...
func LoadPlan(path string) (*Plan, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("auto: cannot read plan %s: %w", abs, err)
	}
	defer f.Close()

	plan := &Plan{Source: abs}
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024) // 2MB max

	var currentPhase *PlanPhase
	var descLines []string
	phaseIdx := 0

	for scanner.Scan() {
		line := scanner.Text()

		// # Title
		if strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ") {
			plan.Title = strings.TrimPrefix(line, "# ")
			continue
		}

		// ## Phase: Description
		if strings.HasPrefix(line, "## ") {
			// Save previous phase
			if currentPhase != nil {
				currentPhase.Description = strings.Join(descLines, "\n")
				plan.Phases = append(plan.Phases, *currentPhase)
			}

			title := strings.TrimPrefix(line, "## ")
			currentPhase = &PlanPhase{
				Index:  phaseIdx,
				Title:  title,
				Status: PhasePending,
			}
			descLines = nil
			phaseIdx++
			continue
		}

		// Phase description lines
		if currentPhase != nil && strings.TrimSpace(line) != "" {
			descLines = append(descLines, line)
		}
	}

	// Save last phase
	if currentPhase != nil {
		currentPhase.Description = strings.Join(descLines, "\n")
		plan.Phases = append(plan.Phases, *currentPhase)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("auto: error reading plan: %w", err)
	}

	if len(plan.Phases) == 0 {
		return nil, fmt.Errorf("auto: no phases found in plan %s (expected ## Phase headers)", abs)
	}

	return plan, nil
}

// NextPhase returns the next pending phase and marks it running.
func (am *AutoMode) NextPhase() *PlanPhase {
	if am.Plan == nil {
		return nil
	}
	for i := am.CurrentPhase + 1; i < len(am.Plan.Phases); i++ {
		if am.Plan.Phases[i].Status == PhasePending {
			am.Plan.Phases[i].Status = PhaseRunning
			am.CurrentPhase = i
			return &am.Plan.Phases[i]
		}
	}
	return nil
}

// MarkPhaseDone marks the current phase as complete.
func (am *AutoMode) MarkPhaseDone() {
	if am.Plan != nil && am.CurrentPhase >= 0 && am.CurrentPhase < len(am.Plan.Phases) {
		am.Plan.Phases[am.CurrentPhase].Status = PhaseDone
	}
}

// MarkPhaseFailed marks the current phase as failed.
func (am *AutoMode) MarkPhaseFailed() {
	if am.Plan != nil && am.CurrentPhase >= 0 && am.CurrentPhase < len(am.Plan.Phases) {
		am.Plan.Phases[am.CurrentPhase].Status = PhaseFailed
	}
}

// IsComplete reports whether all phases are done or skipped.
func (am *AutoMode) IsComplete() bool {
	if am.Plan == nil {
		return true
	}
	for _, p := range am.Plan.Phases {
		if p.Status == PhasePending || p.Status == PhaseRunning {
			return false
		}
	}
	return true
}

// HasFailed reports whether any phase failed.
func (am *AutoMode) HasFailed() bool {
	if am.Plan == nil {
		return false
	}
	for _, p := range am.Plan.Phases {
		if p.Status == PhaseFailed {
			return true
		}
	}
	return false
}

// Progress returns a formatted progress string.
func (am *AutoMode) Progress() string {
	if am.Plan == nil {
		return "no plan loaded"
	}
	done := 0
	for _, p := range am.Plan.Phases {
		if p.Status == PhaseDone {
			done++
		}
	}
	return fmt.Sprintf("[%d/%d] %s", done, len(am.Plan.Phases), am.Plan.Title)
}

// GenerateQuestions analyzes the plan and generates batched clarifying questions.
// In auto mode these are asked upfront before any execution.
func (am *AutoMode) GenerateQuestions() []string {
	if am.Plan == nil {
		return nil
	}
	var qs []string
	for _, phase := range am.Plan.Phases {
		q := am.analyzePhase(&phase)
		if q != "" {
			qs = append(qs, fmt.Sprintf("Phase %d (%s): %s", phase.Index+1, phase.Title, q))
		}
	}
	am.Questions = qs
	return qs
}

// analyzePhase looks for ambiguity in a phase that warrants a clarifying question.
func (am *AutoMode) analyzePhase(phase *PlanPhase) string {
	desc := strings.ToLower(phase.Description)

	// Detect ambiguous decisions
	ambiguous := map[string]string{
		"either":      "which option should be chosen?",
		"decide":      "what's the decision criteria?",
		"choose":      "which choice do you prefer?",
		"config":      "any specific config values needed?",
		"deploy":      "which environment (staging/prod)?",
		"migrate":     "any rollback strategy required?",
		"rename":      "confirm the exact new name?",
		"refactor":    "any constraints on approach (incremental vs big-bang)?",
		"api":         "any API version or auth requirements?",
		"database":    "which database backend?",
		"scale":       "target capacity/performance numbers?",
	}

	for keyword, question := range ambiguous {
		if strings.Contains(desc, keyword) {
			return question
		}
	}
	return ""
}

// BuildAutoPrompt constructs the system prompt extension for auto mode.
func (am *AutoMode) BuildAutoPrompt() string {
	if am.Plan == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n## AUTONOMOUS EXECUTION MODE\n")
	sb.WriteString("You are running in FULLY AUTONOMOUS mode. Do not ask for permission.\n")
	sb.WriteString("Execute the following plan phases sequentially without stopping.\n")
	sb.WriteString("When one phase completes, immediately start the next phase.\n")
	sb.WriteString("Auto-compress context when needed. Do not wait for human input.\n\n")

	sb.WriteString("### EXECUTION PLAN\n")
	sb.WriteString(fmt.Sprintf("Plan: %s\n", am.Plan.Title))
	sb.WriteString(fmt.Sprintf("Phases: %d total\n\n", len(am.Plan.Phases)))

	for i, phase := range am.Plan.Phases {
		status := "[ ]"
		if phase.Status == PhaseDone {
			status = "[x]"
		} else if phase.Status == PhaseRunning {
			status = "[>]"
		} else if phase.Status == PhaseFailed {
			status = "[!]"
		}
		sb.WriteString(fmt.Sprintf("%s Phase %d: %s\n", status, i+1, phase.Title))
		if phase.Description != "" {
			// Truncate long descriptions
			desc := phase.Description
			if len(desc) > 500 {
				desc = desc[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("   %s\n", strings.ReplaceAll(desc, "\n", "\n   ")))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### CURRENT PHASE\n")
	if am.CurrentPhase >= 0 && am.CurrentPhase < len(am.Plan.Phases) {
		p := am.Plan.Phases[am.CurrentPhase]
		sb.WriteString(fmt.Sprintf("Now executing: Phase %d — %s\n", am.CurrentPhase+1, p.Title))
		sb.WriteString("When done, proceed to next phase without asking.\n")
	} else {
		sb.WriteString("Start with Phase 1.\n")
	}

	return sb.String()
}

// FindPlanFiles discovers plan files in common locations.
func FindPlanFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dirs := []string{
		filepath.Join(home, ".overkill", "plans"),
		filepath.Join(home, ".overkill"),
		".",
	}
	var plans []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && (strings.Contains(e.Name(), "plan") || strings.Contains(e.Name(), "master")) &&
				strings.HasSuffix(e.Name(), ".md") {
				plans = append(plans, filepath.Join(dir, e.Name()))
			}
		}
	}
	return plans
}

// PhasePrompt builds the prompt for a specific phase execution.
func (am *AutoMode) PhasePrompt(phase *PlanPhase) string {
	return fmt.Sprintf(
		"Execute Phase %d: %s\n\n%s\n\nAfter completing this phase, report success and the next phase will start automatically.",
		phase.Index+1, phase.Title, phase.Description,
	)
}
