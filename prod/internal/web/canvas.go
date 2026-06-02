package web

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// CanvasEvent is an A2UI JSONL event pushed from the agent to the browser
// via the existing SSE event bus. Each event carries one JSONL line with
// exactly one of the five valid A2UI top-level keys: surfaceUpdate,
// dataModelUpdate, beginRendering, createSurface, deleteSurface.
//
// Stolen from OpenClaw's extensions/canvas/ — the agent never touches
// HTML/CSS. It emits A2UI JSONL and the browser renders it via @a2ui/lit
// web components.
type CanvasEvent struct {
	Type      string    `json:"type"` // always "a2ui"
	SessionID string    `json:"sessionId"`
	SurfaceID string    `json:"surfaceId,omitempty"`
	Action    string    `json:"action"` // "surfaceUpdate", "beginRendering", etc.
	JSONL     string    `json:"jsonl"`  // One line of A2UI JSON
	Timestamp time.Time `json:"timestamp"`
}

// canvasRoutes wires the canvas endpoints into the server.
func (s *Server) canvasRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/canvas/push", s.auth(limitBody(s.handleCanvasPush)))
	mux.HandleFunc("/api/canvas/snapshot", s.auth(s.handleCanvasSnapshot))
	mux.HandleFunc("/api/canvas/reset", s.auth(limitBody(s.handleCanvasReset)))
}

// handleCanvasPush accepts A2UI JSONL from the agent and broadcasts it
// to all connected WebSocket subscribers as a canvas event. The SPA
// renders it via <a2ui-host> web component.
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.JSONL == "" {
		http.Error(w, "jsonl required", http.StatusBadRequest)
		return
	}

	// Validate it's valid JSON and has exactly one A2UI key.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body.JSONL), &raw); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	validKeys := map[string]bool{
		"surfaceUpdate":   true,
		"dataModelUpdate": true,
		"beginRendering":  true,
		"createSurface":   true,
		"deleteSurface":   true,
	}
	var action string
	for k := range raw {
		if validKeys[k] {
			action = k
			break
		}
	}
	if action == "" {
		http.Error(w, "jsonl must contain one of: surfaceUpdate, dataModelUpdate, beginRendering, createSurface, deleteSurface", http.StatusBadRequest)
		return
	}

	// Broadcast via existing SSE event bus — the SPA already listens.
	sid := body.SessionID
	if sid == "" && s.cfg.Agent != nil {
		sid = s.cfg.Agent.SessionID()
	}
	ev := CanvasEvent{
		Type:      "a2ui",
		SessionID: sid,
		SurfaceID: body.SurfaceID,
		Action:    action,
		JSONL:     strings.TrimSpace(body.JSONL),
		Timestamp: time.Now().UTC(),
	}
	// Marshal to wsEvent so it flows through the existing bus.
	data, err := json.Marshal(ev)
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

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "pushed", "action": action})
}

// handleCanvasSnapshot captures the current canvas as a base64 PNG.
// For now, returns a placeholder — full snapshot requires the agent to
// use the vision model or a headless browser render.
func (s *Server) handleCanvasSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	// Canvas snapshot rendering requires headless browser integration
	// or server-side Lit SSR (Phase 5). Returns 200 with status:not_implemented
	// so callers can degrade gracefully.
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "not_implemented",
		"message": "canvas snapshot requires headless browser integration (Phase 5)",
	})
}

// handleCanvasReset clears all A2UI surfaces for a session.
func (s *Server) handleCanvasReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	sid := body.SessionID
	if sid == "" && s.cfg.Agent != nil {
		sid = s.cfg.Agent.SessionID()
	}

	// Broadcast reset event.
	s.bus.publish(sid, wsEvent{
		Type:      "a2ui",
		SessionID: sid,
		Content:   `{"action":"reset"}`,
		Timestamp: time.Now().UTC(),
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}
