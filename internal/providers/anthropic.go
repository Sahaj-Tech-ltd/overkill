package providers

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`

	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`

	// Source is populated for image content blocks. Anthropic accepts
	// base64-encoded image bytes with an explicit media_type so the API
	// can decode without sniffing.
	Source *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`       // always "base64" for our upload path
	MediaType string `json:"media_type"` // e.g. "image/png"
	Data      string `json:"data"`       // base64-encoded image bytes
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	System    string             `json:"system,omitempty"`
	MaxTokens int                `json:"max_tokens"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
	Thinking  *anthropicThinking `json:"thinking,omitempty"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	Model      string                  `json:"model"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsageResp      `json:"usage"`
}

type anthropicUsageResp struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicStreamMessage struct {
	Type         string                  `json:"type"`
	Index        int                     `json:"index,omitempty"`
	Message      *anthropicStreamMsgWrap `json:"message,omitempty"`
	Delta        *anthropicStreamDelta   `json:"delta,omitempty"`
	Usage        *anthropicUsageResp     `json:"usage,omitempty"`
	ContentBlock *anthropicContentBlock  `json:"content_block,omitempty"`
}

type anthropicStreamMsgWrap struct {
	ID    string             `json:"id"`
	Model string             `json:"model"`
	Usage anthropicUsageResp `json:"usage"`
}

type anthropicStreamDelta struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
	// PartialJSON carries one fragment of a tool_use block's input. The
	// full JSON object is the concatenation of every input_json_delta
	// for that content_block_start, in order.
	PartialJSON string `json:"partial_json,omitempty"`
}

type AnthropicProvider struct {
	*BaseProvider
}

const (
	// DefaultAnthropicAPIVersion is the version string sent in the
	// anthropic-version header. Override via FactoryConfig.Headers
	// or by setting the ANTHROPIC_VERSION env var.
	DefaultAnthropicAPIVersion = "2023-06-01"
)

