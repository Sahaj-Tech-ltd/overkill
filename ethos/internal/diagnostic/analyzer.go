package diagnostic

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type Analyzer struct {
	provider providers.Provider
	model    string
}

func NewAnalyzer(provider providers.Provider, model string) *Analyzer {
	return &Analyzer{
		provider: provider,
		model:    model,
	}
}

type AnalyzeRequest struct {
	Error         string            `json:"error"`
	ErrorOutput   string            `json:"error_output"`
	ModifiedFiles []string          `json:"modified_files"`
	FileContents  map[string]string `json:"file_contents"`
	RecentActions []string          `json:"recent_actions"`
}

func (a *Analyzer) Analyze(ctx context.Context, req AnalyzeRequest) (*DiagnosticReport, error) {
	report := &DiagnosticReport{
		Error:     req.Error,
		ErrorType: a.ClassifyError(req.ErrorOutput),
		Timestamp: time.Now(),
	}

	for _, path := range req.ModifiedFiles {
		fi := FileInfo{
			Path:     path,
			Modified: true,
		}
		if content, ok := req.FileContents[path]; ok {
			fr := AnalyzeFile(path, content)
			fi.Role = fr.Role
		}
		report.AffectedFiles = append(report.AffectedFiles, fi)
	}

	prompt := buildAnalysisPrompt(req, report)
	llmResp, err := a.callLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("diagnostic: %w", err)
	}

	causes, approaches := parseLLMResponse(llmResp)
	report.RootCauses = causes
	report.Approaches = approaches

	sort.Slice(report.Approaches, func(i, j int) bool {
		return report.Approaches[i].Confidence > report.Approaches[j].Confidence
	})

	if len(report.Approaches) > 0 {
		best := report.Approaches[0]
		report.Confidence = best.Confidence
		report.Recommendation = fmt.Sprintf("Approach #%d: %s", best.ID, best.Description)
	}

	if report.Confidence == 0 && len(report.RootCauses) > 0 {
		report.Confidence = report.RootCauses[0].Confidence
	}

	return report, nil
}

func (a *Analyzer) ClassifyError(errMsg string) string {
	lower := strings.ToLower(errMsg)

	switch {
	case containsAny(lower,
		"--- fail", "test failed", "fail\t",
	):
		return "test"
	case containsAny(lower,
		"panic:", "nil pointer", "index out of range",
		"sigsegv", "segmentation fault", "fatal error:",
		"goroutine ", "deadlock",
	):
		return "runtime"
	case containsAny(lower,
		"compile error", "syntax error", "undefined:",
		"cannot find", "not used", "imported and not used",
		"expected ", "unexpected ", "missing return",
	):
		return "compile"
	case containsAny(lower,
		"lint", "style", "format", "vet ",
	):
		return "lint"
	}

	return "unknown"
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func buildAnalysisPrompt(req AnalyzeRequest, report *DiagnosticReport) string {
	var sb strings.Builder

	sb.WriteString("Error: ")
	sb.WriteString(req.Error)
	sb.WriteString("\n\nError Type: ")
	sb.WriteString(report.ErrorType)
	sb.WriteString("\n\n")

	if req.ErrorOutput != "" {
		sb.WriteString("Full Error Output:\n```\n")
		sb.WriteString(req.ErrorOutput)
		sb.WriteString("\n```\n\n")
	}

	if len(report.AffectedFiles) > 0 {
		sb.WriteString("Modified Files:\n")
		for _, f := range report.AffectedFiles {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", f.Path, f.Role))
		}
		sb.WriteString("\n")
	}

	if len(req.FileContents) > 0 {
		sb.WriteString("File Contents:\n")
		for path, content := range req.FileContents {
			sb.WriteString(fmt.Sprintf("### %s\n```\n%s\n```\n\n", path, content))
		}
	}

	if len(req.RecentActions) > 0 {
		sb.WriteString("Recent Actions:\n")
		for _, action := range req.RecentActions {
			sb.WriteString(fmt.Sprintf("- %s\n", action))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func (a *Analyzer) callLLM(ctx context.Context, userPrompt string) (string, error) {
	systemPrompt := `You are a debugging diagnostician. Given an error and relevant code, identify root causes with confidence scores. For each root cause, propose fix approaches with: confidence (0-1), risk level, steps. Output structured JSON with this exact format:

{
  "root_causes": [
    {
      "description": "what went wrong",
      "file": "file path",
      "line": 0,
      "confidence": 0.9,
      "evidence": ["evidence1", "evidence2"]
    }
  ],
  "approaches": [
    {
      "id": 1,
      "description": "fix description",
      "steps": ["step1", "step2"],
      "confidence": 0.85,
      "risk": "low",
      "estimated_time": "5 minutes",
      "files_to_change": ["file1.go"]
    }
  ]
}

Return ONLY the JSON object, no markdown fences or explanation.`

	resp, err := a.provider.Complete(ctx, providers.Request{
		Model:        a.model,
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.1,
		MaxTokens:   2048,
	})
	if err != nil {
		return "", fmt.Errorf("llm call: %w", err)
	}

	return resp.Content, nil
}

func parseLLMResponse(content string) ([]RootCause, []Approach) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result struct {
		RootCauses []RootCause `json:"root_causes"`
		Approaches []Approach  `json:"approaches"`
	}

	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return fallbackParse(content)
	}

	return result.RootCauses, result.Approaches
}

func fallbackParse(content string) ([]RootCause, []Approach) {
	causes := []RootCause{
		{
			Description: "unable to parse LLM diagnostic response",
			Confidence:  0.3,
			Evidence:    []string{"raw response: " + truncate(content, 200)},
		},
	}
	return causes, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
