package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakePipelineRunner struct {
	out json.RawMessage
	err error
}

func (f fakePipelineRunner) Run(ctx context.Context, request string) (json.RawMessage, error) {
	return f.out, f.err
}

func TestPipelineTool_NoRunner(t *testing.T) {
	tool := NewPipelineTool(nil)
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{"request":"x"}`))
	if !strings.Contains(string(got), "not configured") {
		t.Errorf("expected not-configured error, got %s", got)
	}
}

func TestPipelineTool_EmptyRequest(t *testing.T) {
	tool := NewPipelineTool(fakePipelineRunner{})
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(got), "request is required") {
		t.Errorf("expected request-required error, got %s", got)
	}
}

func TestPipelineTool_PassthroughResult(t *testing.T) {
	expected := json.RawMessage(`{"stages":["spec","test","code","refactor"],"success":true}`)
	tool := NewPipelineTool(fakePipelineRunner{out: expected})
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"request":"add a hello-world endpoint"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(expected) {
		t.Errorf("expected passthrough %s, got %s", expected, got)
	}
}

func TestPipelineTool_RunnerError(t *testing.T) {
	tool := NewPipelineTool(fakePipelineRunner{err: errors.New("provider down")})
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{"request":"x"}`))
	if !strings.Contains(string(got), "provider down") {
		t.Errorf("expected runner error surfaced, got %s", got)
	}
}

func TestPipelineTool_BadInput(t *testing.T) {
	tool := NewPipelineTool(fakePipelineRunner{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	if err == nil {
		t.Error("expected unmarshal error for bad input")
	}
}
