package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSSE(w http.ResponseWriter, data string) {
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeSSEEvent(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func collectChunks(ch <-chan Chunk) ([]Chunk, string) {
	var chunks []Chunk
	var content string
	for c := range ch {
		chunks = append(chunks, c)
		if !c.Done {
			content += c.Content
		}
	}
	return chunks, content
}

func TestOpenAI_Complete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		assert.Equal(t, "ethos/0.1.0", r.Header.Get("User-Agent"))

		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		require.NoError(t, err)
		assert.Equal(t, "gpt-4o", body["model"])

		msgs := body["messages"].([]any)
		assert.Len(t, msgs, 1)
		firstMsg := msgs[0].(map[string]any)
		assert.Equal(t, "user", firstMsg["role"])
		assert.Equal(t, "Hello", firstMsg["content"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-test123",
			"object": "chat.completion",
			"model":  "gpt-4o",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Hello! How can I help?",
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     20,
				"completion_tokens": 8,
				"total_tokens":      28,
			},
		})
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", nil)
	p.baseURL = server.URL

	resp, err := p.Complete(context.Background(), Request{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "chatcmpl-test123", resp.ID)
	assert.Equal(t, "gpt-4o", resp.Model)
	assert.Equal(t, "Hello! How can I help?", resp.Content)
	assert.Equal(t, 20, resp.Usage.InputTokens)
	assert.Equal(t, 8, resp.Usage.OutputTokens)
}

func TestOpenAI_Complete_WithTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		tools, hasTools := body["tools"]
		require.True(t, hasTools, "expected tools in request body")
		toolsArr := tools.([]any)
		assert.Len(t, toolsArr, 1)
		tool := toolsArr[0].(map[string]any)
		assert.Equal(t, "function", tool["type"])
		fn := tool["function"].(map[string]any)
		assert.Equal(t, "search", fn["name"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-tools123",
			"model": "gpt-4o",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{{
						"id":   "call_abc123",
						"type": "function",
						"function": map[string]any{
							"name":      "search",
							"arguments": `{"query":"test"}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{
				"prompt_tokens":     50,
				"completion_tokens": 20,
				"total_tokens":      70,
			},
		})
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", nil)
	p.baseURL = server.URL

	resp, err := p.Complete(context.Background(), Request{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: "user", Content: "Search for test"},
		},
		Tools: []Tool{
			{Name: "search", Description: "Search the web", Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "chatcmpl-tools123", resp.ID)
	assert.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "call_abc123", resp.ToolCalls[0].ID)
	assert.Equal(t, "search", resp.ToolCalls[0].Name)
	assert.Equal(t, `{"query":"test"}`, resp.ToolCalls[0].Arguments)
	assert.Equal(t, 50, resp.Usage.InputTokens)
	assert.Equal(t, 20, resp.Usage.OutputTokens)
}

func TestOpenAI_Complete_WithSystemPrompt(t *testing.T) {
	var receivedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-sys",
			"model": "gpt-4o",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "response",
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 5},
		})
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", nil)
	p.baseURL = server.URL

	_, err := p.Complete(context.Background(), Request{
		Model:        "gpt-4o",
		SystemPrompt: "You are helpful.",
		Messages:     []Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)

	msgs := receivedBody["messages"].([]any)
	first := msgs[0].(map[string]any)
	assert.Equal(t, "system", first["role"])
	assert.Equal(t, "You are helpful.", first["content"])
}

func TestOpenAI_Complete_NoToolsOmitted(t *testing.T) {
	var receivedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-notools",
			"model": "gpt-4o",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "ok",
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 2},
		})
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", nil)
	p.baseURL = server.URL

	_, err := p.Complete(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)

	_, hasTools := receivedBody["tools"]
	assert.False(t, hasTools, "tools field should be omitted when no tools provided")
}

func TestOpenAI_Stream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, true, body["stream"])

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		writeSSE(w, `{"id":"chatcmpl-stream","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"chatcmpl-stream","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"chatcmpl-stream","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"chatcmpl-stream","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", nil)
	p.baseURL = server.URL

	ch, err := p.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)

	chunks, content := collectChunks(ch)
	assert.Equal(t, "Hello world", content)
	assert.True(t, len(chunks) > 0)

	var doneChunk Chunk
	for _, c := range chunks {
		if c.Done {
			doneChunk = c
		}
	}
	assert.True(t, doneChunk.Done, "should have a done chunk")
	if doneChunk.Usage != nil {
		assert.Equal(t, 20, doneChunk.Usage.InputTokens)
		assert.Equal(t, 5, doneChunk.Usage.OutputTokens)
	}
}

func TestOpenAI_Error_401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Invalid API key",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer server.Close()

	p := NewOpenAIProvider("bad-key", nil)
	p.baseURL = server.URL

	_, err := p.Complete(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})

	require.Error(t, err)
	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 401, httpErr.StatusCode)
	assert.Contains(t, httpErr.Body, "Invalid API key")
	assert.False(t, httpErr.IsRetryable())
}

func TestOpenAI_Error_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Rate limit exceeded",
				"type":    "rate_limit_error",
			},
		})
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", nil)
	p.baseURL = server.URL

	_, err := p.Complete(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})

	require.Error(t, err)
	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 429, httpErr.StatusCode)
	assert.True(t, httpErr.IsRetryable())
}

