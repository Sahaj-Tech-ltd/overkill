package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls/mcpshield"
	"github.com/stretchr/testify/require"
)

func TestDirectCatalogLookupUsesPolicyLivenessAndOwnedSchema(t *testing.T) {
	manager := NewManager(config.MCPConfig{})
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	client := &Client{connected: true, tools: []Tool{{Name: "read", Description: "Read", InputSchema: schema}}}
	manager.clients["fixture"] = client
	adapter := NewToolAdapter(manager, "fixture", "read")
	require.True(t, adapter.Available())
	definition, ok := manager.lookupTool("fixture", "read")
	require.True(t, ok)
	definition.InputSchema[0] = 'x'
	require.JSONEq(t, string(schema), string(adapter.Schema()), "returned metadata cannot mutate live schema")
	policy := mcpshield.NewPolicy()
	require.NoError(t, policy.Set(mcpshield.Capability{ServerName: "fixture", AllowedTools: []string{"other"}}))
	manager.SetPolicy(policy)
	require.False(t, adapter.Available())
	require.Empty(t, manager.Tools())
	require.NoError(t, policy.Set(mcpshield.Capability{ServerName: "fixture", AllowedTools: []string{"read"}}))
	require.True(t, adapter.Available())
	require.Len(t, manager.Tools(), 1)
	client.setError(fmt.Errorf("fixture disconnected"))
	require.False(t, adapter.Available())
	require.Empty(t, manager.Tools())
}

func BenchmarkToolAdapterDefinitionLargeCatalog(b *testing.B) {
	manager := NewManager(config.MCPConfig{})
	for server := range 64 {
		client := &Client{connected: true}
		for tool := range 256 {
			client.tools = append(client.tools, Tool{Name: fmt.Sprintf("tool%d", tool), InputSchema: json.RawMessage(`{"type":"object"}`)})
		}
		manager.clients[fmt.Sprintf("server%d", server)] = client
	}
	adapter := NewToolAdapter(manager, "server63", "tool255")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if !adapter.Available() {
			b.Fatal("expected live catalog entry")
		}
	}
}
