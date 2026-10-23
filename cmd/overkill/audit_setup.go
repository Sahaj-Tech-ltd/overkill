package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/audit"
	"github.com/Sahaj-Tech-ltd/overkill/internal/childruntime"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// Completion workers have a separate factory: normal delegated fs/patch
// capabilities must never leak into the auditor's verification worker.
func newCompletionAuditor(cfg *config.Config, workspace string) (*audit.Auditor, func()) {
	manager := subagent.NewManager(subagent.Config{MaxDepth: 1, MaxChildren: 1, ChildTimeout: 30 * time.Second})
	factory := childruntime.Factory{Workspace: workspace, AuditReadOnly: true, Resolve: func(provider, model string) (providers.Provider, string, error) {
		return resolveChildProvider(cfg, provider, model)
	}}
	if cfg != nil {
		factory.Instructions = cfg.Agent.Instructions
		factory.Personality = personality.Config{Level: personality.ParseLevel(cfg.Personality.Level), Language: cfg.Personality.Language}
	}
	manager.SetWorkerFactory(func(child subagent.RealWorkerConfig) (subagent.WorkerRunner, error) {
		if cfg != nil && child.MaxTokens == 0 {
			child.MaxTokens = cfg.Agent.MaxOutputTokens
		}
		return factory.Build(child)
	})
	model := ""
	if cfg != nil {
		model = cfg.Agent.DefaultModel
	}
	return &audit.Auditor{WorkDir: workspace, SubAgent: &audit.SubAgentRunnerAdapter{Manager: manager, Model: model, WorkDir: workspace}}, func() {
		for _, child := range manager.ActiveChildren() {
			if child.Cancel != nil {
				child.Cancel()
			}
		}
	}
}

func runAuditCommand(ctx context.Context, auditor *audit.Auditor, args string, transcript ...string) error {
	if auditor == nil {
		return fmt.Errorf("completion auditor unavailable")
	}
	if args == "start" {
		snapshot, err := auditor.CaptureBaseline(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("Audit baseline captured: %d files. Project build/test scripts run only with /audit run <JSON claims>.\n", len(snapshot.Files))
		return nil
	}
	if !strings.HasPrefix(args, "run ") {
		return fmt.Errorf("use /audit start before work, then /audit run [{\"description\":\"...\",\"files\":[\"path\"]}]")
	}
	var claims []audit.Claim
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(args, "run "))), &claims); err != nil {
		return err
	}
	if len(transcript) > 0 {
		ctx = audit.WithTranscript(ctx, transcript[0])
	}
	report, err := auditor.AuditCaptured(ctx, claims)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	if !report.Passed {
		return fmt.Errorf("completion audit failed; see command and semantic results above")
	}
	return nil
}
