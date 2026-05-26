package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserDevTool_Name(t *testing.T) {
	if NewBrowserDevTool().Name() != "browser_dev" {
		t.Fatal("name mismatch")
	}
}

func TestBrowserDevTool_NoBinary_GracefulError(t *testing.T) {
	tool := &BrowserDevTool{} // empty binary path simulates missing
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"script":"console.log('x')"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(out), "not found on PATH") {
		t.Fatalf("expected install hint: %s", out)
	}
}

func TestBrowserDevTool_RequiresScript(t *testing.T) {
	tool := &BrowserDevTool{binary: "/usr/bin/true"}
	out, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(out), "script is required") {
		t.Fatalf("expected script-required: %s", out)
	}
}

func TestBrowserDevTool_RunsExternalBinary(t *testing.T) {
	// Use /bin/cat as a stand-in: with no args it echoes stdin to stdout.
	tool := &BrowserDevTool{binary: "/bin/cat"}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"script":"hello world","timeout_seconds":5}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var got browserDevOutput
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Stdout != "hello world" {
		t.Fatalf("stdout = %q want hello world", got.Stdout)
	}
	if got.ExitCode != 0 {
		t.Fatalf("exit = %d want 0", got.ExitCode)
	}
}
