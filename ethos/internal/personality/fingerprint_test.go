package personality

import (
	"testing"
)

func TestDetect_ClaudeOpus(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("claude-3-opus-20240229")
	if fp.Family != "claude-opus" {
		t.Errorf("expected family claude-opus, got %s", fp.Family)
	}
	if fp.ContextWindow != 200000 {
		t.Errorf("expected context window 200000, got %d", fp.ContextWindow)
	}
}

func TestDetect_ClaudeSonnet(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("claude-3-5-sonnet-20241022")
	if fp.Family != "claude-sonnet" {
		t.Errorf("expected family claude-sonnet, got %s", fp.Family)
	}
}

func TestDetect_GPT4o(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("gpt-4o")
	if fp.Family != "gpt-4o" {
		t.Errorf("expected family gpt-4o, got %s", fp.Family)
	}
	if fp.ContextWindow != 128000 {
		t.Errorf("expected context window 128000, got %d", fp.ContextWindow)
	}
}

func TestDetect_GPT4Turbo(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("gpt-4-turbo")
	if fp.Family != "gpt-4" {
		t.Errorf("expected family gpt-4, got %s", fp.Family)
	}
}

func TestDetect_GPT35Turbo(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("gpt-3.5-turbo")
	if fp.Family != "gpt-3.5" {
		t.Errorf("expected family gpt-3.5, got %s", fp.Family)
	}
	if fp.ContextWindow != 16385 {
		t.Errorf("expected context window 16385, got %d", fp.ContextWindow)
	}
}

func TestDetect_GeminiPro(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("gemini-1.5-pro")
	if fp.Family != "gemini-pro" {
		t.Errorf("expected family gemini-pro, got %s", fp.Family)
	}
	if fp.ContextWindow != 1000000 {
		t.Errorf("expected context window 1000000, got %d", fp.ContextWindow)
	}
}

func TestDetect_GeminiFlash(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("gemini-2.0-flash")
	if fp.Family != "gemini-flash" {
		t.Errorf("expected family gemini-flash, got %s", fp.Family)
	}
}

func TestDetect_Llama(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("llama-3.1-70b")
	if fp.Family != "llama" {
		t.Errorf("expected family llama, got %s", fp.Family)
	}
}

func TestDetect_MistralLarge(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("mistral-large")
	if fp.Family != "mistral-large" {
		t.Errorf("expected family mistral-large, got %s", fp.Family)
	}
}

func TestDetect_DeepseekCoder(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("deepseek-coder-v2")
	if fp.Family != "deepseek-coder" {
		t.Errorf("expected family deepseek-coder, got %s", fp.Family)
	}
}

func TestDetect_UnknownModel(t *testing.T) {
	ft := NewFingerprintTracker()
	fp := ft.Detect("custom-model-v1")
	if fp.Family != "custom-model-v1" {
		t.Errorf("expected family custom-model-v1, got %s", fp.Family)
	}
	if fp.ContextWindow != defaultContextWindow {
		t.Errorf("expected context window %d, got %d", defaultContextWindow, fp.ContextWindow)
	}
}

func TestHasChanged_TrueOnFamilyChange(t *testing.T) {
	ft := NewFingerprintTracker()
	ft.Update(ft.Detect("claude-3-opus-20240229"))
	if ft.HasChanged("gpt-4o") != true {
		t.Error("expected HasChanged to return true when switching from claude to gpt")
	}
}

func TestHasChanged_FalseOnSameFamily(t *testing.T) {
	ft := NewFingerprintTracker()
	ft.Update(ft.Detect("claude-3-sonnet-20240229"))
	if ft.HasChanged("claude-3.5-sonnet-20241022") != false {
		t.Error("expected HasChanged to return false when staying within claude-sonnet family")
	}
}

func TestHasChanged_FalseOnNil(t *testing.T) {
	ft := NewFingerprintTracker()
	if ft.HasChanged("anything") != false {
		t.Error("expected HasChanged to return false when current is nil")
	}
}

func TestCalibratePrompt_ReturnsMessage(t *testing.T) {
	ft := NewFingerprintTracker()
	ft.Update(ft.Detect("claude-3-opus-20240229"))
	ft.Update(ft.Detect("gpt-4o"))
	prompt := ft.CalibratePrompt()
	if prompt == "" {
		t.Error("expected non-empty calibration prompt after family change")
	}
	expected := "Model changed from claude-opus to gpt-4o. Running quick calibration to adjust capabilities."
	if prompt != expected {
		t.Errorf("expected %q, got %q", expected, prompt)
	}
}

func TestCalibratePrompt_EmptyOnNoChange(t *testing.T) {
	ft := NewFingerprintTracker()
	ft.Update(ft.Detect("claude-3-opus-20240229"))
	prompt := ft.CalibratePrompt()
	if prompt != "" {
		t.Errorf("expected empty calibration prompt on first update, got %q", prompt)
	}
}

func TestCalibratePrompt_EmptyOnSameFamily(t *testing.T) {
	ft := NewFingerprintTracker()
	ft.Update(ft.Detect("claude-3-sonnet-20240229"))
	ft.Update(ft.Detect("claude-3.5-sonnet-20241022"))
	prompt := ft.CalibratePrompt()
	if prompt != "" {
		t.Errorf("expected empty calibration prompt on same family update, got %q", prompt)
	}
}

func TestUpdate_StoresFingerprint(t *testing.T) {
	ft := NewFingerprintTracker()
	fp1 := ft.Detect("claude-3-opus-20240229")
	ft.Update(fp1)
	if ft.Current() != fp1 {
		t.Error("expected current to be fp1 after first update")
	}
	if ft.Previous() != nil {
		t.Error("expected previous to be nil after first update")
	}
	fp2 := ft.Detect("gpt-4o")
	ft.Update(fp2)
	if ft.Current() != fp2 {
		t.Error("expected current to be fp2 after second update")
	}
	if ft.Previous() != fp1 {
		t.Error("expected previous to be fp1 after second update")
	}
}

func TestContextWindow_KnownModels(t *testing.T) {
	ft := NewFingerprintTracker()
	cases := []struct {
		modelID   string
		wantWindow int
	}{
		{"claude-3-opus-20240229", 200000},
		{"claude-3-5-sonnet-20241022", 200000},
		{"claude-3-haiku-20240307", 200000},
		{"gpt-4o", 128000},
		{"gpt-4-turbo", 128000},
		{"gpt-3.5-turbo", 16385},
		{"gemini-1.5-pro", 1000000},
		{"gemini-2.0-flash", 1000000},
		{"llama-3.1-70b", defaultContextWindow},
		{"unknown-model", defaultContextWindow},
	}
	for _, tc := range cases {
		fp := ft.Detect(tc.modelID)
		if fp.ContextWindow != tc.wantWindow {
			t.Errorf("Detect(%q): expected context window %d, got %d", tc.modelID, tc.wantWindow, fp.ContextWindow)
		}
	}
}

func TestUpdate_SetsChangedFlag(t *testing.T) {
	ft := NewFingerprintTracker()
	ft.Update(ft.Detect("claude-3-opus-20240229"))
	if ft.changed {
		t.Error("expected changed=false on first update (no previous)")
	}
	ft.Update(ft.Detect("gpt-4o"))
	if !ft.changed {
		t.Error("expected changed=true when family changed")
	}
	ft.Update(ft.Detect("gpt-4o-mini"))
	if ft.changed {
		t.Error("expected changed=false when family stayed the same")
	}
}
