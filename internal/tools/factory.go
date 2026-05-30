// Package tools — shared tool registry factory.
//
// Entry points (run.go, web.go, slack.go, tui.go) previously hand-rolled
// 3-8 tool registrations each, diverging over time. This factory centralises
// every available tool constructor behind NewDefaultRegistry so adding a
// tool in one place registers it everywhere.
//
// Sub-package tools (tts, messaging, imagegen) live outside the tools
// package and some transitively import gateway → agent → tools, creating
// an import cycle if imported here. Those tools are accepted as pre-built
// Tool values via FactoryDeps.ExtraTools. Callers construct them in their
// own package (cmd/overkill) where the cycle doesn't apply.
package tools

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/browser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/browser/devbrowser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
	"github.com/Sahaj-Tech-ltd/overkill/internal/lsp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/memory"
	"github.com/Sahaj-Tech-ltd/overkill/internal/multimodal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"

	"github.com/rs/zerolog/log"
)

// FactoryDeps holds all optional runtime dependencies needed to construct
// tools. Fields are nil/zero when the corresponding subsystem is not
// available; NewDefaultRegistry skips tools whose dependencies are missing.
type FactoryDeps struct {
	// CWD is the working directory for file-system-scoped tools.
	CWD string

	// ── Browser ──────────────────────────────────────────────────────
	BrowserManager *browser.Manager
	BrowserPolicy  BrowserHostPolicy

	// ── Dev browser (legacy narrow surface) ──────────────────────────
	DevBrowserManager *devbrowser.Manager

	// ── Vision ───────────────────────────────────────────────────────
	VisionDescriber vision.Describer

	// ── LSP ──────────────────────────────────────────────────────────
	LSPQuerier LSPQuerier
	LSPManager *lsp.Manager

	// ── Memory ───────────────────────────────────────────────────────
	MemoryOrchestrator *memory.Orchestrator

	// ── Plan ─────────────────────────────────────────────────────────
	PlanQuerier PlanQuerier

	// ── Checkpoint ───────────────────────────────────────────────────
	CheckpointManager  *checkpoint.Manager
	CheckpointSessionFn func() string

	// ── Subagent ─────────────────────────────────────────────────────
	SubagentManager *subagent.Manager

	// ── Goal ─────────────────────────────────────────────────────────
	GoalQuerier GoalQuerier

	// ── Tasks ────────────────────────────────────────────────────────
	TasksStore       TasksStore
	SessionIDProvider SessionIDProvider

	// ── Segments ─────────────────────────────────────────────────────
	SegmentsStore SegmentsStore

	// ── Standing Orders ──────────────────────────────────────────────
	StandingOrdersStore StandingOrdersStore

	// ── Journal ──────────────────────────────────────────────────────
	JournalQuerier JournalQuerier
	JournalReader  JournalReader // bookmark_recall

	// ── Playbooks ────────────────────────────────────────────────────
	PlaybooksStore PlaybooksStore

	// ── Automation ───────────────────────────────────────────────────
	AutomationLister AutomationLister
	CronLister        CronLister

	// ── FailHypo ─────────────────────────────────────────────────────
	FailHypoQuerier FailHypoQuerier

	// ── Learnings ────────────────────────────────────────────────────
	LearningsQuerier LearningsQuerier
	LearnRecorder    LearnRecorder

	// ── Pipeline ─────────────────────────────────────────────────────
	PipelineRunner PipelineRunner

	// ── Project Root ─────────────────────────────────────────────────
	ProjectRootResolver ProjectRootResolver

	// ── Test Agent (Spider-Man) ──────────────────────────────────────
	TestAgentRunner TestAgentRunner

	// ── Ask User ─────────────────────────────────────────────────────
	AskUserBridge AskUserBridge

	// ── Alarm ────────────────────────────────────────────────────────
	AlarmGateway     AlarmGateway
	AlarmSessionFn   func() string

	// ── Tags ─────────────────────────────────────────────────────────
	TagsManager *tags.Manager

	// ── Architecture Walls ───────────────────────────────────────────
	ArchitectureWall *walls.ArchitectureWall
	OuroborosWall    *walls.OuroborosWall

	// ── Regression Bank ──────────────────────────────────────────────
	RegressionBank *walls.RegressionBank

	// ── Autocommit ───────────────────────────────────────────────────
	AutoCommitter *automation.AutoCommitter

	// ── Multimodal ───────────────────────────────────────────────────
	MultimodalRegistry *multimodal.Registry

	// ── Introspect / Skill Extract ───────────────────────────────────
	IntrospectDir   string
	SkillExtractDir string

	// ── Extra tools ──────────────────────────────────────────────────
	// For sub-package tools (tts, messaging, imagegen) that live outside
	// the tools package and may transitively import gateway → agent →
	// tools (creating an import cycle if imported here). Callers in
	// cmd/overkill construct these tools and append them here.
	ExtraTools []Tool
}

