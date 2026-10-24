//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
)

func TestTerminalToolBindsIdentityAndPersistsAcrossCalls(t *testing.T) {
	workspace := t.TempDir()
	manager, err := terminal.New(workspace, terminal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	owner := terminal.Owner{Session: "chat", Worker: "main"}
	tool := NewTerminalTool(manager, owner, workspace)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"operation":"start","command":"read value; printf 'received:%s\\n' \"$value\""}`))
	if err != nil {
		t.Fatal(err)
	}
	var info terminal.Info
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if info.Owner != owner || info.ID == "" {
		t.Fatal(info)
	}
	call := func(value any) (json.RawMessage, error) {
		in, _ := json.Marshal(value)
		return tool.Execute(context.Background(), in)
	}
	if _, err := call(map[string]any{"operation": "input", "id": info.ID, "data": "ok\n", "owner": "other"}); err == nil {
		t.Fatal("caller supplied identity")
	}
	if _, err := call(map[string]any{"operation": "input", "id": info.ID, "data": "ok\n", "control_revision": info.ControlRevision, "sequence": 1}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw, err = call(map[string]any{"operation": "read", "id": info.ID, "wait_ms": 1000})
		if err != nil {
			t.Fatal(err)
		}
		var chunk terminal.Chunk
		_ = json.Unmarshal(raw, &chunk)
		if chunk.Info.State == "exited" {
			if chunk.Info.ExitCode == nil || *chunk.Info.ExitCode != 0 {
				t.Fatal(chunk)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal failed to finish")
		}
	}
}
