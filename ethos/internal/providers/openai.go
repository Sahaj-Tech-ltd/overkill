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

type openaiMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []openaiToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type openaiToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function openaiFunctionCall `json:"function"`
}

type openaiFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiToolFunc `json:"function"`
}

type openaiToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Tools       []openaiTool    `json:"tools,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openaiResponse struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Choices []openaiChoice  `json:"choices"`
	Usage   openaiUsageResp `json:"usage"`
}

type openaiChoice struct {
	Index        int            `json:"index"`
	Message      openaiMessage  `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

type openaiStreamChunk struct {
	ID      string               `json:"id"`
	Choices []openaiStreamChoice `json:"choices"`
	Usage   *openaiUsageResp     `json:"usage,omitempty"`
}

type openaiStreamChoice struct {
	Index        int               `json:"index"`
	Delta        openaiStreamDelta `json:"delta"`
	FinishReason *string           `json:"finish_reason"`
}

type openaiStreamDelta struct {
	Role      string            `json:"role,omitempty"`
	Content   string            `json:"content,omitempty"`
	ToolCalls []openaiToolCall  `json:"tool_calls,omitempty"`
}

type openaiUsageResp struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type OpenAIProvider struct {
	*BaseProvider
}

func NewOpenAIProvider(apiKey string, models []Model) *OpenAIProvider {
	bp := NewBaseProvider("openai", "https://api.openai.com/v1", apiKey, models)
	bp.headers["Authorization"] = "Bearer " + apiKey
	return &OpenAIProvider{BaseProvider: bp}
}

func (p *OpenAIProvider) Complete(ctx context.Context, req Request) (Response, error) {
	body := p.buildRequestBody(req, false)

	resp, err := p.doRequest(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return Response{}, fmt.Errorf("providers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Response{}, p.handleHTTPError(resp)
	}

	var result openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Response{}, fmt.Errorf("providers: decode openai response: %w", err)
	}

	return p.parseResponse(&result), nil
}

func (p *OpenAIProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	body := p.buildRequestBody(req, true)

	resp, err := p.doRequest(ctx, http.MethodPost, "/chat/completions", body)
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

func (p *OpenAIProvider) buildRequestBody(req Request, stream bool) openaiRequest {
	msgs := openAIMessages(req.Messages)

	if req.SystemPrompt != "" {
		msgs = append([]openaiMessage{{Role: "system", Content: req.SystemPrompt}}, msgs...)
	}

	body := openaiRequest{
		Model:       req.Model,
		Messages:    msgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      stream,
	}

	if len(req.Tools) > 0 {
		body.Tools = openAITools(req.Tools)
	}

	return body
}

func (p *OpenAIProvider) parseResponse(result *openaiResponse) Response {
	resp := Response{
		ID:    result.ID,
		Model: result.Model,
		Usage: Usage{
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
		},
	}

	if len(result.Choices) > 0 {
		choice := result.Choices[0]
		resp.Content = choice.Message.Content

		for _, tc := range choice.Message.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	return resp
}

func (p *OpenAIProvider) readSSEStream(body io.Reader, ch chan<- Chunk) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var usage *Usage
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Warn().Err(err).Str("data", data).Msg("providers: failed to parse openai stream chunk")
			continue
		}

		if chunk.Usage != nil {
			usage = &Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			ch <- Chunk{Content: choice.Delta.Content}
		}

		for _, tc := range choice.Delta.ToolCalls {
			ch <- Chunk{
				ToolCalls: []ToolCall{{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}},
			}
		}
	}

	ch <- Chunk{Done: true, Usage: usage}
}

func openAIMessages(msgs []Message) []openaiMessage {
	result := make([]openaiMessage, 0, len(msgs))

	for _, msg := range msgs {
		om := openaiMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}

		for _, tc := range msg.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, openaiToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: openaiFunctionCall{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}

		result = append(result, om)
	}

	return result
}

func openAITools(tools []Tool) []openaiTool {
	result := make([]openaiTool, 0, len(tools))
	for _, t := range tools {
		result = append(result, openaiTool{
			Type: "function",
			Function: openaiToolFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return result
}