// NewDefaultRegistry builds a fully-populated Registry from the provided
// dependencies. Core tools (shell, fs, git, grep, web, patch, pty) are
// always registered. Infrastructure tools are registered only when their
// corresponding dependency is non-nil/zero. ExtraTools are unconditionally
// appended — callers are responsible for nil-checking before appending.
func NewDefaultRegistry(deps FactoryDeps) *Registry {
	reg := NewRegistry()
	var regErrs []error

	register := func(tool Tool) {
		if err := reg.Register(tool); err != nil {
			regErrs = append(regErrs, err)
		}
	}

	// ── Core tools (always available) ────────────────────────────────
	register(NewShellTool())
	register(NewFSTool(deps.CWD))
	register(NewGitTool(deps.CWD))
	register(NewGrepTool(deps.CWD))
	register(NewWebTool())
	register(NewPatchTool(deps.CWD))
	register(NewPTYShellTool(deps.CWD))
	register(NewSliceDecomposeTool())
	register(NewACPSendTool())
	register(NewDiagnoseNextTierTool())

	// ── Introspect / Skill Extract ───────────────────────────────────
	if deps.IntrospectDir != "" {
		register(NewIntrospectTool(deps.IntrospectDir))
	}
	if deps.SkillExtractDir != "" {
		register(NewSkillExtractTool(deps.SkillExtractDir))
	}

	// ── Browser tools ────────────────────────────────────────────────
	if deps.BrowserManager != nil {
		register(NewBrowserOpenTool(deps.BrowserManager, deps.BrowserPolicy))
		register(NewBrowserNavigateTool(deps.BrowserManager, deps.BrowserPolicy))
		register(NewBrowserScreenshotTool(deps.BrowserManager, deps.BrowserPolicy))
		register(NewBrowserTextTool(deps.BrowserManager, deps.BrowserPolicy))
		register(NewBrowserMarkdownTool(deps.BrowserManager, deps.BrowserPolicy))
		register(NewBrowserClickTool(deps.BrowserManager, deps.BrowserPolicy))
		register(NewBrowserFillTool(deps.BrowserManager, deps.BrowserPolicy))
		register(NewBrowserSelectTool(deps.BrowserManager, deps.BrowserPolicy))
		register(NewBrowserEvalTool(deps.BrowserManager, deps.BrowserPolicy))
		register(NewBrowserWaitTool(deps.BrowserManager, deps.BrowserPolicy))
	}

	// ── Dev browser ──────────────────────────────────────────────────
	if deps.DevBrowserManager != nil {
		register(NewBrowserDevTool())
		register(NewDevBrowserOpenTool(deps.DevBrowserManager))
		register(NewDevBrowserSnapshotTool(deps.DevBrowserManager))
		register(NewDevBrowserClickTool(deps.DevBrowserManager))
		register(NewDevBrowserTypeTool(deps.DevBrowserManager))
	}

	// ── Vision ───────────────────────────────────────────────────────
	if deps.VisionDescriber != nil {
		register(NewVisionDescribeTool(deps.VisionDescriber, deps.BrowserManager, deps.BrowserPolicy))
	}

	// ── LSP tools ────────────────────────────────────────────────────
	if deps.LSPQuerier != nil {
		register(NewLSPHoverTool(deps.LSPQuerier))
		register(NewLSPDefinitionTool(deps.LSPQuerier))
		register(NewLSPReferencesTool(deps.LSPQuerier))
	}
	if deps.LSPManager != nil {
		register(NewLSPSymbolsTool(deps.LSPManager))
	}

	// ── Memory tools ─────────────────────────────────────────────────
	if deps.MemoryOrchestrator != nil {
		register(NewMemoryRememberTool(deps.MemoryOrchestrator))
		register(NewMemoryRecallTool(deps.MemoryOrchestrator))
		register(NewMemoryForgetTool(deps.MemoryOrchestrator))
	}

	// ── Plan tools ───────────────────────────────────────────────────
	if deps.PlanQuerier != nil {
		register(NewPlanSetTool(deps.PlanQuerier))
		register(NewPlanCheckTool(deps.PlanQuerier))
		register(NewPlanStatusTool(deps.PlanQuerier))
		register(NewPlanClearTool(deps.PlanQuerier))
	}

	// ── Checkpoint tools ─────────────────────────────────────────────
	if deps.CheckpointManager != nil {
		register(NewCheckpointSnapshotTool(deps.CheckpointManager, deps.CheckpointSessionFn))
		register(NewCheckpointListTool(deps.CheckpointManager, deps.CheckpointSessionFn))
		register(NewCheckpointRestoreTool(deps.CheckpointManager))
	}

	// ── Subagent tools ───────────────────────────────────────────────
	if deps.SubagentManager != nil {
		register(NewDelegateTool(deps.SubagentManager))
		register(NewSubagentStatusTool(deps.SubagentManager))
		register(NewSubagentWaitTool(deps.SubagentManager))
	}

	// ── Goal tools ───────────────────────────────────────────────────
	if deps.GoalQuerier != nil {
		register(NewCreateGoalTool(deps.GoalQuerier))
		register(NewGetGoalTool(deps.GoalQuerier))
		register(NewUpdateGoalTool(deps.GoalQuerier))
	}

	// ── Tasks tools ──────────────────────────────────────────────────
	if deps.TasksStore != nil {
		register(NewTaskOpenTool(deps.TasksStore, deps.SessionIDProvider))
		register(NewTaskCloseTool(deps.TasksStore))
		register(NewTaskLinkCommitTool(deps.TasksStore))
		register(NewTaskNoteTool(deps.TasksStore))
		register(NewTaskListTool(deps.TasksStore))
	}

	// ── Segment tools ────────────────────────────────────────────────
	if deps.SegmentsStore != nil {
		register(NewSegmentCreateTool(deps.SegmentsStore))
		register(NewSegmentListTool(deps.SegmentsStore))
		register(NewSegmentRankTool(deps.SegmentsStore))
		register(NewSegmentLoadTool(deps.SegmentsStore))
		register(NewSegmentDeleteTool(deps.SegmentsStore))
	}

	// ── Standing orders tools ────────────────────────────────────────
	if deps.StandingOrdersStore != nil {
		register(NewStandingOrderAddTool(deps.StandingOrdersStore))
		register(NewStandingOrderRemoveTool(deps.StandingOrdersStore))
		register(NewStandingOrderToggleTool(deps.StandingOrdersStore))
		register(NewStandingOrderListTool(deps.StandingOrdersStore))
	}

	// ── Journal tools ────────────────────────────────────────────────
	if deps.JournalQuerier != nil {
		register(NewJournalSearchTool(deps.JournalQuerier))
		register(NewJournalTimelineTool(deps.JournalQuerier))
		register(NewJournalGetTool(deps.JournalQuerier))
	}

	// ── Bookmark tools ───────────────────────────────────────────────
	if deps.TagsManager != nil {
		register(NewBookmarkCreateTool(deps.TagsManager))
		register(NewBookmarkListTool(deps.TagsManager))
		if deps.JournalReader != nil {
			register(NewBookmarkRecallTool(deps.TagsManager, deps.JournalReader))
		}
	}

	// ── Playbooks tools ──────────────────────────────────────────────
	if deps.PlaybooksStore != nil {
		register(NewPlaybookCreateTool(deps.PlaybooksStore))
		register(NewPlaybookRankTool(deps.PlaybooksStore))
		register(NewPlaybookUseTool(deps.PlaybooksStore))
		register(NewPlaybookRecordOutcomeTool(deps.PlaybooksStore))
		register(NewPlaybookRefineTool(deps.PlaybooksStore))
		register(NewPlaybookListTool(deps.PlaybooksStore))
	}

	// ── Automation tools ─────────────────────────────────────────────
	if deps.AutomationLister != nil {
		register(NewAutomationListTool(deps.AutomationLister))
	}
	if deps.CronLister != nil {
		register(NewCronListTool(deps.CronLister))
	}

	// ── FailHypo ─────────────────────────────────────────────────────
	if deps.FailHypoQuerier != nil {
		register(NewFailHypoSearchTool(deps.FailHypoQuerier))
	}

	// ── Learnings ────────────────────────────────────────────────────
	if deps.LearningsQuerier != nil {
		register(NewRecordLearningTool(deps.LearningsQuerier, deps.SessionIDProvider))
		register(NewLearningsSearchTool(deps.LearningsQuerier))
	}
	if deps.LearnRecorder != nil {
		register(NewLearnRecordTool(deps.LearnRecorder))
	}

	// ── Pipeline ─────────────────────────────────────────────────────
	if deps.PipelineRunner != nil {
		register(NewPipelineTool(deps.PipelineRunner))
	}

	// ── Arch / Glossary ──────────────────────────────────────────────
	if deps.ProjectRootResolver != nil {
		register(NewArchReadTool(deps.ProjectRootResolver))
		register(NewGlossaryReadTool(deps.ProjectRootResolver))
		register(NewGlossaryAddTermTool(deps.ProjectRootResolver))
	}

	// ── Worktree tools ───────────────────────────────────────────────
	if deps.CWD != "" {
		register(NewWorktreeListTool(deps.CWD))
		register(NewWorktreeAddTool(deps.CWD))
		register(NewWorktreeRemoveTool(deps.CWD))
	}

	// ── Test Agent (Spider-Man) ──────────────────────────────────────
	if deps.TestAgentRunner != nil {
		register(NewSpiderTestTool(deps.TestAgentRunner))
		register(NewSpiderValidateTool(deps.TestAgentRunner))
	}

	// ── Ask User ─────────────────────────────────────────────────────
	if deps.AskUserBridge != nil {
		register(NewAskUserTool(deps.AskUserBridge))
	}

	// ── Alarm tools ──────────────────────────────────────────────────
	if deps.AlarmGateway != nil {
		register(NewAlarmSetTool(deps.AlarmGateway, deps.AlarmSessionFn))
		register(NewAlarmListTool(deps.AlarmGateway))
		register(NewAlarmCancelTool(deps.AlarmGateway))
	}

	// ── Tag tools ────────────────────────────────────────────────────
	if deps.TagsManager != nil {
		register(NewTagAddTool(deps.TagsManager))
		register(NewTagRemoveTool(deps.TagsManager))
		register(NewTagListTool(deps.TagsManager))
	}

	// ── Architecture Walls ───────────────────────────────────────────
	if deps.ArchitectureWall != nil {
		register(NewArchitectureWallTool(deps.ArchitectureWall))
	}
	if deps.OuroborosWall != nil {
		register(NewOuroborosWallTool(deps.OuroborosWall))
	}

	// ── Regression Bank ──────────────────────────────────────────────
	if deps.RegressionBank != nil {
		register(NewRegressionRecordTool(deps.RegressionBank))
		register(NewRegressionListTool(deps.RegressionBank))
		register(NewRegressionVerifyTool(deps.RegressionBank))
	}

	// ── Autocommit ───────────────────────────────────────────────────
	if deps.AutoCommitter != nil {
		register(NewAutocommitStageTool(deps.AutoCommitter))
	}

	// ── Multimodal Understand ────────────────────────────────────────
	if deps.MultimodalRegistry != nil {
		register(NewUnderstandTool(deps.MultimodalRegistry, deps.CWD))
	}

	// ── Extra tools (sub-package / caller-provided) ──────────────────
	for _, t := range deps.ExtraTools {
		if t != nil {
			register(t)
		}
	}

	if len(regErrs) > 0 {
		for _, err := range regErrs {
			log.Warn().Err(err).Msg("tools: registration failed")
		}
	}

	return reg
}
