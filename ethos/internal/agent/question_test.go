package agent

import (
	"context"
	"testing"
)

func TestAskQuestion_NoFuncCancels(t *testing.T) {
	a := &Agent{}
	a.allowedTools = make(map[string]bool)
	got := a.AskQuestion(context.Background(), Question{Prompt: "go?"})
	if !got.Cancel {
		t.Fatalf("expected Cancel=true with no func, got %+v", got)
	}
}

func TestAskQuestion_DispatchesToFunc(t *testing.T) {
	a := &Agent{}
	a.allowedTools = make(map[string]bool)
	a.SetQuestionFunc(func(ctx context.Context, q Question) Answer {
		return Answer{Text: "yes", Index: 0}
	})
	got := a.AskQuestion(context.Background(), Question{Prompt: "go?"})
	if got.Text != "yes" {
		t.Fatalf("unexpected answer: %+v", got)
	}
}

func TestSetQuestionFunc_Nil(t *testing.T) {
	a := &Agent{}
	a.allowedTools = make(map[string]bool)
	a.SetQuestionFunc(func(ctx context.Context, q Question) Answer { return Answer{Text: "ok"} })
	a.SetQuestionFunc(nil)
	got := a.AskQuestion(context.Background(), Question{})
	if !got.Cancel {
		t.Fatalf("expected Cancel=true after nil install, got %+v", got)
	}
}
