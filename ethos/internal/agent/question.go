// Package agent — interactive question API.
//
// Mirrors the approval bridge: tools (or other code) can call AskQuestion to
// pause the agent and surface a prompt to the user via QuestionFunc.
package agent

import "context"

// Question describes a question to ask the user.
type Question struct {
	Prompt  string
	Choices []string // free-text if empty
}

// Answer carries the user's response.
type Answer struct {
	Text   string // free text or selected choice
	Index  int    // index into Choices, -1 if free text or no selection
	Cancel bool   // user dismissed
}

// QuestionFunc is the callback invoked to surface a question. If unset, the
// agent returns Answer{Cancel:true}.
type QuestionFunc func(ctx context.Context, q Question) Answer

// SetQuestionFunc installs the callback. Pass nil to disable interactive
// questions.
func (a *Agent) SetQuestionFunc(fn QuestionFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.questionFn = fn
}

// AskQuestion bridges a tool/agent call into the user-facing question UI.
// Blocks until the user answers.
func (a *Agent) AskQuestion(ctx context.Context, q Question) Answer {
	a.mu.RLock()
	fn := a.questionFn
	a.mu.RUnlock()
	if fn == nil {
		return Answer{Cancel: true}
	}
	return fn(ctx, q)
}
