package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// Callback setup is an authenticated application operation. No agent tool or
// message payload can choose an outbound URL or retrieve a signing credential.
func (s *Server) handleCoordinationCallback(ctx context.Context, method string, data []byte) (any, *RPCError) {
	if s.coordinationStore == nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Agent coordination is unavailable."}
	}
	var identity struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "Invalid session parameters."}
	}
	peer, err := s.coordinationStore.PeerForSession(ctx, identity.SessionID)
	if err != nil {
		return nil, coordinationError(err)
	}
	switch method {
	case "coordination.callback.subscribe":
		var p struct {
			SessionID   string `json:"session_id"`
			Destination string `json:"destination"`
			After       int64  `json:"after"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		s.backgroundMu.Lock()
		_, allowed := s.callbackDestinations[p.Destination]
		s.backgroundMu.Unlock()
		if !allowed {
			return nil, &RPCError{Code: InvalidParams, Message: "Callback destination is not configured by the application operator."}
		}
		sub, secret, err := s.coordinationStore.SubscribeCallback(ctx, peer.ID, p.Destination, p.After)
		if err != nil {
			return nil, coordinationError(err)
		}
		return map[string]any{"subscription": sub, "signing_secret": secret, "delivery_guarantee": "at_least_once", "signature_version": "v1"}, nil
	case "coordination.callback.list":
		var p struct {
			SessionID string `json:"session_id"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		result, err := s.coordinationStore.CallbackSubscriptions(ctx, peer.ID)
		if err != nil {
			return nil, coordinationError(err)
		}
		return map[string]any{"subscriptions": result}, nil
	case "coordination.callback.rotate", "coordination.callback.deliveries", "coordination.callback.cancel", "coordination.callback.retry":
		var p struct {
			SessionID      string `json:"session_id"`
			SubscriptionID string `json:"subscription_id"`
			Sequence       int64  `json:"sequence,omitempty"`
		}
		if err := coordinationParams(data, &p); err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
		}
		if p.SubscriptionID == "" || (method == "coordination.callback.retry" && p.Sequence < 1) || (method != "coordination.callback.retry" && p.Sequence != 0) {
			return nil, &RPCError{Code: InvalidParams, Message: "Valid subscription and retry sequence required."}
		}
		if method == "coordination.callback.rotate" {
			secret, err := s.coordinationStore.RotateCallbackKey(ctx, peer.ID, p.SubscriptionID)
			if err != nil {
				return nil, coordinationError(err)
			}
			return map[string]any{"signing_secret": secret, "subscription_id": p.SubscriptionID, "signature_version": "v1"}, nil
		}
		if method == "coordination.callback.deliveries" {
			result, err := s.coordinationStore.CallbackDeliveries(ctx, peer.ID, p.SubscriptionID)
			if err != nil {
				return nil, coordinationError(err)
			}
			return map[string]any{"deliveries": result}, nil
		}
		if method == "coordination.callback.cancel" {
			err = s.coordinationStore.CancelCallback(ctx, peer.ID, p.SubscriptionID)
		} else {
			err = s.coordinationStore.RetryCallback(ctx, peer.ID, p.SubscriptionID, p.Sequence)
		}
		if err != nil {
			return nil, coordinationError(err)
		}
		return map[string]bool{"ok": true}, nil
	default:
		return nil, &RPCError{Code: MethodNotFound, Message: fmt.Sprintf("Unknown callback operation.")}
	}
}
