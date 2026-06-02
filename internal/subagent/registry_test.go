package subagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentRegistry_ScanDiscoversFiles(t *testing.T) {
	dir := t.TempDir()
	r := NewAgentRegistry(dir, dir) // project=user=same for test
	r.RegisterBuiltin(AgentDef{Name: "explore", Description: "test explore"})

	if err := r.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Built-in should be registered.
	if def := r.Get("explore"); def == nil {
		t.Fatal("built-in explore not found")
	}

	// No file-based agents yet.
	if len(r.List()) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(r.List()))
	}
}

func TestAgentRegistry_FileOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	r := NewAgentRegistry(dir, dir)
	r.RegisterBuiltin(AgentDef{Name: "explore", Description: "built-in"})

	// Write an agent file that overrides the built-in.
	agentFile := filepath.Join(dir, "explore.md")
	content := `---
name: explore
description: file-based override
tools: read, grep
model: haiku
maxTurns: 5
---
You are an exploration agent from a file.`
	if err := os.WriteFile(agentFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	if err := r.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	def := r.Get("explore")
	if def == nil {
		t.Fatal("explore not found")
	}
	if def.Description != "file-based override" {
		t.Errorf("description = %q, want file-based override", def.Description)
	}
	if def.Model != "haiku" {
		t.Errorf("model = %q, want haiku", def.Model)
	}
	if def.MaxTurns != 5 {
		t.Errorf("maxTurns = %d, want 5", def.MaxTurns)
	}
	if len(def.Tools) != 2 {
		t.Errorf("tools len = %d, want 2", len(def.Tools))
	}
}

func TestAgentRegistry_SelectBest(t *testing.T) {
	dir := t.TempDir()
	r := NewAgentRegistry(dir, dir)

	r.RegisterBuiltin(AgentDef{
		Name:        "explore",
		Description: "Codebase exploration and research. Use for file discovery and code search. Best for: investigate, research, find.",
	})
	r.RegisterBuiltin(AgentDef{
		Name:        "reviewer",
		Description: "Expert code reviewer. Use proactively after code changes. Reviews for bugs and security.",
	})

	// "investigate the codebase structure" should match explore (investigate + codebase).
	def := r.SelectBest("investigate the codebase structure")
	if def == nil {
		t.Fatal("no agent matched investigate task")
	}
	if def.Name != "explore" {
		t.Errorf("got %q, want explore", def.Name)
	}

	// "review my latest PR" should match reviewer.
	def = r.SelectBest("review my latest PR for bugs")
	if def == nil {
		t.Fatal("no agent matched review task")
	}
	if def.Name != "reviewer" {
		t.Errorf("got %q, want reviewer", def.Name)
	}

	// "something completely unrelated" should return nil.
	def = r.SelectBest("make me a sandwich")
	if def != nil {
		t.Errorf("expected nil for unrelated task, got %q", def.Name)
	}
}

func TestAgentRegistry_SelectBestByName(t *testing.T) {
	dir := t.TempDir()
	r := NewAgentRegistry(dir, dir)

	r.RegisterBuiltin(AgentDef{
		Name:        "code-reviewer",
		Description: "Reviews code for quality and best practices",
	})

	// Direct mention of agent name should match.
	def := r.SelectBest("use the code-reviewer agent to check this file")
	if def == nil || def.Name != "code-reviewer" {
		t.Fatalf("expected code-reviewer, got %v", def)
	}
}

func TestAgentRegistry_PriorityResolution(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "project")
	userDir := filepath.Join(t.TempDir(), "user")
	os.MkdirAll(projectDir, 0750)
	os.MkdirAll(userDir, 0750)

	r := NewAgentRegistry(projectDir, userDir)
	r.RegisterBuiltin(AgentDef{Name: "helper", Description: "built-in helper"})

	// User agent overrides builtin.
	userFile := filepath.Join(userDir, "helper.md")
	os.WriteFile(userFile, []byte(`---
name: helper
description: user-level helper
---
User.`), 0600)

	// Project agent overrides user.
	projectFile := filepath.Join(projectDir, "helper.md")
	os.WriteFile(projectFile, []byte(`---
name: helper
description: project-level helper
---
Project.`), 0600)

	if err := r.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	def := r.Get("helper")
	if def == nil {
		t.Fatal("helper not found")
	}
	// Project should win (priority 3 > user 4).
	if def.Description != "project-level helper" {
		t.Errorf("description = %q, want project-level helper", def.Description)
	}
}

func TestAgentRegistry_DisallowedTools(t *testing.T) {
	dir := t.TempDir()
	r := NewAgentRegistry(dir, dir)

	agentFile := filepath.Join(dir, "restricted.md")
	os.WriteFile(agentFile, []byte(`---
name: restricted
description: restricted agent
tools: read, grep, shell, write
disallowedTools: write, shell
---
Restricted.`), 0644)

	if err := r.Scan(); err != nil {
		t.Fatal(err)
	}

	def := r.Get("restricted")
	if def == nil {
		t.Fatal("not found")
	}
	if len(def.DisallowedTools) != 2 {
		t.Errorf("disallowedTools len = %d, want 2", len(def.DisallowedTools))
	}
}
