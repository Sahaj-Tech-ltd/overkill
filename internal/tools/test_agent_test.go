package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	gotLanguage    string
	gotFiles       []string
	gotSpec        string
	gotDescription string
	gotTestCode    string
	gotImplFiles   []string
	tests          string
	review         string
	err            error
}

func (f *fakeRunner) GenerateTests(ctx context.Context, language string, files []string, spec, description string) (string, error) {
	f.gotLanguage = language
	f.gotFiles = files
	f.gotSpec = spec
	f.gotDescription = description
	if f.err != nil {
		return "", f.err
	}
	return f.tests, nil
}

func (f *fakeRunner) ValidateTests(ctx context.Context, testCode string, implFiles []string) (string, error) {
	f.gotTestCode = testCode
	f.gotImplFiles = implFiles
	if f.err != nil {
		return "", f.err
	}
	return f.review, nil
}

func TestSpiderTestTool_NilRunner(t *testing.T) {
	out, _ := NewSpiderTestTool(nil).Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(out), "not configured") {
		t.Fatalf("expected not-configured: %s", out)
	}
}

func TestSpiderTestTool_RequiresSpec(t *testing.T) {
	out, _ := NewSpiderTestTool(&fakeRunner{}).Execute(context.Background(), json.RawMessage(`{"files_to_test":["x.go"]}`))
	if !strings.Contains(string(out), "spec is required") {
		t.Fatalf("expected spec-required: %s", out)
	}
}

func TestSpiderTestTool_RequiresFiles(t *testing.T) {
	out, _ := NewSpiderTestTool(&fakeRunner{}).Execute(context.Background(), json.RawMessage(`{"spec":"do thing"}`))
	if !strings.Contains(string(out), "files_to_test") {
		t.Fatalf("expected files-required: %s", out)
	}
}

func TestSpiderTestTool_HappyPath(t *testing.T) {
	r := &fakeRunner{tests: "func TestFoo() {}"}
	in, _ := json.Marshal(map[string]any{
		"description":   "verifies foo",
		"files_to_test": []string{"foo.go"},
		"spec":          "foo returns 42",
		"language":      "go",
	})
	out, err := NewSpiderTestTool(r).Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["tests"] != "func TestFoo() {}" {
		t.Fatalf("tests = %v", got["tests"])
	}
	if got["isolation"] == nil {
		t.Fatal("isolation marker missing")
	}
	if r.gotLanguage != "go" {
		t.Errorf("language passthrough: %q", r.gotLanguage)
	}
	if r.gotSpec != "foo returns 42" {
		t.Errorf("spec passthrough: %q", r.gotSpec)
	}
}

func TestSpiderValidateTool_HappyPath(t *testing.T) {
	r := &fakeRunner{review: "tests look good"}
	in, _ := json.Marshal(map[string]any{
		"test_code":  "func TestFoo() {}",
		"impl_files": []string{"foo.go"},
	})
	out, err := NewSpiderValidateTool(r).Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(out), "tests look good") {
		t.Fatalf("review missing: %s", out)
	}
}

func TestSpiderValidateTool_RequiresTestCode(t *testing.T) {
	out, _ := NewSpiderValidateTool(&fakeRunner{}).Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(out), "test_code is required") {
		t.Fatalf("expected test-code-required: %s", out)
	}
}

func TestSpiderTestTool_RunnerErrorPropagates(t *testing.T) {
	r := &fakeRunner{err: errors.New("provider down")}
	in, _ := json.Marshal(map[string]any{
		"files_to_test": []string{"x.go"},
		"spec":          "x",
	})
	out, _ := NewSpiderTestTool(r).Execute(context.Background(), in)
	if !strings.Contains(string(out), "provider down") {
		t.Fatalf("expected error pass-through: %s", out)
	}
}
