package animation

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAll_Covers18Spinners(t *testing.T) {
	if len(All) != 18 {
		t.Errorf("expected 18 spinners, got %d", len(All))
	}
}

func TestByName_Lookup(t *testing.T) {
	s, ok := ByName["braille"]
	if !ok {
		t.Fatal("braille not found")
	}
	if s.Name != "braille" {
		t.Errorf("expected name 'braille', got %q", s.Name)
	}
	if len(s.Frames) != 10 {
		t.Errorf("expected 10 frames, got %d", len(s.Frames))
	}
}

func TestByName_NotFound(t *testing.T) {
	_, ok := ByName["nonexistent"]
	if ok {
		t.Error("expected nonexistent to not be found")
	}
}

func TestAllSpinners_HaveFramesAndInterval(t *testing.T) {
	for _, s := range All {
		if s.Name == "" {
			t.Error("spinner has empty name")
		}
		if len(s.Frames) == 0 {
			t.Errorf("spinner %q has no frames", s.Name)
		}
		if s.Interval <= 0 {
			t.Errorf("spinner %q has non-positive interval: %v", s.Name, s.Interval)
		}
	}
}

func TestAllSpinners_NamesAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, s := range All {
		if seen[s.Name] {
			t.Errorf("duplicate spinner name: %q", s.Name)
		}
		seen[s.Name] = true
	}
}

func TestAnimState_CurrentChar(t *testing.T) {
	s := AnimState{Name: "braille", Frame: 0, Active: true}
	if c := s.CurrentChar(); c != "⠋" {
		t.Errorf("expected '⠋', got %q", c)
	}

	s.Frame = 9
	if c := s.CurrentChar(); c != "⠏" {
		t.Errorf("expected '⠏', got %q", c)
	}

	s.Frame = 10
	if c := s.CurrentChar(); c != "⠋" {
		t.Errorf("expected wrap to '⠋', got %q", c)
	}
}

func TestAnimState_CurrentChar_Unknown(t *testing.T) {
	s := AnimState{Name: "nonexistent", Frame: 0, Active: true}
	if c := s.CurrentChar(); c != "" {
		t.Errorf("expected empty string for unknown spinner, got %q", c)
	}
}

func TestAnimState_CurrentChar_Inactive(t *testing.T) {
	s := AnimState{Name: "braille", Frame: 0, Active: false}
	if c := s.View(); c != "" {
		t.Errorf("expected empty view for inactive, got %q", c)
	}
}

func TestAnimState_View_Active(t *testing.T) {
	s := AnimState{Name: "braille", Frame: 0, Active: true}
	if v := s.View(); v != "⠋ " {
		t.Errorf("expected '⠋ ', got %q", v)
	}
}

func TestStartAnim(t *testing.T) {
	s := AnimState{}
	cmd := StartAnim(&s, "helix")
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}
	if !s.Active {
		t.Error("expected Active to be true")
	}
	if !s.Running {
		t.Error("expected Running to be true")
	}
	if s.Name != "helix" {
		t.Errorf("expected name 'helix', got %q", s.Name)
	}
	if s.Frame != 0 {
		t.Errorf("expected Frame 0, got %d", s.Frame)
	}
}

func TestStartAnim_Unknown(t *testing.T) {
	s := AnimState{}
	_ = StartAnim(&s, "nonexistent")
	if c := s.CurrentChar(); c != "" {
		t.Errorf("expected empty for unknown, got %q", c)
	}
}

func TestStopAnim(t *testing.T) {
	s := AnimState{Active: true, Running: true, Frame: 5}
	StopAnim(&s)
	if s.Active {
		t.Error("expected Active to be false")
	}
	if s.Running {
		t.Error("expected Running to be false")
	}
	if s.Frame != 0 {
		t.Errorf("expected Frame 0, got %d", s.Frame)
	}
}

func TestAdvanceFrame(t *testing.T) {
	s := AnimState{Name: "braille", Active: true, Running: true, Frame: 0}
	_ = AdvanceFrame(&s)
	if s.Frame != 1 {
		t.Errorf("expected Frame 1, got %d", s.Frame)
	}
}

func TestTick_ReturnsFrameTickMsg(t *testing.T) {
	cmd := Tick("braille")
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}

	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()

	select {
	case msg := <-done:
		if msg == nil {
			t.Fatal("expected non-nil message from Tick")
		}
		_, ok := msg.(FrameTickMsg)
		if !ok {
			t.Errorf("expected FrameTickMsg, got %T", msg)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for tick")
	}
}

func TestTick_UnknownFallsBackToBraille(t *testing.T) {
	cmd := Tick("nonexistent")
	if cmd == nil {
		t.Fatal("expected non-nil command for fallback")
	}
}

func TestAnimState_View_Inactive_Empty(t *testing.T) {
	s := AnimState{Name: "helix", Frame: 3, Active: false}
	if v := s.View(); v != "" {
		t.Errorf("expected empty string for inactive, got %q", v)
	}
}

func TestSpecificSpinnerFrames(t *testing.T) {
	tests := []struct {
		name       string
		spinner    Spinner
		frameCount int
	}{
		{"braille", Braille, 10},
		{"braillewave", BrailleWave, 8},
		{"dna", DNA, 12},
		{"scan", Scan, 10},
		{"rain", Rain, 12},
		{"scanline", ScanLine, 6},
		{"pulse", Pulse, 5},
		{"snake", Snake, 16},
		{"sparkle", Sparkle, 6},
		{"cascade", Cascade, 14},
		{"columns", Columns, 26},
		{"orbit", Orbit, 8},
		{"breathe", Breathe, 17},
		{"waverows", WaveRows, 16},
		{"checkerboard", Checkerboard, 4},
		{"helix", Helix, 16},
		{"fillsweep", FillSweep, 11},
		{"diagswipe", DiagSwipe, 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.spinner.Frames) != tt.frameCount {
				t.Errorf("expected %d frames, got %d", tt.frameCount, len(tt.spinner.Frames))
			}
		})
	}
}
