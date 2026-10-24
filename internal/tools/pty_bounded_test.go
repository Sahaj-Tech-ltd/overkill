//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPTYShellRetainsBoundedOutput(t *testing.T) {
	tool := NewPTYShellTool(t.TempDir())
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"yes x | head -c 1200000","timeout_seconds":10}`))
	if err != nil {
		t.Fatal(err)
	}
	var out ptyShellOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != 0 || !out.OutputTruncated || out.OutputBytes <= 1024*1024 || len(out.Output) > 1024*1024+1024 {
		t.Fatalf("unbounded or missing output accounting: exit=%d bytes=%d retained=%d truncated=%v", out.ExitCode, out.OutputBytes, len(out.Output), out.OutputTruncated)
	}
}