func NewAnthropicProvider(apiKey string, models []Model) *AnthropicProvider {
	bp := NewBaseProvider("anthropic", "https://api.anthropic.com/v1", apiKey, models)
	bp.headers["x-api-key"] = apiKey
	if v := os.Getenv("ANTHROPIC_VERSION"); v != "" {
		bp.headers["anthropic-version"] = v
	} else {
		bp.headers["anthropic-version"] = DefaultAnthropicAPIVersion
	}
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
		return nil, p.handleHTTPError(resp)
	}

	ch := make(chan Chunk, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		p.readSSEStream(ctx, resp.Body, ch)
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

	// Thinking support: wire the thinking level as an Anthropic
	// extended thinking block. off means no thinking block is sent.
	if req.ThinkingLevel != "" && req.ThinkingLevel != "off" {
		budget := thinkingBudgetTokens(req.ThinkingLevel)
		if budget > 0 {
			// Anthropic requires max_tokens > budget_tokens.
			// Ensure we don't exceed the model's context window.
			if body.MaxTokens <= budget {
				body.MaxTokens = budget + 1024
			}
			body.Thinking = &anthropicThinking{
				Type:         "enabled",
				BudgetTokens: budget,
			}
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

func (p *AnthropicProvider) readSSEStream(ctx context.Context, body io.Reader, ch chan<- Chunk) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var usage *Usage
	// Anthropic streams tool_use blocks as content_block_start (with id +
	// name + an empty input object) followed by N input_json_delta events
	// carrying `partial_json` fragments, then a content_block_stop. The
	// fragments are concatenated to form the full JSON arguments string —
	// they are NOT valid JSON on their own. Index identifies which block
	// each delta belongs to (text vs tool_use can interleave).
	type toolAccum struct {
		id   string
		name string
		args strings.Builder
	}
	toolBlocks := map[int]*toolAccum{}

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
		case "content_block_start":
			if msg.ContentBlock != nil && msg.ContentBlock.Type == "tool_use" {
				// Cap at maxParallelToolBlocks to defang an adversarial
				// stream that bursts 10^6 unique block indices.
				const maxParallelToolBlocks = 256
				if len(toolBlocks) >= maxParallelToolBlocks {
					continue
				}
				toolBlocks[msg.Index] = &toolAccum{
					id:   msg.ContentBlock.ID,
					name: msg.ContentBlock.Name,
				}
			}
		case "content_block_delta":
			if msg.Delta == nil {
				continue
			}
			switch msg.Delta.Type {
			case "text_delta":
				if msg.Delta.Text != "" {
					if !sendChunk(ctx, ch, Chunk{Content: msg.Delta.Text}) {
						return
					}
				}
			case "input_json_delta":
				if a, ok := toolBlocks[msg.Index]; ok {
					a.args.WriteString(msg.Delta.PartialJSON)
				}
			default:
				// Older event shape carried text without a delta.type — keep
				// the fallback so we don't silently drop content on a model
				// that doesn't tag deltas.
				if msg.Delta.Text != "" {
					if !sendChunk(ctx, ch, Chunk{Content: msg.Delta.Text}) {
						return
					}
				}
			}
		case "content_block_stop":
			if a, ok := toolBlocks[msg.Index]; ok {
				if a.id == "" {
					// Tool call block stop with no valid ID from
					// content_block_start — skip to avoid emitting
					// an unaddressable tool call that would cause
					// the next turn to fail with a 400 from Anthropic.
					delete(toolBlocks, msg.Index)
					continue
				}
				args := a.args.String()
				if args == "" {
					// Tools with no input still get a valid JSON object so
					// downstream JSON-unmarshal of arguments doesn't error.
					args = "{}"
				}
				if !sendChunk(ctx, ch, Chunk{ToolCalls: []ToolCall{{
					ID:        a.id,
					Name:      a.name,
					Arguments: args,
				}}}) {
					return
				}
				delete(toolBlocks, msg.Index)
			}
		case "message_stop":
			logSendChunk(ctx, ch, Chunk{Done: true, Usage: usage})
			return
		}
	}

	// Loop exited without seeing message_stop. Distinguish clean EOF from
	// transport failure — scanner.Err() returns non-nil when the underlying
	// reader errored mid-stream (TCP reset, proxy timeout, body close). Without
	// this check a dropped connection masquerades as a clean Done and the
	// caller commits a partial assistant message as if it were complete.
	if err := scanner.Err(); err != nil {
		logSendChunk(ctx, ch, Chunk{Err: fmt.Errorf("anthropic stream: %w", err)})
		return
	}
	// L18: clean EOF without the message_stop sentinel is an unexpected
	// truncation. Emit an error instead of a fake Done so the caller
	// doesn't commit a partial response as complete.
	logSendChunk(ctx, ch, Chunk{Err: fmt.Errorf("anthropic stream: unexpected EOF without message_stop")})
}

func anthropicMessages(msgs []Message) []anthropicMessage {
	result := make([]anthropicMessage, 0, len(msgs))

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			// Plain-text user message stays as a string Content for
			// backwards compatibility with the API's most common shape.
			// Once attachments enter the picture we must switch to the
			// content-blocks array — the API rejects mixing.
			if len(msg.Attachments) == 0 {
				result = append(result, anthropicMessage{
					Role:    "user",
					Content: msg.Content,
				})
				break
			}
			blocks := make([]anthropicContentBlock, 0, len(msg.Attachments)+1)
			// Attachments precede the text so the model has the visual
			// context loaded before reading the user's question — same
			// convention Anthropic's docs use in examples.
			for _, att := range msg.Attachments {
				if att.Kind != AttachmentImage {
					continue
				}
				blocks = append(blocks, anthropicContentBlock{
					Type: "image",
					Source: &anthropicImageSource{
						Type:      "base64",
						MediaType: att.MediaType,
						Data:      base64.StdEncoding.EncodeToString(att.Data),
					},
				})
			}
			if strings.TrimSpace(msg.Content) != "" {
				blocks = append(blocks, anthropicContentBlock{
					Type: "text",
					Text: msg.Content,
				})
			}
			result = append(result, anthropicMessage{
				Role:    "user",
				Content: blocks,
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

// thinkingBudgetTokens maps a thinking level string to an Anthropic
// budget_tokens value. Mirrors config.ThinkingLevel.BudgetTokens()
// without importing config to avoid import cycles.
func thinkingBudgetTokens(level string) int {
	switch level {
	case "minimal":
		return 1024
	case "low":
		return 2048
	case "medium":
		return 4096
	case "high":
		return 8192
	case "x-high":
		return 16384
	default:
		return 0
	}
}
