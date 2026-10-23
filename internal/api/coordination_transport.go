package api

import (
	"context"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
)

// Authenticated application-only provisioning. Model tools never select a URL,
// credential reference or peer identity. Admission covers verification, durable
// configuration and disposal of any already-loaded agent/tool catalogue.
func (s *Server) handleCoordinationTransport(ctx context.Context, method string, data []byte) (any, *RPCError) {
	if s.coordinationStore == nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Agent coordination is unavailable."}
	}
	var in struct {
		SessionID      string `json:"session_id"`
		Endpoint       string `json:"endpoint"`
		CredentialFile string `json:"credential_file"`
	}
	if err := coordinationParams(data, &in); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "Invalid transport binding parameters."}
	}
	release, err := s.beginSessionOperation(in.SessionID)
	if err != nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Session is busy; bind transport after its active turn finishes."}
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	local, err := s.coordinationStore.PeerForSession(ctx, in.SessionID)
	if err != nil {
		return nil, coordinationError(err)
	}
	if method == "coordination.transport.clear" {
		if in.Endpoint != "" || in.CredentialFile != "" {
			return nil, &RPCError{Code: InvalidParams, Message: "Clear requires only session identity."}
		}
		if err = s.coordinationStore.ClearSessionTransport(ctx, in.SessionID); err != nil {
			return nil, coordinationError(err)
		}
		s.discardSessionRuntime(in.SessionID)
		return map[string]any{"session_id": in.SessionID, "mode": "local"}, nil
	}
	token, err := acp.ReadPeerCredential(in.CredentialFile)
	if err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "A private scoped peer credential file is required."}
	}
	remote, err := acp.NewClient(in.Endpoint, token).Coordination(ctx, "peers", acp.CoordinationRequest{})
	if err != nil || remote.Self == nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Scoped ACP transport authentication failed."}
	}
	if remote.Self.ID != local.ID || remote.Self.ProjectID != local.ProjectID || remote.Self.Workspace != local.Workspace {
		return nil, &RPCError{Code: InvalidRequest, Message: "ACP identity must match this chat's existing project peer and workspace."}
	}
	binding, err := s.coordinationStore.BindSessionTransport(ctx, in.SessionID, *remote.Self, in.Endpoint, in.CredentialFile)
	if err != nil {
		return nil, coordinationError(err)
	}
	s.discardSessionRuntime(in.SessionID)
	return map[string]any{"session_id": in.SessionID, "peer_id": local.ID, "endpoint": binding.Endpoint, "revision": binding.Revision}, nil
}
