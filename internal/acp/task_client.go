package acp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// SubmitTask persists a request and returns its handle without waiting for the
// agent. The caller must retain/reuse the idempotency key across uncertain POSTs.
func (c *Client) SubmitTask(ctx context.Context, input TaskSubmitRequest) (Task, error) {
	task, err := c.taskRequest(ctx, http.MethodPost, "/v1/tasks", input, "")
	if err == nil && task.SessionID != input.SessionID {
		return Task{}, fmt.Errorf("acp/client: submitted task receipt has a different session")
	}
	return task, err
}
func (c *Client) TaskStatus(ctx context.Context, id string) (Task, error) {
	if !validTaskHandle(id) {
		return Task{}, fmt.Errorf("acp/client: taskID required")
	}
	return c.taskRequest(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(id), nil, id)
}

// AwaitTask bounds only this observation; timeout/disconnect never cancels work.
func (c *Client) AwaitTask(ctx context.Context, id string, wait time.Duration) (Task, error) {
	if !validTaskHandle(id) || wait < 0 || wait > 25*time.Second {
		return Task{}, fmt.Errorf("acp/client: taskID and wait between 0 and 25 seconds required")
	}
	path := "/v1/tasks/" + url.PathEscape(id) + "/await?timeoutMs=" + strconv.FormatInt(wait.Milliseconds(), 10)
	return c.taskRequest(ctx, http.MethodGet, path, nil, id)
}
func (c *Client) CancelTask(ctx context.Context, id string) (Task, error) {
	if !validTaskHandle(id) {
		return Task{}, fmt.Errorf("acp/client: taskID required")
	}
	return c.taskRequest(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(id)+"/cancel", struct{}{}, id)
}
func (c *Client) taskRequest(ctx context.Context, method, path string, body any, expected string) (Task, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	response, err := c.request(ctx, method, path, body)
	if err != nil {
		return Task{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return Task{}, fmt.Errorf("acp/client: task request status %d", response.StatusCode)
	}
	var task Task
	if err = decodeResponse(ctx, response.Body, &task); err != nil {
		return Task{}, err
	}
	if !validTaskHandle(task.TaskID) || !validTaskHandle(task.MessageID) || task.SessionID == "" || (expected != "" && task.TaskID != expected) {
		return Task{}, fmt.Errorf("acp/client: invalid task receipt identity")
	}
	switch task.Status {
	case "queued", "running", "completed", "failed", "cancelled", "interrupted":
	default:
		return Task{}, fmt.Errorf("acp/client: invalid task status")
	}
	return task, nil
}

func validTaskHandle(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
