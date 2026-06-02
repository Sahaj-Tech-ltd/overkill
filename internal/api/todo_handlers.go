package api

import (
	"context"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tasks"
)

// TaskDTO is the wire format the todo panel expects.
type TaskDTO struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Intent    string `json:"intent"`
	Status    string `json:"status"` // "open" | "shipped" | "abandoned"
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// TasksStoreAdapter adapts *tasks.Store for use as the server's TasksStore.
type TasksStoreAdapter struct{ S *tasks.Store }

func (a *TasksStoreAdapter) Add(sessionID, intent string) (*TaskDTO, error) {
	t, err := a.S.Open(sessionID, intent)
	if err != nil {
		return nil, fmt.Errorf("tasks: add: %w", err)
	}
	return taskFromCore(t), nil
}

func (a *TasksStoreAdapter) Toggle(id string) (*TaskDTO, error) {
	t, err := a.S.Get(id)
	if err != nil {
		return nil, fmt.Errorf("tasks: get: %w", err)
	}
	if t == nil {
		return nil, nil
	}
	next := tasks.StatusInProgress
	if t.Status == tasks.StatusInProgress {
		next = tasks.StatusShipped
	} else if t.Status == tasks.StatusShipped {
		next = tasks.StatusOpen
	}
	t2, err := a.S.SetStatus(id, next)
	if err != nil {
		return nil, fmt.Errorf("tasks: toggle: %w", err)
	}
	return taskFromCore(t2), nil
}

func (a *TasksStoreAdapter) Remove(id string) error {
	return a.S.Delete(id)
}

func (a *TasksStoreAdapter) List(sessionID string) ([]TaskDTO, error) {
	all, err := a.S.All()
	if err != nil {
		return nil, fmt.Errorf("tasks: list: %w", err)
	}
	out := make([]TaskDTO, 0, len(all))
	for _, t := range all {
		if t.SessionID == sessionID {
			out = append(out, *taskFromCore(t))
		}
	}
	if out == nil {
		out = []TaskDTO{}
	}
	return out, nil
}

func taskFromCore(t *tasks.Task) *TaskDTO {
	return &TaskDTO{
		ID:        t.ID,
		SessionID: t.SessionID,
		Intent:    t.Intent,
		Status:    string(t.Status),
		CreatedAt: t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// handleTodoAdd creates a new open task.
func (s *Server) handleTodoAdd(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		SessionID   string `json:"session_id"`
		Description string `json:"description"`
	}
	if len(params) > 0 {
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
	}
	if s.tasksStore == nil {
		return nil, &RPCError{Code: InternalError, Message: "tasks store not configured"}
	}
	task, err := s.tasksStore.Add(p.SessionID, p.Description)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}
	return task, nil
}

// handleTodoToggle flips a task between open and done.
func (s *Server) handleTodoToggle(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		ID string `json:"id"`
	}
	if len(params) > 0 {
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
	}
	if s.tasksStore == nil {
		return nil, &RPCError{Code: InternalError, Message: "tasks store not configured"}
	}
	task, err := s.tasksStore.Toggle(p.ID)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}
	return task, nil
}

// handleTodoDelete removes a task by ID.
func (s *Server) handleTodoDelete(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		ID string `json:"id"`
	}
	if len(params) > 0 {
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
	}
	if s.tasksStore == nil {
		return nil, &RPCError{Code: InternalError, Message: "tasks store not configured"}
	}
	if err := s.tasksStore.Remove(p.ID); err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}
	return map[string]string{"status": "deleted"}, nil
}

// handleTodoList returns all tasks for a session.
func (s *Server) handleTodoList(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if len(params) > 0 {
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
	}
	if s.tasksStore == nil {
		return []TaskDTO{}, nil
	}
	tasks, err := s.tasksStore.List(p.SessionID)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}
	if tasks == nil {
		tasks = []TaskDTO{}
	}
	return tasks, nil
}
