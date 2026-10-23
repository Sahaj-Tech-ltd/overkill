package learning

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestEvolutionRealProviderHTTPGenerationPersistedDraftReview(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]any
	proposal := `{"targetSkillName":"project-checks","draftType":"workflow","changeKind":"create","bodyOrPatch":"# Project checks\nInspect the result and run the relevant tests."}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "wrong route", 404)
			return
		}
		var request map[string]any
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": proposal}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 25, "completion_tokens": 17}})
	}))
	defer upstream.Close()
	var usage []providers.Usage
	admissions := 0
	generator, err := NewProviderDraftGenerator(ProviderDraftOptions{Provider: providers.NewOpenAICompatProvider("fixture", upstream.URL, "local-fixture-only", nil), Model: "fixture-model", MaxTokens: 512, Timeout: time.Second,
		BudgetGate: func(context.Context) error { admissions++; return nil }, RecordUsage: func(_ context.Context, u providers.Usage) error { usage = append(usage, u); return nil }})
	require.NoError(t, err)
	engine, recordDir, skillDir := evolutionFixture(t)
	engine.generator = generator
	for i := 0; i < 3; i++ {
		require.NoError(t, engine.RecordTurn(t.Context(), LearningRecord{TaskHash: strings.Repeat("a", 64), FinalOutput: "Inspected project output.", ToolExecutions: []ToolExecution{{ToolName: "read", Args: "fixture-private-credential-value", Result: "private contents"}}}))
	}
	result, err := engine.RunColdPath(t.Context(), "", false)
	require.NoError(t, err)
	require.Equal(t, 1, result.CandidateDrafts)
	require.Zero(t, result.AppliedDrafts)
	require.Equal(t, 1, admissions)
	require.Len(t, usage, 1)
	require.Equal(t, 25, usage[0].InputTokens)
	require.Equal(t, 17, usage[0].OutputTokens)
	mu.Lock()
	captured := requests[0]
	mu.Unlock()
	encoded, err := json.Marshal(captured)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "fixture-private-credential-value")
	require.NotContains(t, string(encoded), "private contents")
	require.Equal(t, "fixture-model", captured["model"])
	require.Equal(t, float64(512), captured["max_tokens"])
	_, err = os.Stat(filepath.Join(skillDir, "project-checks", "SKILL.md"))
	require.Error(t, err)
	restarted := NewEngineWithOptions(recordDir, skillDir, nil, nil, nil, engine.options)
	drafts, err := restarted.ListDrafts(t.Context())
	require.NoError(t, err)
	require.Len(t, drafts, 1)
	guidance, err := restarted.LoadAppliedGuidance(t.Context())
	require.NoError(t, err)
	require.Empty(t, guidance)
	_, err = restarted.ReviewDraft(t.Context(), drafts[0].ID, true)
	require.NoError(t, err)
	_, err = restarted.ApplyReviewedDraft(t.Context(), drafts[0].ID)
	require.NoError(t, err)
	guidance, err = NewEngineWithOptions(recordDir, skillDir, nil, nil, nil, engine.options).LoadAppliedGuidance(t.Context())
	require.NoError(t, err)
	require.Len(t, guidance, 1)
	require.Contains(t, guidance[0].Body, "Inspect the result")
}

func TestEvolutionProviderMalformedNullAndBudgetFailures(t *testing.T) {
	cases := []struct {
		name, content       string
		budgetErr, usageErr bool
	}{
		{name: "null", content: "null"}, {name: "empty", content: ""}, {name: "empty object", content: "{}"},
		{name: "unknown approval", content: `{"targetSkillName":"safe","draftType":"workflow","changeKind":"create","bodyOrPatch":"# Safe","status":"accepted"}`},
		{name: "multiple", content: `{} {}`}, {name: "traversal", content: `{"targetSkillName":"../escape","draftType":"workflow","changeKind":"create","bodyOrPatch":"# Safe"}`},
		{name: "budget", budgetErr: true}, {name: "usage", content: `{"targetSkillName":"safe","draftType":"workflow","changeKind":"create","bodyOrPatch":"# Safe"}`, usageErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &evolutionRecordingProvider{response: providers.Response{Content: tc.content, Usage: providers.Usage{InputTokens: 10, OutputTokens: 2}}}
			records := 0
			generator, err := NewProviderDraftGenerator(ProviderDraftOptions{Provider: provider, Model: "fixture", BudgetGate: func(context.Context) error {
				if tc.budgetErr {
					return errors.New("budget denied")
				}
				return nil
			}, RecordUsage: func(context.Context, providers.Usage) error {
				records++
				if tc.usageErr {
					return errors.New("accounting failed")
				}
				return nil
			}})
			require.NoError(t, err)
			_, err = generator.GenerateDraft(t.Context(), LearningRecord{}, nil)
			require.Error(t, err)
			if tc.budgetErr {
				require.Zero(t, provider.calls)
				require.Zero(t, records)
			} else {
				require.Equal(t, 1, provider.calls)
				require.Equal(t, 1, records)
			}
		})
	}
	var nilProvider *evolutionRecordingProvider
	_, err := NewProviderDraftGenerator(ProviderDraftOptions{Provider: nilProvider, Model: "fixture", BudgetGate: func(context.Context) error { return nil }, RecordUsage: func(context.Context, providers.Usage) error { return nil }})
	require.Error(t, err)
}

type evolutionRecordingProvider struct {
	response providers.Response
	calls    int
}

func (p *evolutionRecordingProvider) Name() string              { return "fixture" }
func (p *evolutionRecordingProvider) Models() []providers.Model { return nil }
func (p *evolutionRecordingProvider) Complete(context.Context, providers.Request) (providers.Response, error) {
	p.calls++
	return p.response, nil
}
func (p *evolutionRecordingProvider) Stream(context.Context, providers.Request) (<-chan providers.Chunk, error) {
	return nil, errors.New("not used")
}

func TestEvolutionRealProviderHTTPOmittedUsageRejectedBeforeAccounting(t *testing.T) {
	valid := `{"targetSkillName":"checked-workflow","draftType":"workflow","changeKind":"create","bodyOrPatch":"# Checked workflow\nInspect actual results."}`
	for _, tc := range []struct {
		name         string
		usage        any
		includeUsage bool
		wantError    bool
	}{
		{name: "omitted", wantError: true},
		{name: "null", usage: nil, includeUsage: true, wantError: true},
		{name: "explicit zero", usage: map[string]int{"prompt_tokens": 0, "completion_tokens": 0}, includeUsage: true},
		{name: "nonzero", usage: map[string]int{"prompt_tokens": 9, "completion_tokens": 3}, includeUsage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := map[string]any{"id": "fixture", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": valid}, "finish_reason": "stop"}}}
				if tc.includeUsage {
					response["usage"] = tc.usage
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer upstream.Close()
			recorded := 0
			generator, err := NewProviderDraftGenerator(ProviderDraftOptions{Provider: providers.NewOpenAICompatProvider("fixture", upstream.URL, "fixture-only", nil), Model: "fixture", BudgetGate: func(context.Context) error { return nil }, RecordUsage: func(context.Context, providers.Usage) error { recorded++; return nil }})
			require.NoError(t, err)
			proposal, err := generator.GenerateDraft(t.Context(), LearningRecord{}, nil)
			if tc.wantError {
				require.ErrorContains(t, err, "usage was not reported")
				require.Empty(t, proposal.BodyOrPatch)
				require.Zero(t, recorded)
			} else {
				require.NoError(t, err)
				require.Equal(t, "checked-workflow", proposal.TargetSkillName)
				require.Equal(t, 1, recorded)
			}
		})
	}
	// Older adapters may provide counters without setting the presence marker.
	recorded := 0
	legacy := &evolutionRecordingProvider{response: providers.Response{Content: valid, Usage: providers.Usage{InputTokens: 1}}}
	generator, err := NewProviderDraftGenerator(ProviderDraftOptions{Provider: legacy, Model: "fixture", BudgetGate: func(context.Context) error { return nil }, RecordUsage: func(context.Context, providers.Usage) error { recorded++; return nil }})
	require.NoError(t, err)
	_, err = generator.GenerateDraft(t.Context(), LearningRecord{}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, recorded)
}
