package main

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/reflect"
)

// reflectorAdapter satisfies agent.Reflector by delegating to
// internal/reflect.HeuristicReflector. The adapter lives here so the
// agent package stays free of reflect imports — same pattern as
// verifier_adapter.go for PostWriteVerifier.
//
// failHypoSink, when non-nil, lets each reflection also land in the
// failed-hypothesis store. The agent itself doesn't know about that
// store — the wiring layer connects them.
type reflectorAdapter struct {
	inner       reflect.Reflector
	failHypoFn  func(toolName string, r reflect.Reflection)
}

func newReflectorAdapter(fh func(toolName string, r reflect.Reflection)) *reflectorAdapter {
	return &reflectorAdapter{
		inner:      reflect.HeuristicReflector{},
		failHypoFn: fh,
	}
}

func (a *reflectorAdapter) IsFailure(toolName, output, errStr string) bool {
	if errStr != "" {
		return true
	}
	return reflect.IsFailureOutput(toolName, output)
}

func (a *reflectorAdapter) Reflect(f agent.Failure) agent.Reflection {
	r := a.inner.Reflect(reflect.Failure{
		ToolName: f.ToolName,
		Input:    f.Input,
		Output:   f.Output,
		Error:    f.Error,
	})
	if a.failHypoFn != nil {
		a.failHypoFn(f.ToolName, r)
	}
	return agent.Reflection{
		Mode:       string(r.Mode),
		RootCause:  r.RootCause,
		Hypothesis: r.Hypothesis,
		Confidence: r.Confidence,
	}
}

func (a *reflectorAdapter) FormatNote(toolName string, r agent.Reflection) string {
	return reflect.FormatSystemNote(toolName, reflect.Reflection{
		Mode:       reflect.FailureMode(r.Mode),
		RootCause:  r.RootCause,
		Hypothesis: r.Hypothesis,
		Confidence: r.Confidence,
	})
}
