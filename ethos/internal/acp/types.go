// Package acp implements the Agent Communication Protocol — a small HTTP/SSE
// API that lets other agents (claude code, opencode, custom agents) send
// messages to a running ethos and receive streaming replies. Inverse of MCP.
package acp

import "time"

// Info is the response body of GET /v1/info.
type Info struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities"`
}

// SendRequest is the body of POST /v1/messages.
type SendRequest struct {
	From      string         `json:"from"`
	Content   string         `json:"content"`
	SessionID string         `json:"sessionID,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

// SendResponse is returned from POST /v1/messages.
type SendResponse struct {
	MessageID string `json:"messageID"`
	SessionID string `json:"sessionID"`
}

// Event is a single SSE frame on /v1/messages/{id}/events.
type Event struct {
	Type      string         `json:"type"` // text_delta | tool_call | done | error
	Content   string         `json:"content,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	ToolArgs  string         `json:"tool_args,omitempty"`
	Error     string         `json:"error,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Extra     map[string]any `json:"extra,omitempty"`
}
