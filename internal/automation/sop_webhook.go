// Package automation — HTTP webhook triggers for SOPs (§7.1 Layer 3).
//
// External systems (CI hooks, monitoring alerts, MQTT-to-HTTP relays)
// can kick off a stored SOP by POSTing to this server. Endpoints:
//
//	POST /sop/{id}      → start the SOP by ID
//	GET  /sop           → list registered SOP IDs and statuses
//	GET  /health        → 204 for liveness probes
//
// Loopback-only by default; expose publicly only behind a reverse
// proxy + authentication. The optional Token field enforces a
// `Authorization: Bearer ...` header; empty disables auth.
//
// We deliberately do NOT accept full SOP definitions over the wire —
// only IDs. The agent (or `overkill sop create`) is the source of
// truth for SOP content; webhooks are triggers, not deployments.
package automation

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// SOPWebhookServer wraps an SOPEngine with an HTTP trigger surface.
type SOPWebhookServer struct {
	Engine *SOPEngine
	Listen string        // default 127.0.0.1:7801
	Token  string        // empty = no auth (loopback-only deployments)
	Logger *http.Handler // unused; reserved for future hook-in

	server *http.Server
}

// NewSOPWebhookServer wires the server. Both Listen and Token may be
// empty; Listen defaults to loopback, Token to "no auth".
func NewSOPWebhookServer(engine *SOPEngine) *SOPWebhookServer {
	return &SOPWebhookServer{Engine: engine}
}

// Run starts the HTTP server and blocks until ctx cancels.
func (s *SOPWebhookServer) Run(ctx context.Context) error {
	if s.Engine == nil {
		return fmt.Errorf("sop webhook: engine required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/sop", s.handleList)
	mux.HandleFunc("/sop/", s.handleTrigger)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	addr := s.Listen
	if addr == "" {
		addr = config.DefaultSOPWebhookAddr
	}
	s.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.server.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutCtx)
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// Shutdown lets callers stop the server outside its Run context. No-op
// when the server hasn't started.
func (s *SOPWebhookServer) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *SOPWebhookServer) authorized(r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	got := strings.TrimPrefix(h, "Bearer ")
	// Constant-time compare so token-length / prefix-equality timing
	// can't be used to recover the secret one byte at a time.
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func (s *SOPWebhookServer) handleList(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type entry struct {
		ID     string    `json:"id"`
		Name   string    `json:"name,omitempty"`
		Status SOPStatus `json:"status"`
	}
	var out []entry
	for _, sop := range s.Engine.List() {
		out = append(out, entry{ID: sop.ID, Name: sop.Name, Status: sop.Status})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleTrigger accepts POST /sop/{id} and starts the SOP. The
// execution runs in a goroutine — we ack 202 Accepted immediately
// and the client polls /sop/{id} for status (the engine's Get is
// the source of truth). Long-running SOPs would block the HTTP
// handler otherwise.
func (s *SOPWebhookServer) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/sop/")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost:
		sop, ok := s.Engine.Get(id)
		if !ok {
			http.Error(w, "sop not found", http.StatusNotFound)
			return
		}
		// Fire-and-forget execute. The handler returns 202 and the
		// engine drives the SOP through its own steps; status is
		// observable via GET /sop or the engine's Get.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if err := s.Engine.Execute(ctx, id); err != nil {
				log.Printf("sop_webhook: execute %q failed: %v", id, err)
			}
		}()
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     sop.ID,
			"name":   sop.Name,
			"status": "started",
		})

	case http.MethodGet:
		sop, ok := s.Engine.Get(id)
		if !ok {
			http.Error(w, "sop not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sop)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
