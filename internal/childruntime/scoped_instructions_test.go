package childruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/instructions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

func TestChildInstructionsAreScopedAndReloadedPerRequest(t *testing.T) {
	root := canonicalDir(t)
	path := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(path, []byte("child-guidance-before"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{complete: func(_ context.Context, req providers.Request, step int) (providers.Response, error) {
		expected := "child-guidance-before"
		if step == 2 {
			expected = "child-guidance-after"
		}
		if !strings.Contains(req.SystemPrompt, expected) {
			t.Fatalf("child request missed %q: %s", expected, req.SystemPrompt)
		}
		if step == 1 {
			if err := os.WriteFile(path, []byte("child-guidance-after"), 0600); err != nil {
				t.Fatal(err)
			}
			return call("fs", map[string]any{"action": "read", "path": "AGENTS.md"}), nil
		}
		if strings.Contains(req.SystemPrompt, "child-guidance-before") {
			t.Fatal("stale instructions in child system prompt")
		}
		return providers.Response{Content: "done"}, nil
	}}
	factory := testFactory(root, provider)
	factory.Instructions = instructions.Config{Enabled: true}
	worker, err := factory.Build(subagent.RealWorkerConfig{Goal: "Read the guidance", Tools: []string{"fs"}, MaxSteps: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
}