func TestAnthropic_Complete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/messages", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		assert.Equal(t, "ethos/0.1.0", r.Header.Get("User-Agent"))

		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		require.NoError(t, err)
		assert.Equal(t, "claude-sonnet-4-20250514", body["model"])
		assert.Equal(t, "You are helpful.", body["system"])
		assert.Equal(t, float64(4096), body["max_tokens"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_test123",
			"type":        "message",
			"role":        "assistant",
			"content":     []map[string]any{{"type": "text", "text": "Hello! How can I help?"}},
			"model":       "claude-sonnet-4-20250514",
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 20, "output_tokens": 8},
		})
	}))
	defer server.Close()

	p := NewAnthropicProvider("test-key", nil)
	p.baseURL = server.URL

	resp, err := p.Complete(context.Background(), Request{
		Model:        "claude-sonnet-4-20250514",
		MaxTokens:    4096,
		SystemPrompt: "You are helpful.",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "msg_test123", resp.ID)
	assert.Equal(t, "claude-sonnet-4-20250514", resp.Model)
	assert.Equal(t, "Hello! How can I help?", resp.Content)
	assert.Equal(t, 20, resp.Usage.InputTokens)
	assert.Equal(t, 8, resp.Usage.OutputTokens)
}

func TestAnthropic_Complete_ToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		_, hasTools := body["tools"]
		assert.True(t, hasTools, "expected tools in request body")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":   "msg_tools123",
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "Let me search for that."},
				{"type": "tool_use", "id": "toolu_abc123", "name": "search", "input": map[string]any{"query": "test"}},
			},
			"model":       "claude-sonnet-4-20250514",
			"stop_reason": "tool_use",
			"usage":       map[string]any{"input_tokens": 50, "output_tokens": 20},
		})
	}))
	defer server.Close()

	p := NewAnthropicProvider("test-key", nil)
	p.baseURL = server.URL

	resp, err := p.Complete(context.Background(), Request{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 4096,
		Messages: []Message{
			{Role: "user", Content: "Search for test"},
		},
		Tools: []Tool{
			{Name: "search", Description: "Search", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "msg_tools123", resp.ID)
	assert.Equal(t, "Let me search for that.", resp.Content)
	assert.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "toolu_abc123", resp.ToolCalls[0].ID)
	assert.Equal(t, "search", resp.ToolCalls[0].Name)
	assert.Equal(t, `{"query":"test"}`, resp.ToolCalls[0].Arguments)
}

func TestAnthropic_Stream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/messages", r.URL.Path)

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, true, body["stream"])

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		writeSSEEvent(w, "message_start", `{"type":"message_start","message":{"id":"msg_stream","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-20250514","usage":{"input_tokens":20,"output_tokens":0}}}`)
		writeSSEEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		writeSSEEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
		writeSSEEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"!"}}`)
		writeSSEEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeSSEEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`)
		writeSSEEvent(w, "message_stop", `{"type":"message_stop"}`)
	}))
	defer server.Close()

	p := NewAnthropicProvider("test-key", nil)
	p.baseURL = server.URL

	ch, err := p.Stream(context.Background(), Request{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 4096,
		Messages:  []Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)

	chunks, content := collectChunks(ch)
	assert.Equal(t, "Hello!", content)
	assert.True(t, len(chunks) > 0)

	var doneChunk Chunk
	for _, c := range chunks {
		if c.Done {
			doneChunk = c
		}
	}
	assert.True(t, doneChunk.Done)
	if doneChunk.Usage != nil {
		assert.Equal(t, 2, doneChunk.Usage.OutputTokens)
	}
}

