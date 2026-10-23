//go:build unix

package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPRealProcessNeverReceivesEscapingQuery(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.fixture")
	if err := os.WriteFile(sentinel, []byte("private sentinel stays outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	cfg, marker := fixtureConfig(t, "ready")
	server := cfg.Servers[0]
	client := NewClient(server.Language, server.Command, server.Args, 0)
	if err := client.Start(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Hover(t.Context(), "escape/sentinel.fixture", 0, 0); err == nil {
		t.Fatal("accepted escaped query")
	}
	if _, err := client.Hover(t.Context(), "inside.fixture", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(events), "textDocument/hover ") != 1 {
		t.Fatalf("escaped query reached real child: %s", events)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "private sentinel stays outside" {
		t.Fatalf("sentinel changed: %q %v", data, err)
	}
}
