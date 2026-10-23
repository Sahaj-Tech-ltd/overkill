package main

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

func (r *runtimeServices) sessionCoordinationTools(ctx context.Context, id string, peer coordination.Peer) ([]tools.Tool, func(context.Context) (bool, error), error) {
	binding, err := r.Coordination.SessionTransport(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	var registry []tools.Tool
	var tokenHash [32]byte
	if binding.Revision == 0 {
		registry = tools.NewCoordinationTools(r.Coordination, peer.ID)
	} else {
		if binding.PeerID != peer.ID || binding.ProjectID != peer.ProjectID || binding.Workspace != peer.Workspace {
			return nil, nil, coordination.ErrUnauthorized
		}
		token, err := acp.ReadPeerCredential(binding.CredentialFile)
		if err != nil {
			return nil, nil, err
		}
		tokenHash = sha256.Sum256([]byte(token))
		identity, err := acp.NewClient(binding.Endpoint, token).Coordination(ctx, "peers", acp.CoordinationRequest{})
		if err != nil {
			return nil, nil, fmt.Errorf("runtime: scoped ACP transport unavailable")
		}
		if identity.Self == nil || identity.Self.ID != peer.ID || identity.Self.ProjectID != peer.ProjectID || identity.Self.Workspace != peer.Workspace {
			return nil, nil, coordination.ErrUnauthorized
		}
		registry, err = tools.NewBoundRemoteCoordinationTools(binding.Endpoint, token, peer)
		if err != nil {
			return nil, nil, err
		}
		// Same-hub graph/contract/guard/accounting remain local application services.
		for _, tool := range tools.NewCoordinationTools(r.Coordination, peer.ID) {
			if tool.Name() == "agent_task_graph" {
				registry = append(registry, tool)
			}
		}
	}
	check := func(ctx context.Context) (bool, error) {
		current, err := r.Coordination.SessionTransport(ctx, id)
		if err != nil {
			return false, err
		}
		if current != binding {
			return false, nil
		}
		now, err := r.Coordination.PeerForSession(ctx, id)
		if err != nil {
			return false, err
		}
		if now.ID != peer.ID || now.ProjectID != peer.ProjectID || now.Workspace != peer.Workspace {
			return false, coordination.ErrUnauthorized
		}
		if binding.Revision > 0 {
			token, err := acp.ReadPeerCredential(binding.CredentialFile)
			if err != nil {
				return false, err
			}
			if sha256.Sum256([]byte(token)) != tokenHash {
				return false, nil
			}
		}
		return true, nil
	}
	return registry, check, nil
}
