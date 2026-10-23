package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
)

// ACPSendTool dispatches a sub-task to another ACP agent on the network.
type ACPSendTool struct{ async Tool }

func NewACPSendTool(peers ...map[string]ACPRemote) *ACPSendTool {
	t := &ACPSendTool{}
	if len(peers) > 0 {
		for _, tool := range NewACPTaskTools(peers[0]) {
			if tool.Name() == "acp_submit" {
				t.async = tool
				break
			}
		}
	}
	return t
}
func (t *ACPSendTool) Description() string {
	return "Send to external ACP. Prefer acp_submit with a configured peer for durable asynchronous work. Compatibility async mode accepts async:true,peer,session,content,idempotency_key and returns a handle immediately; agent_url/token belong only to legacy blocking mode."
}

func (t *ACPSendTool) Name() string { return "acp_send" }

type acpSendInput struct {
	Peer           string `json:"peer,omitempty"`
	Async          bool   `json:"async,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	AgentURL       string `json:"agent_url"`
	Token          string `json:"token"`
	Content        string `json:"content"`
	Session        string `json:"session,omitempty"`
}

func (t *ACPSendTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in acpSendInput
	if err := decodeACPObject(input, &in, map[string]bool{"peer": true, "async": true, "idempotency_key": true, "agent_url": true, "token": true, "content": true, "session": true}); err != nil {
		return nil, fmt.Errorf("acp_send: bad input: %w", err)
	}
	if in.Async || in.Peer != "" || in.IdempotencyKey != "" {
		if !in.Async || in.Peer == "" || in.AgentURL != "" || in.Token != "" || t.async == nil {
			return nil, fmt.Errorf("acp_send: durable mode requires async:true and a configured peer; legacy URL/token fields are forbidden")
		}
		raw, err := json.Marshal(map[string]string{"peer": in.Peer, "session": in.Session, "content": in.Content, "idempotency_key": in.IdempotencyKey})
		if err != nil {
			return nil, err
		}
		return t.async.Execute(ctx, raw)
	}
	if in.AgentURL == "" || in.Content == "" {
		return nil, fmt.Errorf("acp_send: agent_url and content required")
	}
	c := acp.NewClient(in.AgentURL, in.Token)
	// Agent jobs follow the caller's lifetime; the client's short POST has
	// its own request timeout, independently of a legitimately long stream.
	sseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := c.SendTo(sseCtx, in.Session, in.Content)
	if err != nil {
		return nil, fmt.Errorf("acp_send: %w", err)
	}
	var sb strings.Builder
	for ev := range ch {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("acp_send: %w", err)
		}
		switch ev.Type {
		case "text_delta":
			sb.WriteString(ev.Content)
		case "done":
			return json.Marshal(map[string]string{"reply": sb.String()})
		case "error":
			return nil, fmt.Errorf("acp_send: remote: %s", ev.Error)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("acp_send: %w", err)
	}
	return nil, fmt.Errorf("acp_send: stream closed without a done event")
}
