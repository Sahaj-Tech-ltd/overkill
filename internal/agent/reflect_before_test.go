package agent

import (
	"testing"
)

func TestReflectBeforeAction_ResearchWithTTS(t *testing.T) {
	input := "Dense research paper about transformer attention mechanisms and their implications for long-context LLMs."
	user := UserModel{HasADHD: true, IsOnMobile: false}
	inventory := NewToolInventory()

	result := ReflectBeforeAction(input, user, inventory)

	if result.ModalityHint != ModalityOfferAudio {
		t.Errorf("expected ModalityOfferAudio for research + ADHD + TTS, got %s", result.ModalityHint)
	}
	if result.Classification.Type != ContentResearchDense {
		t.Errorf("expected research_dense classification, got %s", result.Classification.Type)
	}
}

func TestReflectBeforeAction_ResearchNoTTS(t *testing.T) {
	input := "Dense research paper about transformer attention mechanisms and their implications for long-context LLMs."
	user := UserModel{HasADHD: true, IsOnMobile: false}
	// Empty inventory — no TTS.
	inventory := &ToolInventory{Affordances: map[string]ToolAffordance{}}

	result := ReflectBeforeAction(input, user, inventory)

	if result.ModalityHint != ModalityDefault {
		t.Errorf("expected ModalityDefault when no TTS, got %s", result.ModalityHint)
	}
}

func TestReflectBeforeAction_Emotional(t *testing.T) {
	input := "I'm so frustrated and overwhelmed with everything."
	user := UserModel{HasADHD: true}
	inventory := NewToolInventory()

	result := ReflectBeforeAction(input, user, inventory)

	if result.ModalityHint != ModalityMinimalText {
		t.Errorf("expected ModalityMinimalText for emotional, got %s", result.ModalityHint)
	}
}

func TestReflectBeforeAction_CodeReview(t *testing.T) {
	input := "Fix the bug in the auth middleware function signature."
	user := UserModel{}
	inventory := NewToolInventory()

	result := ReflectBeforeAction(input, user, inventory)

	if result.ModalityHint != ModalityStructured {
		t.Errorf("expected ModalityStructured for code review, got %s", result.ModalityHint)
	}
}

func TestReflectBeforeAction_NewsDigestMobile(t *testing.T) {
	input := "📊 **AI Digest** — Top papers this week in large language model research."
	user := UserModel{IsOnMobile: true}
	inventory := NewToolInventory()

	result := ReflectBeforeAction(input, user, inventory)

	if result.ModalityHint != ModalityAudioPrimary {
		t.Errorf("expected ModalityAudioPrimary for news digest on mobile, got %s", result.ModalityHint)
	}
}

func TestReflectBeforeAction_MultiItemDecomposition(t *testing.T) {
	input := "Fix auth bug\n- Add rate limiting\n- Update README"
	user := UserModel{}
	inventory := NewToolInventory()

	result := ReflectBeforeAction(input, user, inventory)

	if !result.ShouldDecompose {
		t.Error("expected ShouldDecompose for multi-item input")
	}
}

func TestReflectBeforeAction_CasualChat(t *testing.T) {
	input := "hey what's up"
	user := UserModel{}
	inventory := NewToolInventory()

	result := ReflectBeforeAction(input, user, inventory)

	if result.ModalityHint != ModalityDefault {
		t.Errorf("expected ModalityDefault for casual chat, got %s", result.ModalityHint)
	}
}

func TestModalitySystemHint(t *testing.T) {
	tests := []struct {
		hint     ModalityHint
		contains string
	}{
		{ModalityOfferAudio, "offer to read this aloud"},
		{ModalityMinimalText, "keep it short"},
		{ModalityStructured, "structured format"},
		{ModalityAudioPrimary, "audio-first"},
		{ModalityDefault, ""},
	}

	for _, tt := range tests {
		sr := SituationalReflection{ModalityHint: tt.hint}
		got := sr.ModalitySystemHint()
		if tt.contains != "" && !contains(got, tt.contains) {
			t.Errorf("ModalitySystemHint for %s: expected to contain %q, got %q", tt.hint, tt.contains, got)
		}
		if tt.contains == "" && got != "" {
			t.Errorf("ModalitySystemHint for %s: expected empty, got %q", tt.hint, got)
		}
	}
}

func TestToolInventory_BestForType(t *testing.T) {
	inv := NewToolInventory()

	// Research should match edge_tts and web_search.
	tools := inv.BestForType(ContentResearchDense)
	if len(tools) == 0 {
		t.Error("expected tools for research_dense")
	}
	// edge_tts should be first (sorted to top for TTS).
	if tools[0].Name != "edge_tts" {
		t.Errorf("expected edge_tts first, got %s", tools[0].Name)
	}
}

func TestToolInventory_IsAvailable(t *testing.T) {
	inv := NewToolInventory()

	if !inv.IsAvailable("edge_tts") {
		t.Error("edge_tts should be available")
	}
	if inv.IsAvailable("nonexistent") {
		t.Error("nonexistent should not be available")
	}
}
