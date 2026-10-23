package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// flowRow is the JSON shape returned by the flows API.
type flowRow struct {
	ID             string    `json:"id"`
	SessionKey     string    `json:"sessionKey"`
	ControllerID   string    `json:"controllerId"`
	Revision       int64     `json:"revision"`
	Status         string    `json:"status"`
	Goal           string    `json:"goal"`
	CurrentStep    string    `json:"currentStep,omitempty"`
	StateJSON      string    `json:"stateJson,omitempty"`
	WaitJSON       string    `json:"waitJson,omitempty"`
	BlockedTaskID  string    `json:"blockedTaskId,omitempty"`
	BlockedSummary string    `json:"blockedSummary,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

var flowIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// Bodies are single objects; duplicate keys, unknown fields and trailing values
// are rejected before a mutation can consume a revision.
func decodeFlowRequest(r *http.Request, value any, allowed ...string) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := decodeCanvasJSON(raw, &fields); err != nil {
		return err
	}
	for key, value := range fields {
		known := false
		for _, name := range allowed {
			if key == name {
				known = true
				break
			}
		}
		if !known || string(value) == "null" {
			return errors.New("unknown or null request field")
		}
	}
	return decodeCanvasJSON(raw, value)
}

func (s *Server) flowSession(w http.ResponseWriter, r *http.Request, requested string) (string, bool) {
	if s.cfg.Flows == nil || s.cfg.Flows.db == nil || s.cfg.Store == nil {
		http.Error(w, "managed flow storage unavailable", http.StatusServiceUnavailable)
		return "", false
	}
	if requested == "" && s.cfg.Agent != nil {
		requested = s.cfg.Agent.SessionID()
	}
	if !flowIdentifier.MatchString(requested) {
		http.Error(w, "valid sessionKey required", http.StatusBadRequest)
		return "", false
	}
	sess, err := s.cfg.Store.Load(r.Context(), requested)
	if errors.Is(err, session.ErrNotFound) || (err == nil && sess == nil) {
		http.Error(w, "session not found", http.StatusNotFound)
		return "", false
	}
	if err != nil {
		http.Error(w, "session storage unavailable", http.StatusInternalServerError)
		return "", false
	}
	return requested, true
}

func (s *Server) flowQuerySession(w http.ResponseWriter, r *http.Request) (string, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid query encoding", http.StatusBadRequest)
		return "", false
	}
	if len(query["sessionKey"]) > 1 {
		http.Error(w, "single sessionKey required", http.StatusBadRequest)
		return "", false
	}
	return s.flowSession(w, r, query.Get("sessionKey"))
}

func flowFailure(w http.ResponseWriter, err error) {
	var databaseError *pq.Error
	if errors.As(err, &databaseError) && strings.HasPrefix(string(databaseError.Code), "22") {
		http.Error(w, "flow input cannot be represented in storage", http.StatusBadRequest)
		return
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "flow not found", http.StatusNotFound)
	case errors.Is(err, errFlowConflict):
		http.Error(w, "flow revision or state conflict; reload before retrying", http.StatusConflict)
	default:
		http.Error(w, "flow storage operation failed", http.StatusInternalServerError)
	}
}

func validFlowJSON(value string) bool { return len(value) <= 256*1024 && json.Valid([]byte(value)) }

func emptyFlowWait(value string) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal([]byte(value), &object) == nil && object != nil && len(object) == 0
}

// GET /api/flows?sessionKey=...&limit=100&offset=0. Offset pagination is
// explicit; concurrent inserts can move page boundaries, so refresh to reconcile.
func (s *Server) handleFlowsList(w http.ResponseWriter, r *http.Request) {
	sid, ok := s.flowQuerySession(w, r)
	if !ok {
		return
	}
	limit, offset := 100, 0
	for key, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		values := r.URL.Query()[key]
		if len(values) > 1 {
			http.Error(w, "invalid pagination", http.StatusBadRequest)
			return
		}
		if len(values) == 1 {
			n, err := strconv.Atoi(values[0])
			if err != nil {
				http.Error(w, "invalid pagination", http.StatusBadRequest)
				return
			}
			*target = n
		}
	}
	// Reserve room for the next page without wrapping an int. An arbitrary
	// offset cap could otherwise produce a nextOffset the API itself rejects.
	if limit < 1 || limit > 200 || offset < 0 || offset > int(^uint(0)>>1)-limit {
		http.Error(w, "invalid pagination", http.StatusBadRequest)
		return
	}
	rows, err := s.cfg.Flows.list(r.Context(), sid, limit+1, offset)
	if err != nil {
		flowFailure(w, err)
		return
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	var next *int
	if more {
		n := offset + len(rows)
		next = &n
	}
	writeJSON(w, http.StatusOK, struct {
		Flows      []flowRow `json:"flows"`
		NextOffset *int      `json:"nextOffset"`
		HasMore    bool      `json:"hasMore"`
	}{rows, next, more})
}

func (s *Server) handleFlowsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionKey   string `json:"sessionKey"`
		ControllerID string `json:"controllerId"`
		Goal         string `json:"goal"`
		StateJSON    string `json:"stateJson,omitempty"`
		WaitJSON     string `json:"waitJson,omitempty"`
	}
	if err := decodeFlowRequest(r, &body, "sessionKey", "controllerId", "goal", "stateJson", "waitJson"); err != nil {
		http.Error(w, "invalid flow request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Goal) == "" || len(body.Goal) > 32768 || len(body.ControllerID) > 128 {
		http.Error(w, "goal required and text limits apply", http.StatusBadRequest)
		return
	}
	if body.StateJSON == "" {
		body.StateJSON = "{}"
	}
	if body.WaitJSON == "" {
		body.WaitJSON = "{}"
	}
	if !validFlowJSON(body.StateJSON) || !validFlowJSON(body.WaitJSON) {
		http.Error(w, "stateJson/waitJson must be valid bounded JSON", http.StatusBadRequest)
		return
	}
	sid, ok := s.flowSession(w, r, body.SessionKey)
	if !ok {
		return
	}
	if body.ControllerID == "" {
		body.ControllerID = "user"
	}
	f, err := s.cfg.Flows.create(r.Context(), flowRow{ID: uuid.NewString(), SessionKey: sid, ControllerID: body.ControllerID, Goal: body.Goal, StateJSON: body.StateJSON, WaitJSON: body.WaitJSON})
	if err != nil {
		flowFailure(w, err)
		return
	}
	s.publishFlow(f)
	w.Header().Set("Location", "/api/flows/"+f.ID+"?sessionKey="+f.SessionKey)
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) handleFlowsSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/flows/"), "/")
	if len(parts) < 1 || len(parts) > 2 || !flowIdentifier.MatchString(parts[0]) {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		s.handleFlowsGet(w, r, parts[0])
		return
	}
	switch parts[1] {
	case "resume", "finish", "fail", "cancel", "tasks":
	default:
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	s.handleFlowsAction(w, r, parts[0], parts[1])
}

func (s *Server) handleFlowsGet(w http.ResponseWriter, r *http.Request, id string) {
	sid, ok := s.flowQuerySession(w, r)
	if !ok {
		return
	}
	f, err := s.cfg.Flows.get(r.Context(), sid, id)
	if err != nil {
		flowFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) handleFlowsAction(w http.ResponseWriter, r *http.Request, id, action string) {
	var patch flowPatch
	if err := decodeFlowRequest(r, &patch, "expectedRevision", "status", "currentStep", "stateJson", "waitJson", "blockedTaskId", "blockedSummary"); err != nil || patch.ExpectedRevision < 1 {
		http.Error(w, "valid body and positive expectedRevision required", http.StatusBadRequest)
		return
	}
	if patch.Status != nil && (action != "tasks" || (*patch.Status != "running" && *patch.Status != "waiting" && *patch.Status != "blocked")) {
		http.Error(w, "status is only supported for tasks: running, waiting or blocked", http.StatusBadRequest)
		return
	}
	if action != "tasks" && (patch.WaitJSON != nil || patch.BlockedTaskID != nil || patch.BlockedSummary != nil) {
		http.Error(w, "wait and blocker fields are only supported for tasks", http.StatusBadRequest)
		return
	}
	if (patch.CurrentStep != nil && len(*patch.CurrentStep) > 4096) || (patch.BlockedTaskID != nil && len(*patch.BlockedTaskID) > 128) || (patch.BlockedSummary != nil && len(*patch.BlockedSummary) > 32768) {
		http.Error(w, "flow text limit exceeded", http.StatusBadRequest)
		return
	}
	for _, value := range []*string{patch.StateJSON, patch.WaitJSON} {
		if value != nil && !validFlowJSON(*value) {
			http.Error(w, "stateJson/waitJson must be valid bounded JSON", http.StatusBadRequest)
			return
		}
	}
	sid, ok := s.flowQuerySession(w, r)
	if !ok {
		return
	}
	f, err := s.cfg.Flows.apply(r.Context(), sid, id, action, patch)
	if err != nil {
		flowFailure(w, err)
		return
	}
	s.publishFlow(f)
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) publishFlow(f flowRow) {
	data, _ := json.Marshal(f)
	s.bus.publish(f.SessionKey, wsEvent{Type: "flow_updated", SessionID: f.SessionKey, Content: string(data), Timestamp: f.UpdatedAt})
}

func (s *Server) flowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/flows", s.auth(limitBody(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleFlowsList(w, r)
		case http.MethodPost:
			s.handleFlowsCreate(w, r)
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})))
	mux.HandleFunc("/api/flows/", s.auth(limitBody(s.handleFlowsSub)))
}
