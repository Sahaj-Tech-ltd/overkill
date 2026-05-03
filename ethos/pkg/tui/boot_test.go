package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/personality"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/components/animation"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/components/logo"
)

func TestBoot_LoadSoulMD(t *testing.T) {
	dir := t.TempDir()
	soulDir := filepath.Join(dir, ".ethos", "memories")
	os.MkdirAll(soulDir, 0755)
	soulFile := filepath.Join(soulDir, "soul.md")
	os.WriteFile(soulFile, []byte("test soul content"), 0644)
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	cmd := LoadBootData(nil)
	msg := cmd()
	boot, ok := msg.(BootCompleteMsg)
	if !ok {
		t.Fatal("wrong message type")
	}
	if boot.SoulMD != "test soul content" {
		t.Errorf("soul md mismatch: got %q", boot.SoulMD)
	}
}

func TestBoot_NoFunFactWithoutPersonality(t *testing.T) {
	cmd := LoadBootData(nil)
	msg := cmd()
	boot, ok := msg.(BootCompleteMsg)
	if !ok {
		t.Fatal("wrong message type")
	}
	if boot.FunFact != "" {
		t.Errorf("expected no fun fact without personality, got %q", boot.FunFact)
	}
}

func TestBoot_BootView(t *testing.T) {
	// Animations off so View renders the final, fully-revealed state
	// without driving the tick loop.
	animation.SetEnabled(false)
	defer animation.SetEnabled(true)

	b := &BootModel{visible: true, width: 80, height: 24}
	b.funFact = "test fact"
	v := b.View()
	if !containsStr(v, "test fact") {
		t.Error("should show fun fact")
	}
	if !containsStr(v, "press any key") {
		t.Error("should show dismiss hint")
	}
}

func TestBoot_BootViewHidden(t *testing.T) {
	b := &BootModel{visible: false}
	if b.View() != "" {
		t.Error("hidden boot should render empty")
	}
}

func TestBoot_EmptySoulMD(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("HOME", dir)
	defer os.Unsetenv("HOME")

	cmd := LoadBootData(nil)
	msg := cmd()
	boot := msg.(BootCompleteMsg)
	if boot.SoulMD != "" {
		t.Error("should be empty without file")
	}
}

func TestBoot_FadeRamps(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")

	b := BootModel{visible: true, width: 80, height: 24}
	b.StartBootAnimation()
	if b.fadeStep != 0 {
		t.Fatalf("fade should start at 0, got %d", b.fadeStep)
	}
	for i := 0; i < BootFadeFrames; i++ {
		var cmd interface{}
		b, cmd = b.UpdateBoot(bootFadeTickMsg{})
		_ = cmd
	}
	if b.fadeStep != BootFadeFrames {
		t.Fatalf("fade should reach %d, got %d", BootFadeFrames, b.fadeStep)
	}
}

func TestBoot_TypewriterAdvances(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")

	b := BootModel{visible: true, width: 80, height: 24}
	// jump fade past 60% so the typewriter is allowed to advance
	b.fadeStep = BootFadeFrames
	prev := b.typedChars
	b, _ = b.UpdateBoot(bootTypeTickMsg{})
	if b.typedChars != prev+1 {
		t.Fatalf("typewriter should advance by 1, got %d → %d", prev, b.typedChars)
	}
	for i := 0; i < len(logo.Subtitle)+5; i++ {
		b, _ = b.UpdateBoot(bootTypeTickMsg{})
	}
	if b.typedChars != len(logo.Subtitle) {
		t.Fatalf("typewriter should clamp at subtitle length, got %d", b.typedChars)
	}
}

func TestBoot_AnimationsOffJumpsToFinalState(t *testing.T) {
	animation.SetEnabled(false)
	defer animation.SetEnabled(true)

	b := BootModel{visible: true, width: 80, height: 24}
	cmd := b.StartBootAnimation()
	if cmd != nil {
		t.Fatal("animations-off start should return nil cmd")
	}
	if b.fadeStep != BootFadeFrames {
		t.Fatal("fade should be fully done when disabled")
	}
	if b.typedChars != len(logo.Subtitle) {
		t.Fatal("typewriter should be done when disabled")
	}
	if !b.blinkOn {
		t.Fatal("hint should be visible when disabled")
	}
}

func TestBoot_TotalSequenceFitsBudget(t *testing.T) {
	// The three animations overlap: typewriter starts at 60% of fade;
	// blink starts after typewriter completes. Compute the actual wall
	// time and ensure it stays under the 1.2s auto-dismiss.
	fade := time.Duration(BootFadeFrames) * bootFadeInterval
	typeStart := fade * 6 / 10
	typeEnd := typeStart + time.Duration(len(logo.Subtitle))*bootTypeInterval
	blinkEnd := typeEnd + time.Duration(bootBlinkFrames*2)*bootBlinkInterval
	end := fade
	if typeEnd > end {
		end = typeEnd
	}
	if blinkEnd > end {
		end = blinkEnd
	}
	if end >= 1200*time.Millisecond {
		t.Fatalf("boot animation budget %v exceeds 1.2s auto-dismiss", end)
	}
}

func TestBoot_PersonalityProvidesFunFact(t *testing.T) {
	p := personality.New(personality.Config{Level: personality.LevelFull})
	cmd := LoadBootData(p)
	msg := cmd()
	boot := msg.(BootCompleteMsg)
	if boot.FunFact == "" {
		t.Error("should have fun fact when personality supplied")
	}
}