func TestGemini_Complete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "models/gemini-2.5-flash:generateContent")
		assert.Equal(t, "test-key", r.URL.Query().Get("key"))
		assert.Equal(t, "ethos/0.1.0", r.Header.Get("User-Agent"))

		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		require.NoError(t, err)

		contents := body["contents"].([]any)
		assert.Len(t, contents, 1)
		first := contents[0].(map[string]any)
		assert.Equal(t, "user", first["role"])

		sysInstr := body["systemInstruction"].(map[string]any)
		sysParts := sysInstr["parts"].([]any)
		sysText := sysParts[0].(map[string]any)
		assert.Equal(t, "You are helpful.", sysText["text"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{"text": "Hello! How can I help?"}},
					"role":  "model",
				},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":     20,
				"candidatesTokenCount": 8,
				"totalTokenCount":      28,
			},
		})
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", nil)
	p.baseURL = server.URL

	resp, err := p.Complete(context.Background(), Request{
		Model:        "gemini-2.5-flash",
		SystemPrompt: "You are helpful.",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "Hello! How can I help?", resp.Content)
	assert.Equal(t, 20, resp.Usage.InputTokens)
	assert.Equal(t, 8, resp.Usage.OutputTokens)
}

func TestGemini_Stream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "streamGenerateContent")
		assert.Equal(t, "sse", r.URL.Query().Get("alt"))

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		writeSSE(w, `{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":1}}`)
		writeSSE(w, `{"candidates":[{"content":{"parts":[{"text":"!"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":2,"totalTokenCount":22}}`)
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", nil)
	p.baseURL = server.URL

	ch, err := p.Stream(context.Background(), Request{
		Model:    "gemini-2.5-flash",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)

	chunks, content := collectChunks(ch)
	assert.Equal(t, "Hello!", content)
	assert.True(t, len(chunks) > 0)
}

func TestOllama_Complete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Equal(t, "ethos/0.1.0", r.Header.Get("User-Agent"))

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "llama3.1:8b", body["model"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "ollama-resp-1",
			"model": "llama3.1:8b",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Hello from Ollama!",
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 15, "completion_tokens": 5},
		})
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, nil)

	resp, err := p.Complete(context.Background(), Request{
		Model:    "llama3.1:8b",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "ollama-resp-1", resp.ID)
	assert.Equal(t, "Hello from Ollama!", resp.Content)
}

