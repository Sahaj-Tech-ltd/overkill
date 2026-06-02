package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
)

func TestArchitectureWallTool_NoWall(t *testing.T) {
	tool := NewArchitectureWallTool(nil)
	got, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(got), "not configured") {
		t.Errorf("expected configuration error, got %s", got)
	}
}

func TestArchitectureWallTool_EmptyFiles(t *testing.T) {
	w := walls.NewArchitectureWall(walls.ArchitectureConfig{Enabled: true})
	tool := NewArchitectureWallTool(w)
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"files":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res walls.WallResult
	if err := json.Unmarshal(got, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !res.Passed {
		t.Errorf("expected empty file set to pass, got %+v", res)
	}
}

func TestArchitectureWallTool_BadInput(t *testing.T) {
	w := walls.NewArchitectureWall(walls.ArchitectureConfig{Enabled: true})
	tool := NewArchitectureWallTool(w)
	_, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	if err == nil {
		t.Error("expected unmarshal error for bad input")
	}
}

func TestOuroborosWallTool_NoWall(t *testing.T) {
	tool := NewOuroborosWallTool(nil)
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{"code":"x","spec":"y"}`))
	if !strings.Contains(string(got), "not configured") {
		t.Errorf("expected not-configured error, got %s", got)
	}
}

func TestOuroborosWallTool_MissingSpec(t *testing.T) {
	w := walls.NewOuroborosWall(walls.OuroborosConfig{Enabled: true})
	tool := NewOuroborosWallTool(w)
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{"code":"package x"}`))
	if !strings.Contains(string(got), "spec is required") {
		t.Errorf("expected spec-required error, got %s", got)
	}
}

func TestOuroborosWallTool_MissingCode(t *testing.T) {
	w := walls.NewOuroborosWall(walls.OuroborosConfig{Enabled: true})
	tool := NewOuroborosWallTool(w)
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{"spec":"y"}`))
	if !strings.Contains(string(got), "code is required") {
		t.Errorf("expected code-required error, got %s", got)
	}
}
