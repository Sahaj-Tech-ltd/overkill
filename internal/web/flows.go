package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// flowRow is the JSON shape returned by the flows API.
type flowRow struct {
	ID            string    `json:"id"`
	SessionKey    string    `json:"sessionKey"`
	ControllerID  string    `json:"controllerId"`
	Revision      int       `json:"revision"`
	Status        string    `json:"status"`
	Goal          string    `json:"goal"`
	CurrentStep   string    `json:"currentStep,omitempty"`
	StateJSON     string    `json:"stateJson,omitempty"`
	WaitJSON      string    `json:"waitJson,omitempty"`
	BlockedTaskID string    `json:"blockedTaskId,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// handleFlowsList returns all managed flows for the current session.
// GET /api/flows
//
// [NOT IMPLEMENTED] The flows system requires a durable task-flow store
// (Postgres backed, per-session, with revision-based optimistic locking).
// The schema (flowRow) is defined; storage wiring is deferred.
func (s *Server) handleFlowsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	http.Error(w, "flows: list not implemented — task-flow storage not yet wired [NOT IMPLEMENTED]", http.StatusNotImplemented)
}

// handleFlowsCreate creates a new managed TaskFlow.
// POST /api/flows
//
// [NOT IMPLEMENTED] Flow creation requires durable task-flow storage
// (Postgres backed, per-session). The request schema is defined;
// storage wiring is deferred.
func (s *Server) handleFlowsCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionKey   string `json:"sessionKey"`
		ControllerID string `json:"controllerId"`
		Goal         string `json:"goal"`
		StateJSON    string `json:"stateJson,omitempty"`
		WaitJSON     string `json:"waitJson,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Goal == "" {
		http.Error(w, "goal required", http.StatusBadRequest)
		return
	}
	http.Error(w, "flows: create not implemented — task-flow storage not yet wired [NOT IMPLEMENTED]", http.StatusNotImplemented)
}

// handleFlowsSub routes /api/flows/<id> and /api/flows/<id>/<action>.
func (s *Server) handleFlowsSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/flows/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		// GET /api/flows/:id
		s.handleFlowsGet(w, r, id)
	case action == "resume" && r.Method == http.MethodPost:
		s.handleFlowsAction(w, r, id, "resume")
	case action == "finish" && r.Method == http.MethodPost:
		s.handleFlowsAction(w, r, id, "finish")
	case action == "fail" && r.Method == http.MethodPost:
		s.handleFlowsAction(w, r, id, "fail")
	case action == "cancel" && r.Method == http.MethodPost:
		s.handleFlowsAction(w, r, id, "cancel")
	case action == "tasks" && r.Method == http.MethodPost:
		s.handleFlowsAction(w, r, id, "tasks")
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFlowsGet(w http.ResponseWriter, r *http.Request, id string) {
	// [NOT IMPLEMENTED] Flow retrieval requires durable task-flow storage.
	http.Error(w, "flows: get not implemented — task-flow storage not yet wired [NOT IMPLEMENTED]", http.StatusNotImplemented)
}

func (s *Server) handleFlowsAction(w http.ResponseWriter, r *http.Request, id, action string) {
	var body struct {
		ExpectedRevision int    `json:"expectedRevision"`
		Status           string `json:"status,omitempty"`
		CurrentStep      string `json:"currentStep,omitempty"`
		StateJSON        string `json:"stateJson,omitempty"`
		WaitJSON         string `json:"waitJson,omitempty"`
		BlockedTaskID    string `json:"blockedTaskId,omitempty"`
		BlockedSummary   string `json:"blockedSummary,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// [NOT IMPLEMENTED] Flow actions (resume, finish, fail, cancel, tasks)
	// require durable task-flow storage with revision-based optimistic locking.
	http.Error(w, fmt.Sprintf("flows: %s not implemented — task-flow storage not yet wired [NOT IMPLEMENTED]", action), http.StatusNotImplemented)
}

// flowRoutes wires the /api/flows endpoints into the server mux.
// Called from server.go after the base routes are registered.
func (s *Server) flowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/flows", s.auth(limitBody(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleFlowsList(w, r)
		case http.MethodPost:
			s.handleFlowsCreate(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.HandleFunc("/api/flows/", s.auth(limitBody(s.handleFlowsSub)))
}