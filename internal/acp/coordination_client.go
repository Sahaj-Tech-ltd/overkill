package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"io"
	"net/http"
	"strings"
)

// Coordination uses a scoped peer credential, distinct from a global ACP token.
// Endpoint is explicit application configuration, never model input/discovery.
// Redirects are refused, including same-host redirects, before credential reuse.
func (c *Client) Coordination(ctx context.Context, operation string, in CoordinationRequest) (CoordinationResponse, error) {
	var out CoordinationResponse
	switch operation {
	case "peers", "send", "reply", "inbox", "claim", "status", "await", "cancel", "transition", "recover":
	default:
		return out, fmt.Errorf("invalid coordination operation")
	}
	ctx, cancel := requestContext(ctx)
	defer cancel()
	data, err := json.Marshal(in)
	if err != nil {
		return out, fmt.Errorf("invalid coordination body")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/v1/coordination/"+operation, bytes.NewReader(data))
	if err != nil {
		return out, fmt.Errorf("invalid coordination endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	client, err := coordination.NewPeerHTTPClient(c.BaseURL)
	if err != nil {
		return out, fmt.Errorf("invalid protected coordination endpoint")
	}
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("coordination transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("coordination rejected: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return out, fmt.Errorf("invalid coordination response")
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("invalid coordination response")
	}
	return out, nil
}
