package subagent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContextExport_Marshal(t *testing.T) {
	original := ContextExport{
		SessionID: "sess-1",
		Goal:      "implement auth",
		Context: ExportContext{
			FilesModified:       []string{"auth.go"},
			RecentChanges:       "added login handler",
			ProjectStructure:    "cmd/ main.go",
			Constraints:         "must be backwards compatible",
			Language:            "go",
			RelatedConversation: "user asked about OAuth2",
		},
		EthosVersion: "0.1.0",
	}

	jsonStr, err := original.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error: %v", err)
	}

	got, err := ContextExportFromJSON(jsonStr)
	if err != nil {
		t.Fatalf("ContextExportFromJSON() error: %v", err)
	}

	if got.SessionID != original.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, original.SessionID)
	}
	if got.Goal != original.Goal {
		t.Errorf("Goal = %q, want %q", got.Goal, original.Goal)
	}
	if got.EthosVersion != original.EthosVersion {
		t.Errorf("EthosVersion = %q, want %q", got.EthosVersion, original.EthosVersion)
	}
	if len(got.Context.FilesModified) != 1 || got.Context.FilesModified[0] != "auth.go" {
		t.Errorf("FilesModified = %v, want [auth.go]", got.Context.FilesModified)
	}
	if got.Context.RecentChanges != original.Context.RecentChanges {
		t.Errorf("RecentChanges = %q, want %q", got.Context.RecentChanges, original.Context.RecentChanges)
	}
	if got.Context.ProjectStructure != original.Context.ProjectStructure {
		t.Errorf("ProjectStructure = %q, want %q", got.Context.ProjectStructure, original.Context.ProjectStructure)
	}
	if got.Context.Constraints != original.Context.Constraints {
		t.Errorf("Constraints = %q, want %q", got.Context.Constraints, original.Context.Constraints)
	}
	if got.Context.Language != original.Context.Language {
		t.Errorf("Language = %q, want %q", got.Context.Language, original.Context.Language)
	}
	if got.Context.RelatedConversation != original.Context.RelatedConversation {
		t.Errorf("RelatedConversation = %q, want %q", got.Context.RelatedConversation, original.Context.RelatedConversation)
	}
}

func TestContextExport_SecretFiltering(t *testing.T) {
	original := ContextExport{
		SessionID: "sess-2",
		Goal:      "deploy to prod",
		Context: ExportContext{
			Constraints: "API_KEY=sk-abc123secret DATA=test password=hunter2 TOKEN=sk-proj-abcdefghijklmnopqrstuvwx",
		},
		EthosVersion: "0.2.0",
	}

	filtered := original.FilterSecrets()

	// The original must be unchanged.
	if original.Context.Constraints == filtered.Context.Constraints {
		t.Error("FilterSecrets should return a copy, but original was modified")
	}

	// Secret values must be gone.
	if strings.Contains(filtered.Context.Constraints, "sk-abc123secret") {
		t.Error("filtered constraints still contains secret 'sk-abc123secret'")
	}
	if strings.Contains(filtered.Context.Constraints, "hunter2") {
		t.Error("filtered constraints still contains secret 'hunter2'")
	}
	if strings.Contains(filtered.Context.Constraints, "sk-proj-abcdefghijklmnopqrstuvwx") {
		t.Error("filtered constraints still contains long OpenAI key")
	}

	// Non-secret values must survive.
	if !strings.Contains(filtered.Context.Constraints, "DATA=test") {
		t.Error("filtered constraints should still contain 'DATA=test'")
	}

	// Keys should be preserved with [REDACTED] values.
	if !strings.Contains(filtered.Context.Constraints, "API_KEY=[REDACTED]") {
		t.Error("filtered constraints should contain 'API_KEY=[REDACTED]'")
	}
	if !strings.Contains(filtered.Context.Constraints, "password=[REDACTED]") {
		t.Error("filtered constraints should contain 'password=[REDACTED]'")
	}
}

func TestContextExport_Empty(t *testing.T) {
	original := ContextExport{}

	jsonStr, err := original.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() on empty struct error: %v", err)
	}
	if jsonStr == "" {
		t.Fatal("ToJSON() returned empty string for zero-value ContextExport")
	}

	// Must be valid JSON.
	if !json.Valid([]byte(jsonStr)) {
		t.Fatalf("ToJSON() produced invalid JSON: %s", jsonStr)
	}

	// Round-trip must succeed.
	got, err := ContextExportFromJSON(jsonStr)
	if err != nil {
		t.Fatalf("ContextExportFromJSON() error: %v", err)
	}
	if got.SessionID != "" {
		t.Errorf("SessionID = %q, want empty", got.SessionID)
	}
	if got.Goal != "" {
		t.Errorf("Goal = %q, want empty", got.Goal)
	}
}
