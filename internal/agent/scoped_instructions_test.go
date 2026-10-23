package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/instructions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

type instructionProvider struct{ requests []providers.Request }

func (p *instructionProvider) Name() string              { return "instruction-fixture" }
func (p *instructionProvider) Models() []providers.Model { return nil }
func (p *instructionProvider) Complete(_ context.Context, req providers.Request) (providers.Response, error) {
	p.requests = append(p.requests, req)
	return providers.Response{Content: "done"}, nil
}
func (p *instructionProvider) Stream(_ context.Context, req providers.Request) (<-chan providers.Chunk, error) {
	p.requests = append(p.requests, req)
	ch := make(chan providers.Chunk, 2)
	ch <- providers.Chunk{Content: "done"}
	ch <- providers.Chunk{Done: true}
	close(ch)
	return ch, nil
}
func instructionWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestScopedInstructionsReachProviderAndRefresh(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(t.TempDir(), "global.md")
	instructionWrite(t, global, "global-marker")
	instructionWrite(t, filepath.Join(root, "AGENTS.md"), "root-marker")
	instructionWrite(t, filepath.Join(root, "a", "AGENTS.md"), "directory-a-marker")
	instructionWrite(t, filepath.Join(root, "b", "AGENTS.md"), "directory-b-marker")
	source, err := instructions.New(root, instructions.Config{Enabled: true, GlobalFile: global, Directory: "a"})
	if err != nil {
		t.Fatal(err)
	}
	p := &instructionProvider{}
	a := New(Config{Provider: p, Model: "fixture", InstructionSource: source, Tokenizer: tokenizer.NewEstimator(), ContextWindow: 100000, MaxTokens: 100})
	defer a.Shutdown()
	run := func() string {
		t.Helper()
		if _, err := a.Run(context.Background(), "continue"); err != nil {
			t.Fatal(err)
		}
		return p.requests[len(p.requests)-1].SystemPrompt
	}
	first := run()
	if !(strings.Index(first, "global-marker") < strings.Index(first, "root-marker") && strings.Index(first, "root-marker") < strings.Index(first, "directory-a-marker")) || strings.Contains(first, "directory-b-marker") {
		t.Fatal(first)
	}
	// Editing and switching scope refreshes the exact next provider request.
	instructionWrite(t, filepath.Join(root, "AGENTS.md"), "edited-root-marker")
	if err := source.SetDirectory("b"); err != nil {
		t.Fatal(err)
	}
	next := run()
	if !strings.Contains(next, "edited-root-marker") || !strings.Contains(next, "directory-b-marker") || strings.Contains(next, "directory-a-marker") {
		t.Fatal(next)
	}
	if err := os.Remove(filepath.Join(root, "b", "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if got := run(); strings.Contains(got, "directory-b-marker") {
		t.Fatal(got)
	}
	for _, message := range a.History() {
		if strings.Contains(message.Content, "global-marker") {
			t.Fatal("instruction content persisted in history")
		}
	}
	with := a.BudgetReport().SystemPromptTokens
	a.SetInstructionSource(nil)
	without := a.BudgetReport().SystemPromptTokens
	if with <= without {
		t.Fatalf("instruction tokens missing from budget: %d <= %d", with, without)
	}
	if got := run(); strings.Contains(got, "global-marker") || strings.Contains(got, "Workspace instructions") {
		t.Fatal(got)
	}
}

func TestScopedInstructionsRestoreAndInvalidFilePrompt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	instructionWrite(t, path, "restore-marker")
	source, err := instructions.New(root, instructions.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	a := New(Config{Provider: &instructionProvider{}, InstructionSource: source})
	defer a.Shutdown()
	// Restoring a compacted/resumed transcript cannot replace current guidance.
	a.RestoreHistory([]providers.Message{{Role: "user", Content: "compacted summary"}})
	if got := a.buildRequest().SystemPrompt; !strings.Contains(got, "restore-marker") {
		t.Fatal(got)
	}
	instructionWrite(t, path, strings.Repeat("z", instructions.MaxFileBytes+1))
	got := a.buildRequest().SystemPrompt
	if strings.Contains(got, "restore-marker") || strings.Contains(got, strings.Repeat("z", 100)) || !strings.Contains(got, "oversized") {
		t.Fatal("invalid instruction file did not disappear with diagnostic")
	}
}

func TestScopedInstructionsSurviveActualCompactionAndPromptCompression(t *testing.T) {
	root := t.TempDir()
	instructionWrite(t, filepath.Join(root, "AGENTS.md"), "retained-guidance-marker")
	source, err := instructions.New(root, instructions.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	a := New(Config{Provider: &instructionProvider{}, InstructionSource: source, Tokenizer: tokenizer.NewEstimator(), ContextWindow: 200, MaxTokens: 50})
	defer a.Shutdown()
	a.RestoreHistory([]providers.Message{{Role: "user", Content: "earlier task"}, {Role: "assistant", Content: "earlier result"}, {Role: "user", Content: "continue task"}})
	if _, err := a.Compact(t.Context()); err != nil {
		t.Fatal(err)
	}
	compressor := &fakeCompressor{out: "compressed host prompt", saved: 10}
	a.SetPromptCompressor(compressor, 0.01)
	prompt := a.buildRequest().SystemPrompt
	if !compressor.called {
		t.Fatal("prompt compression not exercised")
	}
	if !strings.Contains(prompt, "compressed host prompt") || !strings.Contains(prompt, "retained-guidance-marker") {
		t.Fatal(prompt)
	}
	if strings.Contains(compressor.gotInput, "retained-guidance-marker") {
		t.Fatal("instruction contents were exposed to lossy prompt compression")
	}
	a.SetInstructionSource(nil)
	if got := a.buildRequest().SystemPrompt; strings.Contains(got, "retained-guidance-marker") {
		t.Fatal(got)
	}
}
