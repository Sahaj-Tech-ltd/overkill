package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepDoesNotReadOutsideSymlinksOrFloodResults(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("PRIVATE_OUTSIDE_MATCH"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "outside-link")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "huge-lines"), []byte(strings.Repeat("match"+strings.Repeat("x", 5000)+"\n", 150)), 0600); err != nil {
		t.Fatal(err)
	}
	grep := NewGrepTool(root)
	raw, err := grep.Execute(context.Background(), json.RawMessage(`{"pattern":"PRIVATE_OUTSIDE_MATCH|match","max_results":1000000}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "PRIVATE_OUTSIDE_MATCH") {
		t.Fatal("outside file bytes leaked")
	}
	var result ToolResult
	_ = json.Unmarshal(raw, &result)
	var matches []grepMatch
	_ = json.Unmarshal([]byte(result.Output), &matches)
	if len(matches) == 0 || len(matches) > 150 || len(result.Output) > 70*1024 {
		t.Fatalf("missing or unbounded allowed matches: %d bytes=%d", len(matches), len(result.Output))
	}
	for _, match := range matches {
		if len(match.Content) > 4200 {
			t.Fatal("unbounded line")
		}
	}
}
