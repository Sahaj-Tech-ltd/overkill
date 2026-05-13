package status

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/animation"
)

func TestToast_Show(t *testing.T) {
	tm := NewToastModel()
	updated, _ := tm.Update(toastShowMsg{message: "test fact"})
	v := updated.View()
	if !strings.Contains(v, "test fact") {
		t.Error("toast should show message")
	}
}

func TestToast_Hide(t *testing.T) {
	// Animations off so hide is immediate (no slide-out window).
	animation.SetEnabled(false)
	defer animation.SetEnabled(true)

	tm := NewToastModel()
	updated, _ := tm.Update(toastShowMsg{message: "test"})
	if !updated.visible {
		t.Error("should be visible")
	}
	updated2, _ := updated.Update(toastHideMsg{})
	if updated2.visible {
		t.Error("should be hidden after hide")
	}
}

func TestToast_SlideInProgresses(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")

	tm := NewToastModel()
	tm.SetWidth(120)
	tm, _ = tm.Update(toastShowMsg{message: "hello"})

	if tm.SlidePos() != 0 {
		t.Fatalf("slide should start at 0, got %v", tm.SlidePos())
	}
	for i := 0; i < SlideFrames; i++ {
		tm, _ = tm.Update(toastSlideTickMsg{})
	}
	if tm.SlidePos() != 1.0 {
		t.Fatalf("slide-in should reach 1.0, got %v", tm.SlidePos())
	}
}

func TestToast_SlideOutReachesZero(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")

	tm := NewToastModel()
	tm.SetWidth(120)
	tm, _ = tm.Update(toastShowMsg{message: "hi"})
	for i := 0; i < SlideFrames; i++ {
		tm, _ = tm.Update(toastSlideTickMsg{})
	}
	tm, _ = tm.Update(toastHideMsg{})
	// Initial slide-out position is 1.0
	if tm.SlidePos() != 1.0 {
		t.Fatalf("slide-out should start at 1.0, got %v", tm.SlidePos())
	}
	for i := 0; i < SlideFrames; i++ {
		tm, _ = tm.Update(toastSlideTickMsg{})
	}
	if tm.visible {
		t.Fatal("toast should be hidden after slide-out completes")
	}
}

func TestToast_SlideSkippedWhenAnimationsOff(t *testing.T) {
	animation.SetEnabled(false)
	defer animation.SetEnabled(true)

	tm := NewToastModel()
	tm.SetWidth(120)
	tm, cmd := tm.Update(toastShowMsg{message: "hi"})
	if tm.SlidePos() != 1.0 {
		t.Fatalf("animations-off show should jump to 1.0, got %v", tm.SlidePos())
	}
	if cmd == nil {
		t.Fatal("animations-off show should still arm the hide timer")
	}
	tm, _ = tm.Update(toastHideMsg{})
	if tm.visible {
		t.Fatal("animations-off hide should immediately hide")
	}
}

func TestToast_ViewOffsetShrinksOverSlideIn(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")

	tm := NewToastModel()
	tm.SetWidth(120)
	tm, _ = tm.Update(toastShowMsg{message: "hello"})
	prev := lipgloss.Width(tm.View())
	for i := 0; i < SlideFrames; i++ {
		tm, _ = tm.Update(toastSlideTickMsg{})
		cur := lipgloss.Width(tm.View())
		if cur > prev {
			t.Fatalf("toast width must not grow during slide-in: %d → %d", prev, cur)
		}
		prev = cur
	}
}

func TestStatusBar_RendersWithoutCrash(t *testing.T) {
	sb := NewStatusBar()
	sb.width = 80
	if sb.View() == "" {
		t.Error("status bar should render")
	}
}
