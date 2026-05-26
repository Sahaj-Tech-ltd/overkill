package providers

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/rs/zerolog/log"
)

type geminiPart struct {
	Text string `json:"text,omitempty"`
	// InlineData carries non-text payloads (image bytes today). Gemini
	// requires base64 with an explicit mime_type; data: URLs aren't
	// accepted here, only the raw base64 string.
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
	// FunctionCall is set on assistant parts when the model wants to
	// invoke a tool. Args is a JSON object (already parsed by the API,
	// not a string fragment like OpenAI/Anthropic streaming).
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"` // e.g. "image/png"
	Data     string `json:"data"`     // base64-encoded bytes, no prefix
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	Contents          []geminiContent  `json:"contents"`
	SystemInstruction *geminiContent   `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDecl `json:"tools,omitempty"`
	GenerationConfig  *geminiGenConfig `json:"generationConfig,omitempty"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFuncDecl `json:"function_declarations,omitempty"`
}

type geminiFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiGenConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	UsageMeta  geminiUsageMeta   `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiUsageMeta struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

type geminiStreamResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	UsageMeta  *geminiUsageMeta  `json:"usageMetadata,omitempty"`
}

type GeminiProvider struct {
	*BaseProvider
}

func NewGeminiProvider(apiKey string, models []Model) *GeminiProvider {
	return &GeminiProvider{
		BaseProvider: NewBaseProvider("gemini", "https://generativelanguage.googleapis.com/v1beta", apiKey, models),
	}
}

func (p *GeminiProvider) Complete(ctx context.Context, req Request) (Response, error) {
	// Escape both segments: a model name with `?`, `#`, or `&` would
	// inject query params or truncate the path; an API key with any
	// reserved URL character would silently corrupt auth.
	path := "/models/" + url.PathEscape(req.Model) + ":generateContent?key=" + url.QueryEscape(p.apiKey)
	body := p.buildRequestBody(req)

	resp, err := p.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return Response{}, fmt.Errorf("providers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Response{}, p.handleHTTPError(resp)
	}

	var result geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Response{}, fmt.Errorf("providers: decode gemini response: %w", err)
	}

	return p.parseResponse(&result), nil
}

func (p *GeminiProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	path := "/models/" + url.PathEscape(req.Model) + ":streamGenerateContent?key=" + url.QueryEscape(p.apiKey) + "&alt=sse"
	body := p.buildRequestBody(req)

	resp, err := p.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("providers: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
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

func (p *GeminiProvider) buildRequestBody(req Request) geminiRequest {
	body := geminiRequest{
		Contents: geminiMessages(req.Messages),
	}

	if req.SystemPrompt != "" {
		body.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.SystemPrompt}},
		}
	}

	if len(req.Tools) > 0 {
		decls := make([]geminiFuncDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, geminiFuncDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
		body.Tools = []geminiToolDecl{{FunctionDeclarations: decls}}
	}

	if req.Temperature > 0 || req.MaxTokens > 0 {
		body.GenerationConfig = &geminiGenConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxTokens,
		}
	}

	return body
}

func (p *GeminiProvider) parseResponse(result *geminiResponse) Response {
	resp := Response{
		Usage: Usage{
			InputTokens:  result.UsageMeta.PromptTokenCount,
			OutputTokens: result.UsageMeta.CandidatesTokenCount,
		},
	}

	if len(result.Candidates) > 0 {
		// Gemini returns the assistant turn as a sequence of parts in one
		// candidate. Parts can be plain text OR functionCall — concatenate
		// the text and lift every functionCall into ToolCalls so write-path
		// callers see a complete, ordered turn. The old code grabbed only
		// parts[0].Text and silently dropped both extra text parts and
		// every tool call.
		var sb strings.Builder
		for _, part := range result.Candidates[0].Content.Parts {
			if part.Text != "" {
				sb.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				args := part.FunctionCall.Args
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				resp.ToolCalls = append(resp.ToolCalls, ToolCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(args),
				})
			}
		}
		resp.Content = sb.String()
	}

	return resp
}

func (p *GeminiProvider) readSSEStream(ctx context.Context, body io.Reader, ch chan<- Chunk) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var usage *Usage

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		var chunk geminiStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Warn().Err(err).Str("data", data).Msg("providers: failed to parse gemini stream chunk")
			continue
		}

		if chunk.UsageMeta != nil {
			usage = &Usage{
				InputTokens:  chunk.UsageMeta.PromptTokenCount,
				OutputTokens: chunk.UsageMeta.CandidatesTokenCount,
			}
		}

		if len(chunk.Candidates) > 0 {
			// Walk every part: emit text fragments as Content chunks and
			// lift any functionCall into a ToolCalls chunk. Gemini does
			// NOT fragment functionCall.args across multiple events —
			// when a part has a functionCall it carries the complete
			// args object, so we can emit immediately without a buffer.
			for _, part := range chunk.Candidates[0].Content.Parts {
				if part.Text != "" {
					if !sendChunk(ctx, ch, Chunk{Content: part.Text}) {
						return
					}
				}
				if part.FunctionCall != nil {
					args := part.FunctionCall.Args
					if len(args) == 0 {
						args = json.RawMessage("{}")
					}
					if !sendChunk(ctx, ch, Chunk{ToolCalls: []ToolCall{{
						Name:      part.FunctionCall.Name,
						Arguments: string(args),
					}}}) {
						return
					}
				}
			}
		}
	}

	// Surface mid-stream transport failures rather than swallowing them as a
	// clean Done. See the matching note in the Anthropic and OpenAI providers.
	if err := scanner.Err(); err != nil {
		_ = sendChunk(ctx, ch, Chunk{Err: fmt.Errorf("gemini stream: %w", err)})
		return
	}
	_ = sendChunk(ctx, ch, Chunk{Done: true, Usage: usage})
}

func geminiMessages(msgs []Message) []geminiContent {
	result := make([]geminiContent, 0, len(msgs))

	for _, msg := range msgs {
		role := msg.Role
		switch role {
		case "assistant":
			role = "model"
		case "tool":
			role = "user"
		}

		var parts []geminiPart
		// Image parts precede text so the model has visual context
		// loaded before reading the prompt — matches Gemini's example
		// ordering in their multimodal docs.
		if msg.Role == "user" {
			for _, att := range msg.Attachments {
				if att.Kind != AttachmentImage {
					continue
				}
				parts = append(parts, geminiPart{
					InlineData: &geminiInlineData{
						MimeType: att.MediaType,
						Data:     base64.StdEncoding.EncodeToString(att.Data),
					},
				})
			}
		}
		if msg.Role == "tool" && msg.ToolCallID != "" {
			parts = append(parts, geminiPart{Text: fmt.Sprintf("Tool result for %s:\n%s", msg.ToolCallID, msg.Content)})
		} else if msg.Content != "" || len(parts) == 0 {
			// Always include a text part when there's text OR when we
			// have no other parts (gemini rejects empty parts arrays).
			parts = append(parts, geminiPart{Text: msg.Content})
		}

		result = append(result, geminiContent{
			Role:  role,
			Parts: parts,
		})
	}

	return result
}
