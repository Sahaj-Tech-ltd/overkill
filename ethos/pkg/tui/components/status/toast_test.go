package status

import (
	"testing"

	tuitypes "github.com/Sahaj-Tech-ltd/ethos/pkg/tui/types"
)

func TestPersonality_StatusBar(t *testing.T) {
	sb := NewStatusBar()
	sb.personalityMode = "witty"
	v := sb.View()
	if !containsStr(v, "Witty") && !containsStr(v, "witty") && !containsStr(v, "Chillaxin") {
		t.Errorf("should show personality mode somewhere in: %q", v)
	}
}

func TestPersonality_SwitchMode(t *testing.T) {
	sb := NewStatusBar()
	sb.personalityMode = "subtle"
	v1 := sb.View()
	sb.personalityMode = "full"
	v2 := sb.View()
	if v1 == "" || v2 == "" {
		t.Error("views should not be empty")
	}
}

func TestPersonality_FunFactToast(t *testing.T) {
	tModel := NewToastModel()
	updated, _ := tModel.Update(toastShowMsg{message: "test fact"})
	v := updated.View()
	if !containsStr(v, "test fact") {
		t.Error("toast should show message")
	}
}

func TestPersonality_FunFactTimer(t *testing.T) {
	tModel := NewToastModel()
	updated, _ := tModel.Update(toastShowMsg{message: "test"})
	if !updated.visible {
		t.Error("should be visible")
	}
	updated2, _ := updated.Update(toastHideMsg{})
	if updated2.visible {
		t.Error("should be hidden after hide")
	}
}

func TestPersonality_Relationship(t *testing.T) {
	sb := NewStatusBar()
	v := sb.View()
	if v == "" {
		t.Error("should render")
	}
}

func TestPersonality_WittyMessages(t *testing.T) {
	sb := NewStatusBar()
	sb.personalityMode = "witty"
	sb.state = tuitypes.StatusIdle
	v := sb.View()
	if v == "" {
		t.Error("should render")
	}
}

func TestPersonality_OffMode(t *testing.T) {
	sb := NewStatusBar()
	sb.personalityMode = "off"
	v := sb.View()
	if v == "" {
		t.Error("should render")
	}
}

func TestPersonality_FullMode(t *testing.T) {
	sb := NewStatusBar()
	sb.personalityMode = "full"
	sb.state = tuitypes.StatusIdle
	v := sb.View()
	if v == "" {
		t.Error("should render")
	}
}
