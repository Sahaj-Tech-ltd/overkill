package mcp

import (
	"testing"
)

func TestRescanTools_RegistersOnlyMissing(t *testing.T) {
	mgr := &Manager{clients: map[string]*Client{}}
	mgr.clients["srvA"] = &Client{
		name:      "srvA",
		connected: true,
		tools:     []Tool{{Name: "t1"}, {Name: "t2"}},
	}
	mgr.clients["srvB"] = &Client{
		name:      "srvB",
		connected: true,
		tools:     []Tool{{Name: "t3"}},
	}

	registered := map[string]bool{
		"mcp:srvA:t1": true, // already known
	}
	added := mgr.RescanTools(
		func(name string) bool { return registered[name] },
		func(adapter *ToolAdapter) error {
			registered[adapter.Name()] = true
			return nil
		},
	)
	if added != 2 {
		t.Fatalf("expected 2 newly registered tools, got %d", added)
	}
	for _, want := range []string{"mcp:srvA:t2", "mcp:srvB:t3"} {
		if !registered[want] {
			t.Errorf("expected %s to be registered", want)
		}
	}
}

func TestRescanTools_NoConnectedServers(t *testing.T) {
	mgr := &Manager{clients: map[string]*Client{}}
	mgr.clients["srvOff"] = &Client{name: "srvOff", connected: false, tools: []Tool{{Name: "t"}}}
	added := mgr.RescanTools(
		func(string) bool { return false },
		func(*ToolAdapter) error { return nil },
	)
	if added != 0 {
		t.Fatalf("expected 0 tools registered when server disconnected, got %d", added)
	}
}
