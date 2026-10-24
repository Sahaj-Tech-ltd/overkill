//go:build unix

package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
)

func TestTerminalHumanRoutesAreScopedAndRequireTakeover(t *testing.T) {
	m, err := terminal.New(t.TempDir(), terminal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	s := &Server{sessionRuntimes: map[string]*SessionRuntime{"chat": {Terminals: m}}}
	owner := terminal.Owner{Session: "chat", Worker: "backend"}
	info, err := m.Start(context.Background(), owner, "cat", "", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	rpc := func(method string, params any) (interface{}, *RPCError) {
		raw, _ := json.Marshal(params)
		return s.handleTerminal(context.Background(), method, raw)
	}
	if _, err := rpc("terminal.input", map[string]any{"session_id": "chat", "id": info.ID, "data": "human\n", "control_revision": 1, "sequence": 1}); err == nil {
		t.Fatal("input admitted before takeover")
	}
	if _, err := rpc("terminal.control", map[string]any{"session_id": "chat", "id": info.ID, "control_revision": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := rpc("terminal.input", map[string]any{"session_id": "chat", "id": info.ID, "data": "human\n", "control_revision": 2, "sequence": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write(context.Background(), owner, info.ID, false, []byte("agent\n")); err != terminal.ErrHumanControl {
		t.Fatal(err)
	}
	if _, err := rpc("terminal.read", map[string]any{"session_id": "other", "id": info.ID}); err == nil {
		t.Fatal("foreign session read admitted")
	}
	if _, err := rpc("terminal.read", map[string]any{"session_id": "chat", "id": info.ID, "owner": "other"}); err == nil {
		t.Fatal("model-supplied identity accepted")
	}
	if _, err := rpc("terminal.control", map[string]any{"session_id": "chat", "id": info.ID, "release": true, "control_revision": 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write(context.Background(), owner, info.ID, false, []byte("agent\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := rpc("terminal.close", map[string]any{"session_id": "chat", "id": info.ID, "control_revision": 3}); err != nil {
		t.Fatal(err)
	}
}

func TestAbortStopsPersistentTerminalEvenWhenNoTurnIsStreaming(t *testing.T) {
	m, err := terminal.New(t.TempDir(), terminal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	s := &Server{sessionRuntimes: map[string]*SessionRuntime{"chat": {Terminals: m}}, sessionCancels: make(map[string]*sessionCancellation)}
	owner := terminal.Owner{Session: "chat", Worker: "main"}
	info, err := m.Start(context.Background(), owner, "cat", "", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	// The method may report no active model; owned processes must still stop.
	_, _ = s.handleAgentAbort(context.Background(), []byte(`{"session_id":"chat"}`))
	if _, err := m.Write(context.Background(), owner, info.ID, false, []byte("late")); err != terminal.ErrClosed {
		t.Fatal("terminal survived abort", err)
	}
}
