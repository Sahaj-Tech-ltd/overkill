package providers

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

type geminiPart struct {
	Text string `json:"text,omitempty"`
	// InlineData carries non-text payloads (image bytes today). Gemini
	// requires base64 with an explicit mime_type; data: URLs aren't
	// accepted here, only the raw base64 string.
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
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
	path := fmt.Sprintf("/models/%s:generateContent?key=%s", req.Model, p.apiKey)
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
	path := fmt.Sprintf("/models/%s:streamGenerateContent?key=%s&alt=sse", req.Model, p.apiKey)
	body := p.buildRequestBody(req)

	resp, err := p.doRequest(ctx, http.MethodPost, path, body)
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

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		resp.Content = result.Candidates[0].Content.Parts[0].Text
	}

	return resp
}

func (p *GeminiProvider) readSSEStream(body io.Reader, ch chan<- Chunk) {
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

		if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 {
			text := chunk.Candidates[0].Content.Parts[0].Text
			if text != "" {
				ch <- Chunk{Content: text}
			}
		}
	}

	// Surface mid-stream transport failures rather than swallowing them as a
	// clean Done. See the matching note in the Anthropic and OpenAI providers.
	if err := scanner.Err(); err != nil {
		ch <- Chunk{Err: fmt.Errorf("gemini stream: %w", err)}
		return
	}
	ch <- Chunk{Done: true, Usage: usage}
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
