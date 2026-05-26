package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
)

// ACPSendTool dispatches a sub-task to another ACP agent on the network.
type ACPSendTool struct{}

func NewACPSendTool() *ACPSendTool { return &ACPSendTool{} }

func (t *ACPSendTool) Name() string { return "acp_send" }

type acpSendInput struct {
	AgentURL string `json:"agent_url"`
	Token    string `json:"token"`
	Content  string `json:"content"`
	Session  string `json:"session,omitempty"`
}

func (t *ACPSendTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in acpSendInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("acp_send: bad input: %w", err)
	}
	if in.AgentURL == "" || in.Content == "" {
		return nil, fmt.Errorf("acp_send: agent_url and content required")
	}
	c := acp.NewClient(in.AgentURL, in.Token)
	ch, err := c.Send(ctx, in.Content)
	if err != nil {
		return nil, fmt.Errorf("acp_send: %w", err)
	}
	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case "text_delta":
			sb.WriteString(ev.Content)
		case "error":
			return nil, fmt.Errorf("acp_send: remote: %s", ev.Error)
		}
	}
	return json.Marshal(map[string]string{"reply": sb.String()})
}
