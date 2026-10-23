package web

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CanvasEvent transports a committed custom-catalog update over the event bus.
// The action names are transport conventions, not full upstream A2UI support.
type CanvasEvent struct {
	Type      string    `json:"type"` // always "a2ui"
	SessionID string    `json:"sessionId"`
	SurfaceID string    `json:"surfaceId,omitempty"`
	Action    string    `json:"action"` // "surfaceUpdate", "beginRendering", etc.
	JSONL     string    `json:"jsonl"`  // One line of A2UI JSON
	Timestamp time.Time `json:"timestamp"`
	Revision  int64     `json:"revision"`
}

// canvasRoutes wires the canvas endpoints into the server.
func (s *Server) canvasRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/canvas/state", s.auth(s.handleCanvasState))
	mux.HandleFunc("/api/canvas/push", s.auth(limitBody(s.handleCanvasPush)))
	mux.HandleFunc("/api/canvas/snapshot", s.auth(s.handleCanvasSnapshot))
	mux.HandleFunc("/api/canvas/reset", s.auth(limitBody(s.handleCanvasReset)))
}

// handleCanvasPush validates and commits custom canvas JSONL before publishing.
func (s *Server) handleCanvasPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
		SurfaceID string `json:"surfaceId"`
		JSONL     string `json:"jsonl"` // One line of A2UI JSON
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := decodeCanvasRequest(data, &body, "sessionId", "surfaceId", "jsonl"); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	sid := body.SessionID
	if sid == "" && s.cfg.Agent != nil {
		sid = s.cfg.Agent.SessionID()
	}
	if !canvasIdentifier(sid) {
		http.Error(w, "valid sessionId required", http.StatusBadRequest)
		return
	}
	if body.SurfaceID != "" && !canvasIdentifier(body.SurfaceID) {
		http.Error(w, "invalid surfaceId", http.StatusBadRequest)
		return
	}
	action, surfaceID, err := validateCanvasEvent(body.JSONL, sid, body.SurfaceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	state, err := s.cfg.Canvas.Apply(r.Context(), sid, body.JSONL)
	if err != nil {
		canvasHTTPError(w, err)
		return
	}
	ev := CanvasEvent{
		Type:      "a2ui",
		SessionID: sid,
		SurfaceID: surfaceID,
		Action:    action,
		JSONL:     strings.TrimSpace(body.JSONL),
		Timestamp: time.Now().UTC(),
		Revision:  state.Revision,
	}
	// Marshal to wsEvent so it flows through the existing bus.
	data, err = json.Marshal(ev)
	if err != nil {
		log.Printf("web: canvas marshal: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.bus.publish(sid, wsEvent{
		Type:      "a2ui",
		SessionID: sid,
		Content:   string(data),
		Timestamp: ev.Timestamp,
	})

	writeJSON(w, http.StatusAccepted, map[string]any{"status": "pushed", "action": action, "revision": state.Revision})
}

// handleCanvasReset commits an empty state and then broadcasts its revision.
func (s *Server) handleCanvasReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := decodeCanvasRequest(data, &body, "sessionId"); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	sid := body.SessionID
	if sid == "" && s.cfg.Agent != nil {
		sid = s.cfg.Agent.SessionID()
	}
	if !canvasIdentifier(sid) {
		http.Error(w, "valid sessionId required", http.StatusBadRequest)
		return
	}

	state, err := s.cfg.Canvas.Reset(r.Context(), sid)
	if err != nil {
		canvasHTTPError(w, err)
		return
	}
	content, _ := json.Marshal(map[string]any{"action": "reset", "sessionId": sid, "revision": state.Revision})
	// Broadcast only after commit.
	s.bus.publish(sid, wsEvent{
		Type:      "a2ui",
		SessionID: sid,
		Content:   string(content),
		Timestamp: time.Now().UTC(),
	})

	writeJSON(w, http.StatusOK, map[string]any{"status": "reset", "revision": state.Revision})
}

// handleCanvasState reads only an existing session's committed document.
func (s *Server) handleCanvasState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	if queryErr != nil || len(query) != 1 || len(query["sessionId"]) != 1 || !canvasIdentifier(query.Get("sessionId")) {
		http.Error(w, "valid sessionId query required", http.StatusBadRequest)
		return
	}
	state, err := s.cfg.Canvas.Get(r.Context(), query.Get("sessionId"))
	if err != nil {
		canvasHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}
func canvasHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCanvasUnavailable):
		http.Error(w, "canvas store unavailable", http.StatusServiceUnavailable)
	case errors.Is(err, ErrCanvasSessionNotFound):
		http.Error(w, "canvas session not found", http.StatusNotFound)
	case errors.Is(err, ErrCanvasInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		log.Printf("web: canvas: %v", err)
		http.Error(w, "canvas operation failed", http.StatusInternalServerError)
	}
}

func canvasStoreError(w http.ResponseWriter, err error) { canvasHTTPError(w, err) }
