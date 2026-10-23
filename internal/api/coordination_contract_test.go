package api

import (
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestCoordinationContractRPCUsesSessionBoundOwner(t *testing.T) {
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	coord, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: store, CoordStore: coord})
	startHTTPServer(t, server)
	folder := t.TempDir()
	front, back, outsider := session.NewSession(folder), session.NewSession(folder), session.NewSession(t.TempDir())
	for i, metadata := range []*session.Session{front, back, outsider} {
		require.NoError(t, store.Create(t.Context(), metadata))
		_, err := coord.RegisterSessionPeer(t.Context(), metadata.ID, []string{"frontend", "backend", "outside"}[i], "fixture")
		require.NoError(t, err)
	}
	spec := coordination.ContractSpec{Name: "users", Endpoint: "GET /users", SchemaVersion: "1", Schema: json.RawMessage(`{"type":"object"}`), AcceptanceCriteria: []string{"client receives a users array"}, Workspace: folder, OwnedPaths: []string{"backend"}}
	created := sidebarRPC(t, server, "coordination.contract", map[string]any{"session_id": back.ID, "input": map[string]any{"action": "create", "spec": spec}})
	require.Nil(t, created.Error)
	data, err := json.Marshal(created.Result)
	require.NoError(t, err)
	var envelope struct {
		Result coordination.Contract `json:"result"`
	}
	require.NoError(t, json.Unmarshal(data, &envelope))
	peer, err := coord.PeerForSession(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, peer.ID, envelope.Result.OwnerPeerID)
	for _, invalid := range []map[string]any{
		{"session_id": front.ID, "input": map[string]any{"action": "update", "contract_id": envelope.Result.ID, "expected_revision": 1, "spec": spec}},
		{"session_id": outsider.ID, "input": map[string]any{"action": "get", "contract_id": envelope.Result.ID}},
		{"session_id": front.ID, "peer_id": peer.ID, "input": map[string]any{"action": "list"}},
		{"session_id": front.ID, "input": map[string]any{"action": "list", "from": peer.ID}},
	} {
		response := sidebarRPC(t, server, "coordination.contract", invalid)
		require.NotNil(t, response.Error)
	}
	view := sidebarRPC(t, server, "coordination.contract", map[string]any{"session_id": front.ID, "input": map[string]any{"action": "get", "contract_id": envelope.Result.ID}})
	require.Nil(t, view.Error)
	for _, revision := range []int{0, 2} {
		response := sidebarRPC(t, server, "coordination.contract", map[string]any{"session_id": front.ID, "input": map[string]any{"action": "acknowledge", "contract_id": envelope.Result.ID, "expected_revision": revision}})
		require.NotNil(t, response.Error)
	}
	ack := sidebarRPC(t, server, "coordination.contract", map[string]any{"session_id": front.ID, "input": map[string]any{"action": "acknowledge", "contract_id": envelope.Result.ID, "expected_revision": 1}})
	require.Nil(t, ack.Error)
}

func TestRequestAgreementPinsThroughSessionRPC(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	coord, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, CoordStore: coord})
	startHTTPServer(t, server)
	folder := t.TempDir()
	metadata := []*session.Session{session.NewSession(folder), session.NewSession(folder), session.NewSession(t.TempDir())}
	peers := make([]coordination.Peer, 3)
	for i, name := range []string{"frontend", "backend", "outsider"} {
		require.NoError(t, sessions.Create(t.Context(), metadata[i]))
		peers[i], err = coord.RegisterSessionPeer(t.Context(), metadata[i].ID, name, "fixture")
		require.NoError(t, err)
		release, err := server.beginSessionOperation(metadata[i].ID)
		require.NoError(t, err)
		defer release()
	}
	spec := coordination.ContractSpec{Name: "Users", Endpoint: "GET /users", SchemaVersion: "1", Schema: json.RawMessage(`{"type":"object"}`), AcceptanceCriteria: []string{"client parses response"}, Workspace: folder, OwnedPaths: []string{"backend"}}
	contract, err := coord.CreateContract(t.Context(), peers[0].ID, spec)
	require.NoError(t, err)
	params := map[string]any{"session_id": metadata[0].ID, "to": "backend", "kind": "request", "payload": "Use this schema", "idempotency_key": "rpc-pin", "required_contracts": []coordination.ContractDependency{{ContractID: contract.ID, Revision: 1}}}
	response := sidebarRPC(t, server, "coordination.send", params)
	require.Nil(t, response.Error)
	data, _ := json.Marshal(response.Result)
	var receipt struct {
		Message coordination.Message `json:"message"`
	}
	require.NoError(t, json.Unmarshal(data, &receipt))
	require.Equal(t, params["required_contracts"], receipt.Message.RequiredContracts)
	_, err = coord.Claim(t.Context(), peers[1].ID)
	require.ErrorIs(t, err, coordination.ErrNotFound)
	graph := sidebarRPC(t, server, "coordination.graph", map[string]string{"session_id": metadata[0].ID, "message_id": receipt.Message.ID})
	require.Nil(t, graph.Error)
	data, _ = json.Marshal(graph.Result)
	require.Contains(t, string(data), "current_revision_not_acknowledged_by:")
	ack := sidebarRPC(t, server, "coordination.contract", map[string]any{"session_id": metadata[1].ID, "input": map[string]any{"action": "acknowledge", "contract_id": contract.ID, "expected_revision": 1}})
	require.Nil(t, ack.Error)
	claimed, err := coord.Claim(t.Context(), peers[1].ID)
	require.NoError(t, err)
	require.Equal(t, receipt.Message.ID, claimed.ID)
	params["session_id"] = metadata[2].ID
	_, _, err = coord.RegisterPeer(t.Context(), coordination.Peer{ProjectID: peers[2].ProjectID, Name: "outside-worker", Role: "fixture", Workspace: peers[2].Workspace})
	require.NoError(t, err)
	params["to"] = "outside-worker"
	params["idempotency_key"] = "foreign-pin"
	require.NotNil(t, sidebarRPC(t, server, "coordination.send", params).Error)
}
