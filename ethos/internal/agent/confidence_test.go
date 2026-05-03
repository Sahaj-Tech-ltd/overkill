package agent

import (
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

func makeHistory(n int) []providers.Message {
	msgs := make([]providers.Message, n)
	for i := range msgs {
		msgs[i] = providers.Message{
			Role:    "user",
			Content: "do something",
		}
	}
	return msgs
}

func makeHistoryWithTools(n int) []providers.Message {
	msgs := make([]providers.Message, n)
	for i := range msgs {
		msgs[i] = providers.Message{
			Role:    "assistant",
			Content: "running tests",
			ToolCalls: []providers.ToolCall{
				{Name: "pytest", Arguments: "{}"},
			},
		}
	}
	return msgs
}

func TestAssessConfidence_HighForFamiliarTask(t *testing.T) {
	history := makeHistory(12)
	ca := AssessConfidence("fix the login bug", history, "claude-3-opus")
	if ca.Level != ConfidenceHigh {
		t.Errorf("expected High, got %s (score=%.2f)", ca.Level, ca.Score)
	}
	if ca.Score < 0.7 {
		t.Errorf("expected score >= 0.7, got %.2f", ca.Score)
	}
}

func TestAssessConfidence_LowForNovelTask(t *testing.T) {
	history := makeHistory(1)
	ca := AssessConfidence("transmogrify the frobnitz", history, "gpt-4o")
	if ca.Level != ConfidenceLow {
		t.Errorf("expected Low, got %s (score=%.2f)", ca.Level, ca.Score)
	}
	if ca.Score >= 0.4 {
		t.Errorf("expected score < 0.4, got %.2f", ca.Score)
	}
}

func TestAssessConfidence_UnknownForNoModel(t *testing.T) {
	ca := AssessConfidence("fix something", makeHistory(10), "")
	if ca.Level != ConfidenceUnknown {
		t.Errorf("expected Unknown, got %s", ca.Level)
	}
	if ca.Score != 0.0 {
		t.Errorf("expected score 0.0, got %.2f", ca.Score)
	}
}

func TestAssessConfidence_MediumForModerateContext(t *testing.T) {
	history := makeHistory(5)
	ca := AssessConfidence("refactor the handler", history, "gpt-4o")
	if ca.Level != ConfidenceMedium {
		t.Errorf("expected Medium, got %s (score=%.2f)", ca.Level, ca.Score)
	}
}

func TestFormatConfidence_ProducesReadableOutput(t *testing.T) {
	ca := AssessConfidence("explain the code", makeHistory(8), "gemini-pro")
	formatted := FormatConfidence(ca)
	if !strings.Contains(formatted, "%") {
		t.Errorf("expected percentage in output, got: %s", formatted)
	}
	if !strings.Contains(formatted, "confident") {
		t.Errorf("expected 'confident' in output, got: %s", formatted)
	}
}

func TestFormatConfidence_Unknown(t *testing.T) {
	ca := AssessConfidence("anything", makeHistory(5), "")
	formatted := FormatConfidence(ca)
	if !strings.Contains(formatted, "unknown") {
		t.Errorf("expected 'unknown' in output, got: %s", formatted)
	}
}

func TestConfidenceLevel_String(t *testing.T) {
	cases := map[ConfidenceLevel]string{
		ConfidenceHigh:    "high",
		ConfidenceMedium:  "medium",
		ConfidenceLow:     "low",
		ConfidenceUnknown: "unknown",
	}
	for level, want := range cases {
		got := level.String()
		if got != want {
			t.Errorf("ConfidenceLevel(%d).String() = %q, want %q", level, got, want)
		}
	}
}

func TestAssessConfidence_ClampsScore(t *testing.T) {
	ca := AssessConfidence("test this", makeHistoryWithTools(20), "claude-3-opus")
	if ca.Score < 0.0 {
		t.Errorf("score below 0.0: %.2f", ca.Score)
	}
	if ca.Score > 1.0 {
		t.Errorf("score above 1.0: %.2f", ca.Score)
	}

	ca2 := AssessConfidence("xyz", makeHistory(0), "gpt-4o")
	if ca2.Score < 0.0 {
		t.Errorf("score below 0.0: %.2f", ca2.Score)
	}
	if ca2.Score > 1.0 {
		t.Errorf("score above 1.0: %.2f", ca2.Score)
	}
}
