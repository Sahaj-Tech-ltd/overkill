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
	Model           string          `json:"model"`
	Messages        []openaiMessage `json:"messages"`
	Tools           []openaiTool    `json:"tools,omitempty"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	Temperature     float64         `json:"temperature,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Thinking        *openaiThinking `json:"thinking,omitempty"`
}

type openaiThinking struct {
	Type string `json:"type"`
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

	parsed, err := p.parseResponse(&result)
	return parsed, err
}

func (p *OpenAIProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	body := p.buildRequestBody(req, true)

	resp, err := p.doRequest(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return nil, fmt.Errorf("providers: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, p.handleHTTPError(resp)
	}

	// Buffered + ctx-aware send. Unbuffered channels here leaked a
	// goroutine + HTTP body whenever the consumer cancelled
	// mid-stream: readSSEStream blocked forever on `ch <- chunk`.
	ch := make(chan Chunk, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		p.readSSEStream(ctx, resp.Body, ch)
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

	// Thinking support: map thinking level to provider-specific parameters.
	// OpenAI o-series uses reasoning_effort; DeepSeek R1 uses thinking.type.
	if req.ThinkingLevel != "" && req.ThinkingLevel != "off" {
		// For DeepSeek models (deepseek-reasoner), use thinking {type: enabled}.
		if strings.HasPrefix(req.Model, "deepseek-reasoner") || strings.HasPrefix(req.Model, "deepseek-r1") {
			body.Thinking = &openaiThinking{Type: "enabled"}
		} else {
			// For OpenAI o-series and other reasoning models, use reasoning_effort.
			// Map thinking levels to reasoning_effort values.
			body.ReasoningEffort = openAIReasoningEffort(req.ThinkingLevel)
		}
	}

	return body
}

func (p *OpenAIProvider) parseResponse(result *openaiResponse) (Response, error) {
	resp := Response{
		ID:    result.ID,
		Model: result.Model,
		Usage: Usage{
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
		},
	}

	if len(result.Choices) == 0 {
		return Response{}, fmt.Errorf("providers: openai returned empty choices (no model response)")
	}

	choice := result.Choices[0]
	// Response content always comes back as a string for our request
	// shape (we don't ask for tool-use blocks or images out). Defensive
	// assertion in case the API ever evolves — non-string falls back
	// to empty rather than panicking.
	if s, ok := choice.Message.Content.(string); ok {
		resp.Content = s
	} else if choice.FinishReason != "stop" {
		return Response{}, fmt.Errorf("providers: openai response content is not a string (type: %T, finish_reason: %s)", choice.Message.Content, choice.FinishReason)
	}

	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return resp, nil
}

// send delivers a chunk while respecting ctx cancellation so a stalled
// consumer can't pin this goroutine forever. Returns false when ctx is
// done — callers must exit the loop.
func sendChunk(ctx context.Context, ch chan<- Chunk, c Chunk) bool {
	select {
	case ch <- c:
		return true
	case <-ctx.Done():
		return false
	}
}

// logSendChunk wraps sendChunk and logs a warning when a signal chunk
// (Done or Err) cannot be delivered because the consumer context was
// cancelled. Non-signal content chunks are silently dropped.
func logSendChunk(ctx context.Context, ch chan<- Chunk, c Chunk) {
	if !sendChunk(ctx, ch, c) && (c.Done || c.Err != nil) {
		log.Warn().Msg("[provider] failed to deliver stream signal chunk (ctx cancelled)")
	}
}

func (p *OpenAIProvider) readSSEStream(ctx context.Context, body io.Reader, ch chan<- Chunk) {
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

	// SSE per WHATWG/RFC: an event is terminated by a blank line, and
	// a single event can span multiple `data:` lines that join with
	// '\n'. Old parser dispatched on every `data:` line — a proxy
	// that wraps a large chunk across two `data:` lines would have
	// produced two JSON-parse failures and silent content loss.
	var dataBuf strings.Builder
	// Returns false when caller should stop reading (e.g. [DONE]).
	dispatch := func(payload string) bool {
		if payload == "" {
			return true
		}
		if payload == "[DONE]" {
			return false
		}
		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			log.Warn().Err(err).Str("data", payload).Msg("providers: failed to parse openai stream chunk")
			return true
		}
		if chunk.Usage != nil {
			usage = &Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			}
		}
		if len(chunk.Choices) == 0 {
			return true
		}
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			if !sendChunk(ctx, ch, Chunk{Content: choice.Delta.Content}) {
				return false
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			a, ok := toolAccum[idx]
			if !ok {
				const maxParallelToolCalls = 256
				if len(toolAccum) >= maxParallelToolCalls {
					continue
				}
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
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			if tools := flushTools(); len(tools) > 0 {
				if !sendChunk(ctx, ch, Chunk{ToolCalls: tools}) {
					return false
				}
			}
		}
		return true
	}

	for scanner.Scan() {
		line := scanner.Text()

		// Blank line = event boundary. Dispatch what we accumulated.
		if line == "" {
			payload := dataBuf.String()
			dataBuf.Reset()
			if !dispatch(payload) {
				break
			}
			continue
		}

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		// Per spec: a single leading space after `data:` is stripped;
		// other whitespace is preserved.
		seg := strings.TrimPrefix(line, "data:")
		if strings.HasPrefix(seg, " ") {
			seg = seg[1:]
		}
		if dataBuf.Len() > 0 {
			dataBuf.WriteByte('\n')
		}
		dataBuf.WriteString(seg)
	}
	// Last event (no trailing blank line on close).
	if pending := dataBuf.String(); pending != "" {
		if strings.TrimSpace(pending) == "[DONE]" {
			return
		}
		if !sendChunk(ctx, ch, Chunk{Content: pending}) {
			return
		}
	}

	// Belt + suspenders: some proxies drop finish_reason. Flush anything
	// still accumulated before signalling Done.
	if tools := flushTools(); len(tools) > 0 {
		if !sendChunk(ctx, ch, Chunk{ToolCalls: tools}) {
			return
		}
	}

	// Transport-level error (TCP reset, proxy timeout, body close) leaves the
	// scanner in a non-nil err state. Surface it as a stream error instead of
	// faking a clean Done — the consumer must NOT commit partial content.
	if err := scanner.Err(); err != nil {
		logSendChunk(ctx, ch, Chunk{Err: fmt.Errorf("openai stream: %w", err)})
		return
	}
	logSendChunk(ctx, ch, Chunk{Done: true, Usage: usage})
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

// openAIReasoningEffort maps a thinking level string to an OpenAI
// reasoning_effort value. For use with o-series and other reasoning models.
func openAIReasoningEffort(level string) string {
	switch level {
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "x-high":
		return "high"
	default:
		return ""
	}
}
