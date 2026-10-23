package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
)

func reconciliationFields(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	if len(raw) > 32*1024 {
		return nil, fmt.Errorf("reconciliation parameters exceed limit")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("reconciliation object required")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		known := false
		for _, candidate := range allowed {
			if name == candidate {
				known = true
			}
		}
		if !ok || !known {
			return nil, fmt.Errorf("unknown reconciliation field")
		}
		if _, seen := fields[name]; seen {
			return nil, fmt.Errorf("duplicate reconciliation field")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("one reconciliation object required")
	}
	return fields, nil
}
func reconciliationString(raw json.RawMessage) (string, error) {
	var value string
	if len(raw) == 0 || raw[0] != '"' {
		return "", fmt.Errorf("explicit string required")
	}
	err := json.Unmarshal(raw, &value)
	return value, err
}

func (s *Server) handleUsageReconciliation(ctx context.Context, method string, raw []byte) (any, *RPCError) {
	allowed := []string{"session_id", "message_id"}
	if method == "coordination.reconciliation.candidates" {
		allowed = []string{"session_id", "message_id", "after_id", "limit"}
	}
	if method == "coordination.reconcile" {
		allowed = []string{"session_id", "input"}
	}
	fields, err := reconciliationFields(raw, allowed...)
	if err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
	}
	sid, err := reconciliationString(fields["session_id"])
	if err != nil || !canvasSessionID.MatchString(sid) {
		return nil, &RPCError{Code: InvalidParams, Message: "Valid reconciliation session required."}
	}
	if s.coordinationStore == nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Agent accounting is unavailable."}
	}
	peer, err := s.coordinationStore.PeerForSession(ctx, sid)
	if err != nil {
		return nil, coordinationError(err)
	}
	if method == "coordination.reconciliation.candidates" {
		id, err := reconciliationString(fields["message_id"])
		if err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: "Message ID required."}
		}
		after := ""
		if value, ok := fields["after_id"]; ok {
			after, err = reconciliationString(value)
			if err != nil {
				return nil, &RPCError{Code: InvalidParams, Message: "Invalid candidate cursor."}
			}
		}
		limit := 25
		if value, ok := fields["limit"]; ok {
			if bytes.Equal(value, []byte("null")) || json.Unmarshal(value, &limit) != nil || limit < 1 || limit > 50 {
				return nil, &RPCError{Code: InvalidParams, Message: "Candidate limit must be 1..50."}
			}
		}
		page, err := s.coordinationStore.UsageReconciliationCandidates(ctx, peer.ID, id, after, limit)
		if err != nil {
			return nil, coordinationError(err)
		}
		return page, nil
	}
	if method == "coordination.reconciliation" {
		id, err := reconciliationString(fields["message_id"])
		if err != nil {
			return nil, &RPCError{Code: InvalidParams, Message: "Message ID required."}
		}
		snapshot, err := s.coordinationStore.UsageReconciliationForMessage(ctx, peer.ID, id)
		if err != nil {
			return nil, coordinationError(err)
		}
		return map[string]any{"snapshot": snapshot}, nil
	}
	names := []string{"message_id", "expected_message_revision", "expected_usage_revision", "expected_journal_fingerprint", "reconciliation_id", "input_tokens", "output_tokens", "cached_input_tokens", "cost_usd", "usage_known", "price_known", "reason", "evidence", "basis", "acknowledge_uncertainty"}
	inputFields, err := reconciliationFields(fields["input"], names...)
	if err != nil || len(inputFields) != len(names) {
		return nil, &RPCError{Code: InvalidParams, Message: "Every reconciliation value must be explicit; unknown or duplicate fields are rejected."}
	}
	for _, name := range names {
		value := inputFields[name]
		if len(value) == 0 || bytes.Equal(value, []byte("null")) {
			return nil, &RPCError{Code: InvalidParams, Message: "Reconciliation values cannot be null."}
		}
	}
	for _, name := range []string{"usage_known", "price_known", "acknowledge_uncertainty"} {
		if !bytes.Equal(inputFields[name], []byte("true")) {
			return nil, &RPCError{Code: InvalidParams, Message: "Explicit manual accounting acknowledgement is required."}
		}
	}
	var input coordination.ReconcileTaskUsageInput
	if err := json.Unmarshal(fields["input"], &input); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: "Invalid reconciliation values."}
	}
	// This read is participant-filtered; owner authority is checked again by the
	// committing transaction. Only authenticated owner conflicts become typed
	// definitive non-commit results, never authorization/unknown database failures.
	snapshot, err := s.coordinationStore.UsageReconciliationForMessage(ctx, peer.ID, input.MessageID)
	if err != nil {
		return nil, coordinationError(err)
	}
	if snapshot.OwnerPeerID != peer.ID {
		return nil, coordinationError(coordination.ErrUnauthorized)
	}
	result, err := s.coordinationStore.ReconcileTaskUsage(ctx, peer.ID, input)
	if errors.Is(err, coordination.ErrConflict) {
		return map[string]any{"committed": false, "conflict": true}, nil
	}
	if err != nil {
		return nil, coordinationError(err)
	}
	return map[string]any{"committed": true, "usage": result.Usage, "snapshot": result.Snapshot, "reconciliation": result.Reconciliation}, nil
}
