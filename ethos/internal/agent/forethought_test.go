package agent

import (
	"encoding/json"
	"testing"
)

func TestForethought_ShellReadCommand(t *testing.T) {
	f := NewForethinker()
	input := json.RawMessage(`{"command": "ls -la /home/user/project"}`)
	assessment := f.Assess("shell", input)

	if assessment.RiskLevel != RiskLow {
		t.Errorf("RiskLevel = %d, want %d (RiskLow)", assessment.RiskLevel, RiskLow)
	}
	if assessment.Protected {
		t.Error("Protected should be false for read-only commands")
	}
}

func TestForethought_ShellWriteCommand(t *testing.T) {
	f := NewForethinker()
	input := json.RawMessage(`{"command": "echo hello > output.txt"}`)
	assessment := f.Assess("shell", input)

	if assessment.RiskLevel != RiskMedium {
		t.Errorf("RiskLevel = %d, want %d (RiskMedium)", assessment.RiskLevel, RiskMedium)
	}
	if len(assessment.AffectedPaths) < 1 {
		t.Errorf("AffectedPaths = %v, want at least 1 path", assessment.AffectedPaths)
	}
	found := false
	for _, p := range assessment.AffectedPaths {
		if p == "output.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'output.txt' in AffectedPaths, got %v", assessment.AffectedPaths)
	}
}

func TestForethought_ShellMultiWrite(t *testing.T) {
	f := NewForethinker()
	input := json.RawMessage(`{"command": "rm -rf dir1 dir2 dir3"}`)
	assessment := f.Assess("shell", input)

	if assessment.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %d, want %d (RiskHigh)", assessment.RiskLevel, RiskHigh)
	}
	if len(assessment.AffectedPaths) != 3 {
		t.Errorf("AffectedPaths count = %d, want 3", len(assessment.AffectedPaths))
	}
}

func TestForethought_ShellModifiesGoMod(t *testing.T) {
	f := NewForethinker()
	input := json.RawMessage(`{"command": "echo module ethos > go.mod"}`)
	assessment := f.Assess("shell", input)

	if assessment.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %d, want %d (RiskHigh)", assessment.RiskLevel, RiskHigh)
	}
	if !assessment.Protected {
		t.Error("Protected should be true when targeting go.mod")
	}
}

func TestForethought_GrepTool(t *testing.T) {
	f := NewForethinker()
	input := json.RawMessage(`{"pattern": "TODO", "path": "."}`)
	assessment := f.Assess("grep", input)

	if assessment.RiskLevel != RiskLow {
		t.Errorf("RiskLevel = %d, want %d (RiskLow)", assessment.RiskLevel, RiskLow)
	}
}

func TestForethought_UnknownTool(t *testing.T) {
	f := NewForethinker()
	input := json.RawMessage(`{"data": "something"}`)
	assessment := f.Assess("custom_tool", input)

	if assessment.RiskLevel != RiskMedium {
		t.Errorf("RiskLevel = %d, want %d (RiskMedium)", assessment.RiskLevel, RiskMedium)
	}
}

func TestForethought_FSTool(t *testing.T) {
	f := NewForethinker()
	input := json.RawMessage(`{"path": "src/main.go", "content": "package main"}`)
	assessment := f.Assess("fs", input)

	if assessment.RiskLevel != RiskMedium {
		t.Errorf("RiskLevel = %d, want %d (RiskMedium)", assessment.RiskLevel, RiskMedium)
	}
}

func TestForethought_ProtectedPathDetection(t *testing.T) {
	f := NewForethinker()
	input := json.RawMessage(`{"command": "rm .github/workflows/test.yml"}`)
	assessment := f.Assess("shell", input)

	if !assessment.Protected {
		t.Error("Protected should be true for .github/ paths")
	}
	if assessment.RiskLevel != RiskHigh {
		t.Errorf("RiskLevel = %d, want %d (RiskHigh)", assessment.RiskLevel, RiskHigh)
	}
}