func TestOllama_Stream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		writeSSE(w, `{"id":"ollama-stream","model":"llama3.1:8b","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"ollama-stream","model":"llama3.1:8b","choices":[{"index":0,"delta":{"content":" Ollama!"},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"ollama-stream","model":"llama3.1:8b","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, nil)

	ch, err := p.Stream(context.Background(), Request{
		Model:    "llama3.1:8b",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)

	_, content := collectChunks(ch)
	assert.Equal(t, "Hello Ollama!", content)
}

func TestOpenAICompat_CustomEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer custom-key", r.Header.Get("Authorization"))
		assert.Equal(t, "custom-provider", r.Header.Get("X-Custom"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "custom-resp",
			"model": "deepseek-chat",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Custom response",
				},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 3},
		})
	}))
	defer server.Close()

	p := NewOpenAICompatProvider("custom", server.URL, "custom-key", nil)
	p.headers["X-Custom"] = "custom-provider"

	resp, err := p.Complete(context.Background(), Request{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "custom-resp", resp.ID)
	assert.Equal(t, "Custom response", resp.Content)
	assert.Equal(t, "custom", p.Name())
}

func TestFactory_AllTypes(t *testing.T) {
	tests := []struct {
		typeStr      string
		expectedName string
	}{
		{"openai", "openai"},
		{"anthropic", "anthropic"},
		{"gemini", "gemini"},
		{"deepseek", "deepseek"},
		{"ollama", "ollama"},
		{"openrouter", "openrouter"},
		{"custom", "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.typeStr, func(t *testing.T) {
			cfg := FactoryConfig{
				Name:   tt.expectedName,
				Type:   tt.typeStr,
				APIKey: "test-key",
			}
			if tt.typeStr == "ollama" {
				cfg.BaseURL = "http://localhost:11434"
			}
			if tt.typeStr == "custom" {
				cfg.BaseURL = "http://localhost:8080/v1"
			}

			p, err := NewProvider(cfg)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedName, p.Name())
		})
	}
}

func TestFactory_UnknownType(t *testing.T) {
	cfg := FactoryConfig{
		Name:   "unknown",
		Type:   "nonexistent",
		APIKey: "test-key",
	}

	_, err := NewProvider(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider type")
}

func TestModels_OpenAI(t *testing.T) {
	models := OpenAIModels()
	assert.NotEmpty(t, models)

	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
		assert.NotEmpty(t, m.ID)
		assert.NotEmpty(t, m.Name)
		assert.True(t, m.MaxTokens > 0, "%s should have MaxTokens > 0", m.ID)
	}

	assert.True(t, ids["gpt-4o"], "should include gpt-4o")
	assert.True(t, ids["gpt-4o-mini"], "should include gpt-4o-mini")
	assert.True(t, ids["o1"], "should include o1")
	assert.True(t, ids["o1-mini"], "should include o1-mini")
	assert.True(t, ids["o3-mini"], "should include o3-mini")
}

func TestModels_Anthropic(t *testing.T) {
	models := AnthropicModels()
	assert.NotEmpty(t, models)

	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
		assert.True(t, m.MaxTokens > 0)
		assert.True(t, m.SupportsTools)
		assert.True(t, m.SupportsStreaming)
	}

	assert.True(t, ids["claude-sonnet-4-20250514"])
	assert.True(t, ids["claude-3.5-haiku-20241022"])
	assert.True(t, ids["claude-opus-4-20250514"])
}

func TestModels_Gemini(t *testing.T) {
	models := GeminiModels()
	assert.NotEmpty(t, models)

	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
	}

	assert.True(t, ids["gemini-2.5-pro"])
	assert.True(t, ids["gemini-2.5-flash"])
}

func TestModels_DeepSeek(t *testing.T) {
	models := DeepSeekModels()
	assert.NotEmpty(t, models)

	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
	}

	assert.True(t, ids["deepseek-chat"])
	assert.True(t, ids["deepseek-reasoner"])
}

func TestModels_Ollama(t *testing.T) {
	models := OllamaModels()
	assert.NotEmpty(t, models)

	for _, m := range models {
		assert.Equal(t, 0.0, m.CostIn)
		assert.Equal(t, 0.0, m.CostOut)
	}
}

func TestModels_OpenRouter(t *testing.T) {
	models := OpenRouterModels()
	assert.NotEmpty(t, models)
}

func TestMessageConversion_OpenAI(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!", ToolCalls: []ToolCall{
			{ID: "tc-1", Name: "search", Arguments: `{"q":"test"}`},
		}},
		{Role: "tool", Content: `{"result":"found"}`, ToolCallID: "tc-1"},
		{Role: "assistant", Content: "Found it!"},
	}

	converted := openAIMessages(msgs)
	assert.Len(t, converted, 5)

	assert.Equal(t, "system", converted[0].Role)
	assert.Equal(t, "You are helpful.", converted[0].Content)

	assert.Equal(t, "user", converted[1].Role)
	assert.Equal(t, "Hello", converted[1].Content)

	assert.Equal(t, "assistant", converted[2].Role)
	assert.Equal(t, "Hi!", converted[2].Content)
	require.Len(t, converted[2].ToolCalls, 1)
	assert.Equal(t, "tc-1", converted[2].ToolCalls[0].ID)
	assert.Equal(t, "search", converted[2].ToolCalls[0].Function.Name)
	assert.Equal(t, `{"q":"test"}`, converted[2].ToolCalls[0].Function.Arguments)

	assert.Equal(t, "tool", converted[3].Role)
	assert.Equal(t, "tc-1", converted[3].ToolCallID)
	assert.Equal(t, `{"result":"found"}`, converted[3].Content)

	assert.Equal(t, "assistant", converted[4].Role)
	assert.Equal(t, "Found it!", converted[4].Content)
}

func TestMessageConversion_Anthropic(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!", ToolCalls: []ToolCall{
			{ID: "tc-1", Name: "search", Arguments: `{"q":"test"}`},
		}},
		{Role: "tool", Content: "found", ToolCallID: "tc-1"},
	}

	converted := anthropicMessages(msgs)

	assert.Len(t, converted, 3)

	assert.Equal(t, "user", converted[0].Role)

	assert.Equal(t, "assistant", converted[1].Role)
	contentArr, ok := converted[1].Content.([]anthropicContentBlock)
	require.True(t, ok)
	assert.Len(t, contentArr, 2)
	assert.Equal(t, "text", contentArr[0].Type)
	assert.Equal(t, "Hi!", contentArr[0].Text)
	assert.Equal(t, "tool_use", contentArr[1].Type)
	assert.Equal(t, "tc-1", contentArr[1].ID)
	assert.Equal(t, "search", contentArr[1].Name)

	assert.Equal(t, "user", converted[2].Role)
	toolResult, ok := converted[2].Content.([]anthropicContentBlock)
	require.True(t, ok)
	assert.Equal(t, "tool_result", toolResult[0].Type)
	assert.Equal(t, "tc-1", toolResult[0].ToolUseID)
}

func TestMessageConversion_Gemini(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "How are you?"},
	}

	converted := geminiMessages(msgs)
	assert.Len(t, converted, 3)

	assert.Equal(t, "user", converted[0].Role)
	assert.Equal(t, "model", converted[1].Role)
	assert.Equal(t, "user", converted[2].Role)
}

func TestBaseProvider_HandleHTTPError(t *testing.T) {
	bp := NewBaseProvider("test", "http://localhost", "key", nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"internal server error"}`)
	}))
	defer server.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	resp, _ := http.DefaultClient.Do(req)

	err := bp.handleHTTPError(resp)
	require.Error(t, err)

	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 500, httpErr.StatusCode)
	assert.Contains(t, httpErr.Body, "internal server error")
}

