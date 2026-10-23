package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// NewSessionEvolution creates one workspace-bound recorder and proposal engine.
// Generation is an explicit operator action and uses the session's provider,
// model and cost admission. No background paid calls or automatic installation.
func NewSessionEvolution(cfg *config.Config, home string, sess *session.Session, provider providers.Provider, tracker cost.Tracker) (*learning.Engine, error) {
	canonical, err := CanonicalSession(sess)
	if err != nil || home == "" || cfg == nil {
		return nil, fmt.Errorf("evolution: valid workspace and configuration directory required")
	}
	model := canonical.Model
	if model == "" {
		model = cfg.Agent.DefaultModel
	}
	providerName := canonical.Provider
	if providerName == "" {
		providerName = cfg.Agent.DefaultProvider
	}
	var generator learning.DraftGenerator
	if provider != nil && tracker != nil && model != "" {
		var pricing providers.Model
		for _, candidate := range provider.Models() {
			if candidate.ID == model {
				pricing = candidate
				break
			}
		}
		priced := pricing.CostIn > 0 || pricing.CostOut > 0
		generator, err = learning.NewProviderDraftGenerator(learning.ProviderDraftOptions{
			Provider: provider, Model: model, MaxTokens: 2048, Timeout: 15 * time.Second,
			BudgetGate: func(ctx context.Context) error {
				if !priced && (cfg.Cost.DailyLimitUSD > 0 || cfg.Cost.PerTaskLimitUSD > 0 || cfg.Cost.RollingLimitUSD > 0) {
					return fmt.Errorf("evolution: model pricing is required to enforce configured dollar limits")
				}
				status, err := tracker.CheckBudget(ctx, canonical.ID)
				if err != nil || status.ShouldAbort {
					return fmt.Errorf("evolution: budget unavailable or exhausted")
				}
				return nil
			},
			RecordUsage: func(ctx context.Context, usage providers.Usage) error {
				return tracker.Record(ctx, cost.Entry{SessionID: canonical.ID, Model: model, Provider: providerName,
					TaskID: "skill-evolution", InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CachedTokens: usage.CachedInputTokens, CostUSD: cost.CalculateCost(usage, pricing)})
			},
		})
		if err != nil {
			return nil, err
		}
	}
	scope := learning.WorkspaceScope(canonical.Folder)
	base := filepath.Join(home, "learning", "workspaces", scope)
	return learning.NewEngineWithOptions(filepath.Join(base, "records"), filepath.Join(base, "skills"), nil, generator, nil,
		learning.EngineOptions{Workspace: canonical.Folder, KnownSecrets: cfg.RedactionSecrets()}), nil
}

// evolution.* is authenticated operator RPC, deliberately absent from tools.
func (s *Server) handleEvolution(ctx context.Context, method string, params []byte) (interface{}, *RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
		ID        string `json:"id,omitempty"`
		Approve   *bool  `json:"approve,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "Invalid project skill operation parameters"}
	}
	if p.SessionID == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "session_id required"}
	}
	release, err := s.beginSessionOperation(p.SessionID)
	if err != nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Session is busy; finish or stop its current operation first."}
	}
	defer release()
	ctx, finish := s.bindSessionCancellation(ctx, p.SessionID)
	defer finish()
	a, rpcErr := s.getOrCreateAdmittedAgent(ctx, p.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	engine := a.EvolutionEngine()
	if engine == nil {
		return nil, &RPCError{Code: InternalError, Message: "Project skill evolution is unavailable."}
	}
	var result interface{}
	switch method {
	case "evolution.list":
		result, err = engine.ListDrafts(ctx)
	case "evolution.generate":
		if s.costTracker == nil {
			return nil, &RPCError{Code: InvalidRequest, Message: "Generation requires durable usage accounting."}
		}
		generationCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		result, err = engine.RunColdPath(generationCtx, "", false)
	case "evolution.review":
		if p.Approve == nil {
			return nil, &RPCError{Code: InvalidParams, Message: "approve is required"}
		}
		result, err = engine.ReviewDraft(ctx, p.ID, *p.Approve)
	case "evolution.apply":
		result, err = engine.ApplyReviewedDraft(ctx, p.ID)
	case "evolution.rollback":
		result, err = engine.RollbackDraft(ctx, p.ID)
	default:
		return nil, &RPCError{Code: MethodNotFound, Message: "Unknown evolution operation"}
	}
	if err != nil {
		return nil, &RPCError{Code: InvalidRequest, Message: err.Error()}
	}
	return result, nil
}
