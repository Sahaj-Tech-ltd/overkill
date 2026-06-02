package agent

import (
	"testing"
)

func TestContentClassifier_Classify_ResearchDense(t *testing.T) {
	cc := NewContentClassifier()
	input := `I found this paper on arXiv about large language model alignment. 
The methodology uses RLHF with constitutional AI. Key findings suggest 
that chain-of-thought reasoning improves safety by 40%. The study 
was published in the Journal of Machine Learning Research.`

	result := cc.Classify(input)

	if result.Type != ContentResearchDense {
		t.Errorf("expected research_dense, got %s", result.Type)
	}
	if result.Confidence < 0.3 {
		t.Errorf("confidence too low: %f", result.Confidence)
	}
}

func TestContentClassifier_Classify_EmotionalVent(t *testing.T) {
	cc := NewContentClassifier()
	input := "I'm so frustrated and overwhelmed with this project. I'm exhausted and can't deal with the constant changes. I hate this."

	result := cc.Classify(input)

	if result.Type != ContentEmotionalVent {
		t.Errorf("expected emotional_vent, got %s", result.Type)
	}
}

func TestContentClassifier_Classify_CodeReview(t *testing.T) {
	cc := NewContentClassifier()
	input := "Can you fix the bug in the auth middleware? The function signature is wrong and the error handling panics. Review the PR for the refactored interface."

	result := cc.Classify(input)

	if result.Type != ContentCodeReview {
		t.Errorf("expected code_review, got %s", result.Type)
	}
}

func TestContentClassifier_Classify_MultiItem(t *testing.T) {
	cc := NewContentClassifier()
	input := "Fix the auth bug in middleware\n- Add rate limiting to all endpoints\n- Update the README with new config options"

	result := cc.Classify(input)

	if result.Type != ContentMultiItem {
		t.Errorf("expected multi_item, got %s", result.Type)
	}
	if result.ItemCount < 2 {
		t.Errorf("expected at least 2 items, got %d", result.ItemCount)
	}
}

func TestContentClassifier_Classify_CasualChat(t *testing.T) {
	cc := NewContentClassifier()
	input := "hey how are you doing today"

	result := cc.Classify(input)

	if result.Type != ContentCasualChat {
		t.Errorf("expected casual_chat, got %s", result.Type)
	}
}

func TestContentClassifier_Classify_NewsDigest(t *testing.T) {
	cc := NewContentClassifier()
	input := "📊 **Research Digest** — May 28\nTop papers this week in AI research..."

	result := cc.Classify(input)

	if result.Type != ContentNewsDigest {
		t.Errorf("expected news_digest, got %s", result.Type)
	}
}

func TestContentClassifier_Classify_Instruction(t *testing.T) {
	cc := NewContentClassifier()
	input := "send the weekly report to the team"

	result := cc.Classify(input)

	if result.Type != ContentInstruction {
		t.Errorf("expected instruction, got %s", result.Type)
	}
}

func TestContentClassifier_EdgeCases(t *testing.T) {
	cc := NewContentClassifier()

	tests := []struct {
		name     string
		input    string
		wantType ContentType
	}{
		{"empty", "", ContentInstruction},
		{"short", "ok", ContentInstruction},
		{"single word", "deploy", ContentInstruction},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cc.Classify(tt.input)
			if result.Type != tt.wantType {
				t.Errorf("got %s, want %s", result.Type, tt.wantType)
			}
		})
	}
}
