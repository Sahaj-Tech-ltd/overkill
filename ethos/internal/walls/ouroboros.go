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
		return nil, fmt.Errorf("walls: ouroboros response parse failed: %w", err)
	}

	severity := SeverityInfo
	switch parsed.Severity {
	case "warning":
		severity = SeverityWarning
	case "block":
		severity = SeverityBlock
	}

	passed := true
	effectiveSeverity := severity
	if !w.config.StrictMode && severity == SeverityBlock {
		effectiveSeverity = SeverityWarning
	}
	if w.config.StrictMode && (severity == SeverityBlock || severity == SeverityWarning) {
		passed = false
	}
	if !w.config.StrictMode && severity == SeverityWarning {
		passed = false
	}
	if severity == SeverityInfo {
		passed = true
	}
	if len(parsed.Issues) == 0 {
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
