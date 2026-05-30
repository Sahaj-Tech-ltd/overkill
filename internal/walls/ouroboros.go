package walls

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type OuroborosConfig struct {
	Enabled    bool
	Provider   providers.Provider
	Model      string
	StrictMode bool
}

type OuroborosWall struct {
	config OuroborosConfig
}

func NewOuroborosWall(cfg OuroborosConfig) *OuroborosWall {
	return &OuroborosWall{config: cfg}
}

type ouroborosResponse struct {
	Severity    string   `json:"severity"`
	Issues      []string `json:"issues"`
	Suggestions []string `json:"suggestions"`
}

func (w *OuroborosWall) Check(ctx context.Context, code string, spec string) (*WallResult, error) {
	if !w.config.Enabled {
		return &WallResult{
			Wall:     WallOuroboros,
			Passed:   true,
			Severity: SeverityInfo,
			Message:  "Ouroboros wall disabled",
		}, nil
	}

	if w.config.Provider == nil {
		return &WallResult{
			Wall:        WallOuroboros,
			Passed:      false,
			Severity:    SeverityWarning,
			Message:     "Ouroboros provider not configured",
			Suggestions: []string{"configure a separate provider for AI review"},
		}, nil
	}

	systemPrompt := `You are an adversarial code reviewer. Find failure modes, bugs, edge cases. Do NOT approve — find problems. Rate severity: info/warning/block. Output JSON: {"severity":"info|warning|block","issues":["..."],"suggestions":["..."]}`

	userContent := fmt.Sprintf("## Specification\n%s\n\n## Code\n%s", spec, code)

	resp, err := w.config.Provider.Complete(ctx, providers.Request{
		Model:        w.config.Model,
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: "user", Content: userContent},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("walls: ouroboros check failed: %w", err)
	}

	var parsed ouroborosResponse
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		// B106: Treat unparseable LLM JSON as a warning, not a hard error.
		// A model that returns garbled output didn't successfully avoid
		// self-reference, so we flag it but don't crash the wall check.
		return &WallResult{
			Wall:     WallOuroboros,
			Passed:   false,
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("ouroboros response parse failed (treating as warning): %v", err),
		}, nil
	}

	severity := SeverityInfo
	switch parsed.Severity {
	case "warning":
		severity = SeverityWarning
	case "block":
		severity = SeverityBlock
	}

	var passed bool
	effectiveSeverity := severity

	switch severity {
	case SeverityInfo:
		passed = true
		effectiveSeverity = SeverityInfo
	case SeverityWarning:
		passed = false
		effectiveSeverity = SeverityWarning
	case SeverityBlock:
		if w.config.StrictMode {
			passed = false
			effectiveSeverity = SeverityBlock
		} else {
			passed = false
			effectiveSeverity = SeverityWarning
		}
	default:
		passed = true
		effectiveSeverity = SeverityInfo
	}

	// Empty-issues bypass guard: the prior version coerced
	// passed=true whenever Issues was empty, regardless of declared
	// severity. A jailbroken Ouroboros that returns
	// {"severity":"block","issues":[]} could trivially mark itself
	// as passed. Now: empty issues at Info severity are still pass
	// (model truly found nothing), but empty issues at Warning or
	// Block stay failed — the model is contradicting itself, treat
	// it as a fault, not as a pass.
	if len(parsed.Issues) == 0 && severity == SeverityInfo {
		passed = true
		effectiveSeverity = SeverityInfo
	}

	return &WallResult{
		Wall:        WallOuroboros,
		Passed:      passed,
		Severity:    effectiveSeverity,
		Message:     "Ouroboros review complete",
		Details:     parsed.Issues,
		Suggestions: parsed.Suggestions,
	}, nil
}
