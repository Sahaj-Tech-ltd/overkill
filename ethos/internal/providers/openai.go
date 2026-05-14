package providers

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
)

type openaiMessage struct {
	Role string `json:"role"`
	// Content is either a string (plain text, the common case) or a
	// slice of openaiContentPart (when attachments are present). The
	// OpenAI Chat Completions API accepts both shapes; we keep the
	// string form for plain messages so request bodies stay small and
	// match the common examples in the API docs.
	Content    any              `json:"content"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

// openaiContentPart is one slot inside a multi-part content message.
// We use Type="text" with Text set for prose and Type="image_url" with
// ImageURL set for images. Anything else is rejected by the API.
type openaiContentPart struct {
	Type     string             `json:"type"`
	Text     string             `json:"text,omitempty"`
	ImageURL *openaiImageURLRef `json:"image_url,omitempty"`
}

type openaiImageURLRef struct {
	// URL is a data: URL like "data:image/png;base64,...". The API also
	// accepts https:// URLs but our pipeline only ever has in-memory
	// bytes (clipboard paste) so we always inline.
	URL string `json:"url"`
}

type openaiToolCall struct {
	// Index is set on streaming deltas to identify which parallel tool
	// call this fragment belongs to. Non-stream responses omit it.
	Index    *int               `json:"index,omitempty"`
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
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
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
	Role      string           `json:"role,omitempty"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
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
		// Response content always comes back as a string for our request
		// shape (we don't ask for tool-use blocks or images out). Defensive
		// assertion in case the API ever evolves — non-string falls back
		// to empty rather than panicking.
		if s, ok := choice.Message.Content.(string); ok {
			resp.Content = s
		}

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
	// OpenAI streams tool_calls as a sequence of deltas, each carrying
	// an `index` plus partial id / function.name / function.arguments
	// fragments. Arguments is a JSON string that is NOT valid mid-stream
	// — it must be concatenated until `finish_reason: "tool_calls"`. The
	// prior code emitted each fragment as a complete ToolCall, so the
	// consumer saw bursts of half-parsed args and bogus empty IDs.
	type accum struct {
		order int
		id    string
		name  string
		args  strings.Builder
	}
	toolAccum := map[int]*accum{}
	var orderCounter int
	flushTools := func() []ToolCall {
		if len(toolAccum) == 0 {
			return nil
		}
		// Emit in insertion order (matches the order OpenAI streamed
		// them, which preserves index → call alignment for the next
		// turn).
		indices := make([]int, 0, len(toolAccum))
		for i := range toolAccum {
			indices = append(indices, i)
		}
		sort.Slice(indices, func(a, b int) bool {
			return toolAccum[indices[a]].order < toolAccum[indices[b]].order
		})
		out := make([]ToolCall, 0, len(indices))
		for _, i := range indices {
			a := toolAccum[i]
			out = append(out, ToolCall{
				ID:        a.id,
				Name:      a.name,
				Arguments: a.args.String(),
			})
		}
		toolAccum = map[int]*accum{}
		return out
	}

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
			// Default index to 0 when the API omits it — the
			// non-parallel case streams a single tool with no index.
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			a, ok := toolAccum[idx]
			if !ok {
				a = &accum{order: orderCounter}
				orderCounter++
				toolAccum[idx] = a
			}
			if tc.ID != "" {
				a.id = tc.ID
			}
			if tc.Function.Name != "" {
				a.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				a.args.WriteString(tc.Function.Arguments)
			}
		}

		// On finish_reason transition, flush accumulated tool calls in
		// one Chunk so the consumer sees fully-formed JSON arguments.
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			if tools := flushTools(); len(tools) > 0 {
				ch <- Chunk{ToolCalls: tools}
			}
		}
	}
	// Belt + suspenders: some proxies drop finish_reason. Flush anything
	// still accumulated before signalling Done.
	if tools := flushTools(); len(tools) > 0 {
		ch <- Chunk{ToolCalls: tools}
	}

	// Transport-level error (TCP reset, proxy timeout, body close) leaves the
	// scanner in a non-nil err state. Surface it as a stream error instead of
	// faking a clean Done — the consumer must NOT commit partial content.
	if err := scanner.Err(); err != nil {
		ch <- Chunk{Err: fmt.Errorf("openai stream: %w", err)}
		return
	}
	ch <- Chunk{Done: true, Usage: usage}
}

func openAIMessages(msgs []Message) []openaiMessage {
	result := make([]openaiMessage, 0, len(msgs))

	for _, msg := range msgs {
		om := openaiMessage{
			Role:       msg.Role,
			ToolCallID: msg.ToolCallID,
		}

		// Content shape: string when no attachments, []openaiContentPart
		// when attachments are present. Mixing is not allowed by the API.
		if len(msg.Attachments) > 0 && msg.Role == "user" {
			parts := make([]openaiContentPart, 0, len(msg.Attachments)+1)
			for _, att := range msg.Attachments {
				if att.Kind != AttachmentImage {
					continue
				}
				// data: URL inlines the image so the API doesn't need to
				// fetch externally. base64 stdlib encoding matches what
				// the API expects (RFC 4648, no line breaks).
				dataURL := fmt.Sprintf("data:%s;base64,%s", att.MediaType, base64.StdEncoding.EncodeToString(att.Data))
				parts = append(parts, openaiContentPart{
					Type:     "image_url",
					ImageURL: &openaiImageURLRef{URL: dataURL},
				})
			}
			if strings.TrimSpace(msg.Content) != "" {
				parts = append(parts, openaiContentPart{
					Type: "text",
					Text: msg.Content,
				})
			}
			om.Content = parts
		} else {
			om.Content = msg.Content
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
