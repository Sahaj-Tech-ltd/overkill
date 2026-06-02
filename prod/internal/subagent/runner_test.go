package subagent

import (
	"testing"
	"time"
)

func TestAgentRunner_ModelCascade(t *testing.T) {
	tests := []struct {
		name      string
		cfg       SpawnConfig
		wantModel string
	}{
		{
			name: "agent model wins",
			cfg: SpawnConfig{
				Agent:         AgentDef{Name: "test", Model: "haiku"},
				ModelOverride: "sonnet",
			},
			wantModel: "haiku",
		},
		{
			name: "inherit uses override",
			cfg: SpawnConfig{
				Agent:         AgentDef{Name: "test", Model: "inherit"},
				ModelOverride: "sonnet",
			},
			wantModel: "sonnet",
		},
		{
			name: "default fallback",
			cfg: SpawnConfig{
				Agent: AgentDef{Name: "test"},
			},
			wantModel: "mimo-v2-pro",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveModel(tt.cfg)
			if got != tt.wantModel {
				t.Errorf("resolveModel = %q, want %q", got, tt.wantModel)
			}
		})
	}
}

func TestAgentRunner_MaxStepsCascade(t *testing.T) {
	tests := []struct {
		name      string
		cfg       SpawnConfig
		wantSteps int
	}{
		{
			name: "agent maxTurns wins",
			cfg: SpawnConfig{
				Agent:            AgentDef{Name: "test", MaxTurns: 5},
				MaxStepsOverride: 20,
			},
			wantSteps: 5,
		},
		{
			name: "override fallback",
			cfg: SpawnConfig{
				Agent:            AgentDef{Name: "test"},
				MaxStepsOverride: 20,
			},
			wantSteps: 20,
		},
		{
			name: "default",
			cfg: SpawnConfig{
				Agent: AgentDef{Name: "test"},
			},
			wantSteps: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveMaxSteps(tt.cfg)
			if got != tt.wantSteps {
				t.Errorf("resolveMaxSteps = %d, want %d", got, tt.wantSteps)
			}
		})
	}
}

func TestAgentRunner_IsReadOnly(t *testing.T) {
	tests := []struct {
		name     string
		agent    AgentDef
		readOnly bool
	}{
		{"explore is read-only", AgentDef{Name: "explore", Tools: []string{"read", "grep"}}, true},
		{"plan is read-only", AgentDef{Name: "plan", Tools: []string{"read", "grep", "glob"}}, true},
		{"Explore (capital) read-only", AgentDef{Name: "Explore"}, true},
		{"shell tool = not read-only", AgentDef{Name: "builder", Tools: []string{"read", "shell"}}, false},
		{"write tool = not read-only", AgentDef{Name: "writer", Tools: []string{"write"}}, false},
		{"edit tool = not read-only", AgentDef{Name: "editor", Tools: []string{"edit"}}, false},
		{"no tools = read-only", AgentDef{Name: "unknown"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isReadOnlyAgent(tt.agent)
			if got != tt.readOnly {
				t.Errorf("isReadOnlyAgent(%q) = %v, want %v", tt.name, got, tt.readOnly)
			}
		})
	}
}

func TestAgentRunner_BuildPrompt(t *testing.T) {
	cfg := SpawnConfig{
		Agent: AgentDef{
			Name:          "test",
			InitialPrompt: "You are a test agent.",
			Tools:         []string{"read", "write"}, // not read-only
		},
		Goal:        "Run the tests.",
		ForkContext: "Previous run failed with timeout.",
	}

	prompt := buildPrompt(cfg)

	// Initial prompt should be first, then fork context, then goal.
	// No slim context note because agent has write tools.
	expected := "You are a test agent.\n\n[Context from parent conversation]\nPrevious run failed with timeout.\n\nRun the tests."
	if prompt != expected {
		t.Errorf("unexpected prompt:\n got: %q\nwant: %q", prompt, expected)
	}
}

func TestAgentRunner_SlimContext(t *testing.T) {
	cfg := SpawnConfig{
		Agent: AgentDef{
			Name:          "explore",
			InitialPrompt: "Explore the codebase.",
		},
		Goal: "Find auth code.",
	}

	prompt := buildPrompt(cfg)

	if prompt != "Explore the codebase.\n\n[Note: Read-only agent. Project guidelines (CLAUDE.md, AGENTS.md) are omitted for efficiency. The main agent interprets your output.]\n\nFind auth code." {
		t.Errorf("unexpected slim prompt: %q", prompt)
	}
}

func TestAgentRunner_AutoDeny(t *testing.T) {
	ctx := withAutoDenyPermissions(t.Context())
	if !IsAutoDeny(ctx) {
		t.Error("expected auto-deny to be true")
	}

	ctx2 := t.Context()
	if IsAutoDeny(ctx2) {
		t.Error("expected auto-deny to be false for clean context")
	}
}

func TestAgentRunner_BackgroundAutoDeny(t *testing.T) {
	// Verify that async agents without permission prompts get auto-deny.
	cfg := SpawnConfig{
		Agent:   AgentDef{Name: "bg-agent", Background: true},
		IsAsync: true,
	}

	runner := &AgentRunner{cfg: cfg}
	ctx := t.Context()

	// Simulate what Run() does.
	if runner.cfg.IsAsync && !runner.cfg.CanShowPermissionPrompts {
		ctx = withAutoDenyPermissions(ctx)
	}

	if !IsAutoDeny(ctx) {
		t.Error("background agent should auto-deny permissions")
	}
}

func TestAgentRunner_TimeoutDefault(t *testing.T) {
	cfg := SpawnConfig{Agent: AgentDef{Name: "test"}}
	got := resolveTimeout(cfg)
	if got != 5*time.Minute {
		t.Errorf("default timeout = %v, want 5m", got)
	}

	cfg.TimeoutOverride = 30 * time.Second
	got = resolveTimeout(cfg)
	if got != 30*time.Second {
		t.Errorf("override timeout = %v, want 30s", got)
	}
}
