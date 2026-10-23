package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

func TestResearchReaderModeRejectsActualShellWrite(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewShellTool()); err != nil {
		t.Fatal(err)
	}
	a := &Agent{toolRegistry: registry}
	a.SetPrivilegeGate(security.NewPrivilegeGate(security.ModeReader))
	raw, err := json.Marshal(tools.ShellInput{Command: "printf reviewed > reader-mode-proof.txt", WorkingDir: root})
	if err != nil {
		t.Fatal(err)
	}
	output, execErr := a.executeTool(t.Context(), "shell", raw)
	_, statErr := os.Stat(filepath.Join(root, "reader-mode-proof.txt"))
	t.Logf("actual Agent.executeTool -> default-scanned ShellTool: error=%v, temp-file-created=%v, output=%s", execErr, statErr == nil, output)
	if !errors.Is(execErr, security.ErrWriteDenied) || statErr == nil {
		t.Fatalf("ReaderMode must deny mutation before shell execution; denied=%v, temp-file-created=%v", errors.Is(execErr, security.ErrWriteDenied), statErr == nil)
	}
}

func TestReaderModeDeniesOpaqueEffectsButPreservesNativeReads(t *testing.T) {
	for _, command := range []string{
		"printf reviewed > proof.txt",
		"echo observed; printf changed > proof.txt",
		"python3 -c 'open(\"proof.txt\",\"w\").write(\"changed\")'",
		"env sh -c 'printf changed > proof.txt'",
	} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			registry := tools.NewRegistry()
			if err := registry.Register(tools.NewShellTool()); err != nil {
				t.Fatal(err)
			}
			a := &Agent{toolRegistry: registry}
			a.SetPrivilegeGate(security.NewPrivilegeGate(security.ModeReader))
			raw, _ := json.Marshal(tools.ShellInput{Command: command, WorkingDir: root})
			_, err := a.executeTool(t.Context(), "shell", raw)
			if !errors.Is(err, security.ErrWriteDenied) {
				t.Fatalf("command admitted: %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("effect before denial: %v %v", entries, err)
			}
		})
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "read.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFSTool(root)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tools.NewShellTool()); err != nil {
		t.Fatal(err)
	}
	a := &Agent{toolRegistry: registry}
	gate := security.NewPrivilegeGate(security.ModeReader)
	a.SetPrivilegeGate(gate)
	out, err := a.executeTool(t.Context(), "fs", json.RawMessage(`{"action":"read","path":"read.txt"}`))
	if err != nil || !strings.Contains(string(out), "original") {
		t.Fatalf("native read failed: %s %v", out, err)
	}
	_, err = a.executeTool(t.Context(), "fs", json.RawMessage(`{"action":"edit","path":"read.txt","old_text":"original","new_text":"changed"}`))
	if !errors.Is(err, security.ErrWriteDenied) {
		t.Fatalf("edit admitted: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "read.txt"))
	if string(data) != "original" {
		t.Fatal("reader edited file")
	}
	gate.SetMode(security.ModeWriter)
	raw, _ := json.Marshal(tools.ShellInput{Command: "printf writer > writer.txt", WorkingDir: root})
	if _, err = a.executeTool(t.Context(), "shell", raw); err != nil {
		t.Fatalf("writer failed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "writer.txt")); err != nil || string(data) != "writer" {
		t.Fatalf("writer effect missing: %q %v", data, err)
	}
}
