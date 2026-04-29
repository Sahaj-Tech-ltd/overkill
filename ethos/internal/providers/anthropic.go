package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

type anthropicContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`

	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Input    any    `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicRequest struct {
	Model     string          `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	System    string          `json:"system,omitempty"`
	MaxTokens int             `json:"max_tokens"`
	Tools     []anthropicTool `json:"tools,omitempty"`
	Stream    bool            `json:"stream,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	ID         string                   `json:"id"`
	Type       string                   `json:"type"`
	Role       string                   `json:"role"`
	Content    []anthropicContentBlock  `json:"content"`
	Model      string                   `json:"model"`
	StopReason string                   `json:"stop_reason"`
	Usage      anthropicUsageResp       `json:"usage"`
}

type anthropicUsageResp struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicStreamMessage struct {
	Type    string                   `json:"type"`
	Index   int                      `json:"index,omitempty"`
	Message *anthropicStreamMsgWrap  `json:"message,omitempty"`
	Delta   *anthropicStreamDelta    `json:"delta,omitempty"`
	Usage   *anthropicUsageResp      `json:"usage,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
}

type anthropicStreamMsgWrap struct {
	ID      string              `json:"id"`
	Model   string              `json:"model"`
	Usage   anthropicUsageResp  `json:"usage"`
}

type anthropicStreamDelta struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

type AnthropicProvider struct {
	*BaseProvider
}

func NewAnthropicProvider(apiKey string, models []Model) *AnthropicProvider {
	bp := NewBaseProvider("anthropic", "https://api.anthropic.com/v1", apiKey, models)
	bp.headers["x-api-key"] = apiKey
	bp.headers["anthropic-version"] = "2023-06-01"
	return &AnthropicProvider{BaseProvider: bp}
}

func (p *AnthropicProvider) Complete(ctx context.Context, req Request) (Response, error) {
	body := p.buildRequestBody(req, false)

	resp, err := p.doRequest(ctx, http.MethodPost, "/messages", body)
	if err != nil {
		return Response{}, fmt.Errorf("providers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Response{}, p.handleHTTPError(resp)
	}

	var result anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Response{}, fmt.Errorf("providers: decode anthropic response: %w", err)
	}

	return p.parseResponse(&result), nil
}

func (p *AnthropicProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	body := p.buildRequestBody(req, true)

	resp, err := p.doRequest(ctx, http.MethodPost, "/messages", body)
	if err != nil {
		return nil, fmt.Errorf("providers: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, p.handleHTTPError(resp)
	}

	ch := make(chan Chunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		p.readSSEStream(resp.Body, ch)
	}()

	return ch, nil
}

func (p *AnthropicProvider) buildRequestBody(req Request, stream bool) anthropicRequest {
	body := anthropicRequest{
		Model:     req.Model,
		Messages:  anthropicMessages(req.Messages),
		MaxTokens: req.MaxTokens,
		Stream:    stream,
	}

	if req.MaxTokens == 0 {
		body.MaxTokens = 4096
	}

	if req.SystemPrompt != "" {
		body.System = req.SystemPrompt
	}

	if len(req.Tools) > 0 {
		body.Tools = make([]anthropicTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			body.Tools = append(body.Tools, anthropicTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.Parameters,
			})
		}
	}

	return body
}

func (p *AnthropicProvider) parseResponse(result *anthropicResponse) Response {
	resp := Response{
		ID:    result.ID,
		Model: result.Model,
		Usage: Usage{
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
		},
	}

	for _, block := range result.Content {
		switch block.Type {
		case "text":
			if resp.Content != "" {
				resp.Content += "\n"
			}
			resp.Content += block.Text
		case "tool_use":
			args, _ := json.Marshal(block.Input)
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(args),
			})
		}
	}

	return resp
}

func (p *AnthropicProvider) readSSEStream(body io.Reader, ch chan<- Chunk) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var usage *Usage

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			_ = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		var msg anthropicStreamMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Warn().Err(err).Str("data", data).Msg("providers: failed to parse anthropic stream event")
			continue
		}

		switch msg.Type {
		case "message_delta":
			if msg.Usage != nil {
				usage = &Usage{
					OutputTokens: msg.Usage.OutputTokens,
				}
			}
		case "content_block_delta":
			if msg.Delta != nil && msg.Delta.Text != "" {
				ch <- Chunk{Content: msg.Delta.Text}
			}
		case "message_stop":
			ch <- Chunk{Done: true, Usage: usage}
			return
		}
	}

	ch <- Chunk{Done: true, Usage: usage}
}

func anthropicMessages(msgs []Message) []anthropicMessage {
	result := make([]anthropicMessage, 0, len(msgs))

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			result = append(result, anthropicMessage{
				Role:    "user",
				Content: msg.Content,
			})
		case "assistant":
			if len(msg.ToolCalls) == 0 {
				result = append(result, anthropicMessage{
					Role:    "assistant",
					Content: msg.Content,
				})
			} else {
				blocks := make([]anthropicContentBlock, 0)
				if msg.Content != "" {
					blocks = append(blocks, anthropicContentBlock{
						Type: "text",
						Text: msg.Content,
					})
				}
				for _, tc := range msg.ToolCalls {
					var input any
					if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
						input = map[string]any{}
					}
					blocks = append(blocks, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Name,
						Input: input,
					})
				}
				result = append(result, anthropicMessage{
					Role:    "assistant",
					Content: blocks,
				})
			}
		case "tool":
			result = append(result, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: msg.ToolCallID,
						Content:   msg.Content,
					},
				},
			})
		}
	}

	return result
}
