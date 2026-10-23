package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
)

// CoordinationRequest deliberately has no sender, project or credential fields.
// The authenticated peer token alone supplies the actor for every operation.
type CoordinationRequest struct {
	Execution        *coordination.TransportExecution `json:"execution,omitempty"`
	Send             *coordination.SendInput          `json:"send,omitempty"`
	MessageID        string                           `json:"message_id,omitempty"`
	Payload          json.RawMessage                  `json:"payload,omitempty"`
	IdempotencyKey   string                           `json:"idempotency_key,omitempty"`
	ExpectedRevision int64                            `json:"expected_revision,omitempty"`
	Event            string                           `json:"event,omitempty"`
	AfterRevision    int64                            `json:"after_revision,omitempty"`
	Limit            int                              `json:"limit,omitempty"`
	WaitMilliseconds int                              `json:"wait_milliseconds,omitempty"`
}
type CoordinationResponse struct {
	NextRevision int64                  `json:"next_revision,omitempty"`
	HasMore      bool                   `json:"has_more,omitempty"`
	Self         *coordination.Peer     `json:"self,omitempty"`
	Message      *coordination.Message  `json:"message,omitempty"`
	Messages     []coordination.Message `json:"messages,omitempty"`
	Peers        []coordination.Peer    `json:"peers,omitempty"`
	Events       []coordination.Event   `json:"events,omitempty"`
	TimedOut     bool                   `json:"timed_out,omitempty"`
}

func coordinationHTTPError(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, coordination.ErrUnauthorized):
		code = http.StatusUnauthorized
	case errors.Is(err, coordination.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, coordination.ErrConflict), errors.Is(err, coordination.ErrIdempotency), errors.Is(err, coordination.ErrBusy):
		code = http.StatusConflict
	case errors.Is(err, context.DeadlineExceeded):
		code = http.StatusGatewayTimeout
	}
	// Never reflect request contents, tokens, SQL diagnostics or provider errors.
	writeErrorJSON(w, code, "coordination request rejected")
}
func (s *Server) handleCoordination(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == r.Header.Get("Authorization") {
		writeErrorJSON(w, 401, "unauthorized")
		return
	}
	peer, scoped, err := s.coordination.AuthenticateScoped(ctx, token)
	if err != nil {
		writeErrorJSON(w, 401, "unauthorized")
		return
	}
	ctx = scoped
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var in CoordinationRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 72*1024))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&in); err != nil {
		writeErrorJSON(w, 400, "invalid coordination request")
		return
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		writeErrorJSON(w, 400, "invalid coordination request")
		return
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > 200 || in.AfterRevision < 0 || in.WaitMilliseconds < 0 || in.WaitMilliseconds > 25000 {
		writeErrorJSON(w, 400, "invalid coordination bounds")
		return
	}
	out := CoordinationResponse{Self: &peer}
	var m coordination.Message
	operation := strings.TrimPrefix(r.URL.Path, "/v1/coordination/")
	if in.Execution != nil {
		if operation != "send" && operation != "reply" {
			writeErrorJSON(w, 400, "execution proof not applicable")
			return
		}
		ctx, err = coordination.WithTransportExecution(ctx, peer.ID, *in.Execution)
		if err != nil {
			writeErrorJSON(w, 401, "invalid execution proof")
			return
		}
	}
	switch operation {
	case "peers":
		out.Peers, err = s.coordination.Peers(ctx, peer.ID)
	case "send":
		if in.Send == nil {
			writeErrorJSON(w, 400, "send required")
			return
		}
		if in.Execution != nil {
			m, err = s.coordination.Send(ctx, peer.ID, *in.Send)
		} else {
			m, err = s.coordination.ExternalSend(ctx, peer.ID, *in.Send)
		}
		out.Message = &m
	case "reply":
		if in.Execution != nil {
			m, err = s.coordination.Reply(ctx, peer.ID, in.MessageID, in.Payload, in.IdempotencyKey)
		} else {
			m, err = s.coordination.ExternalReply(ctx, peer.ID, in.MessageID, in.Payload, in.IdempotencyKey)
		}
		out.Message = &m
	case "inbox":
		out.Messages, err = s.coordination.Inbox(ctx, peer.ID, in.Limit)
	case "recover":
		err = s.coordination.RecoverExternal(ctx, peer.ID)
	case "claim":
		m, err = s.coordination.ExternalClaim(ctx, peer.ID)
		out.Message = &m
	case "status":
		m, err = s.coordination.Read(ctx, peer.ID, in.MessageID)
		out.Message = &m
		if err == nil {
			out.Events, err = s.coordination.MessageEvents(ctx, peer.ID, in.MessageID, in.AfterRevision, in.Limit+1)
			if len(out.Events) > in.Limit {
				out.HasMore = true
				out.Events = out.Events[:in.Limit]
			}
			out.NextRevision = in.AfterRevision
			if len(out.Events) > 0 {
				out.NextRevision = out.Events[len(out.Events)-1].Revision
			}
		}
	case "await":
		m, err = s.coordination.Read(ctx, peer.ID, in.MessageID)
		if err == nil && !m.State.Terminal() && m.Revision <= in.AfterRevision {
			if in.WaitMilliseconds == 0 {
				out.TimedOut = true
			} else {
				waitCtx, stop := context.WithTimeout(ctx, time.Duration(in.WaitMilliseconds)*time.Millisecond)
				m, err = s.coordination.Await(waitCtx, peer.ID, in.MessageID, in.AfterRevision)
				stop()
				if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
					m, err = s.coordination.Read(ctx, peer.ID, in.MessageID)
					out.TimedOut = err == nil && !m.State.Terminal() && m.Revision <= in.AfterRevision
				}
			}
		}
		out.Message = &m
	case "cancel":
		m, err = s.coordination.Transition(ctx, peer.ID, in.MessageID, in.ExpectedRevision, "cancelled", in.Payload)
		out.Message = &m
	case "transition":
		m, err = s.coordination.ExternalTransition(ctx, peer.ID, in.MessageID, in.ExpectedRevision, in.Event, in.Payload)
		out.Message = &m
	default:
		writeErrorJSON(w, 404, "unknown coordination operation")
		return
	}
	if err != nil {
		coordinationHTTPError(w, err)
		return
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > maxResponseBytes {
		writeErrorJSON(w, 413, "coordination response exceeds bound; narrow query")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}
