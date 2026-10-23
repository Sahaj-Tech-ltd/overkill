package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/audit"
)

// session.audit is an explicit user operation. It runs project verification
// scripts only after the client requests action=run; baseline capture is reads.
func (s *Server) handleSessionAudit(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		SessionID string        `json:"session_id"`
		Action    string        `json:"action"`
		Claims    []audit.Claim `json:"claims"`
	}
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" || (p.Action != "snapshot" && p.Action != "run") {
		return nil, &RPCError{Code: InvalidParams, Message: "session_id and action snapshot|run required"}
	}
	release, err := s.beginSessionOperation(p.SessionID)
	if err != nil {
		return nil, &RPCError{Code: InvalidRequest, Message: err.Error()}
	}
	defer release()
	a, rpcErr := s.getOrCreateAdmittedAgent(ctx, p.SessionID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	s.mu.Lock()
	runtime := s.sessionRuntimes[p.SessionID]
	s.mu.Unlock()
	if runtime == nil || runtime.CompletionAuditor == nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "completion auditor unavailable for session"}
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if p.Action == "snapshot" {
		snapshot, err := runtime.CompletionAuditor.CaptureBaseline(runCtx)
		if err != nil {
			return nil, &RPCError{Code: InternalError, Message: err.Error()}
		}
		return map[string]any{"captured": true, "files": len(snapshot.Files), "time": snapshot.Time}, nil
	}
	history, _ := json.Marshal(a.History())
	runCtx = audit.WithTranscript(runCtx, string(history))
	report, err := runtime.CompletionAuditor.AuditCaptured(runCtx, p.Claims)
	if err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: fmt.Sprint(err)}
	}
	return report, nil
}
