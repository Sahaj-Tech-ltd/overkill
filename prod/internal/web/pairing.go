package web

import (
	"encoding/json"
	"net/http"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

// pairingRoutes wires the DM pairing API endpoints (§7.1.7).
// The pairing store is read from the server config's security pairing
// field. If no store is configured, the endpoints return 503.
func (s *Server) pairingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/pairing/pending", s.auth(s.handlePairingPending))
	mux.HandleFunc("/api/pairing/approve", s.auth(limitBody(s.handlePairingApprove)))
	mux.HandleFunc("/api/pairing/deny", s.auth(limitBody(s.handlePairingDeny)))
	mux.HandleFunc("/api/pairing/known", s.auth(s.handlePairingKnown))
}

// pairingStore returns the configured pairing store or nil.
func (s *Server) pairingStore() *security.PairingStore {
	return s.cfg.PairingStore
}

func (s *Server) handlePairingPending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	ps := s.pairingStore()
	if ps == nil {
		writeJSON(w, http.StatusOK, []security.PairingRequest{})
		return
	}
	list, err := ps.ListPending()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePairingApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Channel string `json:"channel"`
		Code    string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Channel == "" || body.Code == "" {
		http.Error(w, "channel and code required", http.StatusBadRequest)
		return
	}
	ps := s.pairingStore()
	if ps == nil {
		http.Error(w, "pairing not configured", http.StatusServiceUnavailable)
		return
	}
	sender, err := ps.ApproveCode(body.Channel, body.Code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, sender)
}

func (s *Server) handlePairingDeny(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Channel  string `json:"channel"`
		SenderID string `json:"senderId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Channel == "" || body.SenderID == "" {
		http.Error(w, "channel and senderId required", http.StatusBadRequest)
		return
	}
	ps := s.pairingStore()
	if ps == nil {
		http.Error(w, "pairing not configured", http.StatusServiceUnavailable)
		return
	}
	if err := ps.RemoveSender(body.Channel, body.SenderID, "default"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "denied"})
}

func (s *Server) handlePairingKnown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		http.Error(w, "?channel= required", http.StatusBadRequest)
		return
	}
	ps := s.pairingStore()
	if ps == nil {
		writeJSON(w, http.StatusOK, []security.KnownSender{})
		return
	}
	list, err := ps.ListKnown(channel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