func TestBaseProvider_Name(t *testing.T) {
	bp := NewBaseProvider("test-provider", "http://localhost", "key", nil)
	assert.Equal(t, "test-provider", bp.Name())
}

func TestBaseProvider_Models(t *testing.T) {
	models := []Model{{ID: "m1"}, {ID: "m2"}}
	bp := NewBaseProvider("test", "http://localhost", "key", models)
	assert.Len(t, bp.Models(), 2)
	assert.Equal(t, "m1", bp.Models()[0].ID)
}

func TestOllama_Name(t *testing.T) {
	p := NewOllamaProvider("http://localhost:11434", nil)
	assert.Equal(t, "ollama", p.Name())
}

func TestOpenAIProvider_Name(t *testing.T) {
	p := NewOpenAIProvider("key", nil)
	assert.Equal(t, "openai", p.Name())
}

func TestAnthropicProvider_Name(t *testing.T) {
	p := NewAnthropicProvider("key", nil)
	assert.Equal(t, "anthropic", p.Name())
}

func TestGeminiProvider_Name(t *testing.T) {
	p := NewGeminiProvider("key", nil)
	assert.Equal(t, "gemini", p.Name())
}

func TestOpenAIStream_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", nil)
	p.baseURL = server.URL

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Stream(ctx, Request{
		Model:    "gpt-4o",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	assert.Error(t, err)
}

func TestAnthropic_Complete_NoSystemPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		_, hasSystem := body["system"]
		assert.False(t, hasSystem, "system field should be omitted when no system prompt")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "msg-nosys",
			"type":    "message",
			"role":    "assistant",
			"content": []map[string]any{{"type": "text", "text": "ok"}},
			"model":   "claude-sonnet-4-20250514",
			"usage":   map[string]any{"input_tokens": 5, "output_tokens": 2},
		})
	}))
	defer server.Close()

	p := NewAnthropicProvider("test-key", nil)
	p.baseURL = server.URL

	resp, err := p.Complete(context.Background(), Request{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages:  []Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
}

func TestOllama_DefaultBaseURL(t *testing.T) {
	p := NewOllamaProvider("", nil)
	assert.Equal(t, "http://localhost:11434/v1", p.baseURL)
}

func TestOpenAI_Complete_SendsTemperature(t *testing.T) {
	var receivedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-temp",
			"model": "gpt-4o",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 2},
		})
	}))
	defer server.Close()

	p := NewOpenAIProvider("test-key", nil)
	p.baseURL = server.URL

	_, err := p.Complete(context.Background(), Request{
		Model:       "gpt-4o",
		Temperature: 0.5,
		Messages:    []Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 0.5, receivedBody["temperature"])
}

func TestGemini_Complete_AssistantRole(t *testing.T) {
	var receivedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{"text": "response"}},
					"role":  "model",
				},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 5},
		})
	}))
	defer server.Close()

	p := NewGeminiProvider("test-key", nil)
	p.baseURL = server.URL

	_, err := p.Complete(context.Background(), Request{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			{Role: "user", Content: "Hi"},
			{Role: "assistant", Content: "Hello!"},
			{Role: "user", Content: "How are you?"},
		},
	})
	require.NoError(t, err)

	contents := receivedBody["contents"].([]any)
	assert.Equal(t, "user", contents[0].(map[string]any)["role"])
	assert.Equal(t, "model", contents[1].(map[string]any)["role"])
	assert.Equal(t, "user", contents[2].(map[string]any)["role"])
}

func TestOpenAICompat_Streaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(w, `{"id":"ds-stream","model":"deepseek-chat","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}`)
		writeSSE(w, `{"id":"ds-stream","model":"deepseek-chat","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	p := NewOpenAICompatProvider("deepseek", server.URL, "test-key", nil)
	ch, err := p.Stream(context.Background(), Request{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})
	require.NoError(t, err)

	_, content := collectChunks(ch)
	assert.Equal(t, "Hi", content)
}
