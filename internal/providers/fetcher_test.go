package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchOpenAI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "openai", "test-key", server.URL)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	assert.Equal(t, "gpt-4o", models[0].ID)
	assert.Equal(t, "gpt-4o", models[0].Name)
	assert.Equal(t, "gpt-4o-mini", models[1].ID)
}

func TestFetchAnthropic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"claude-sonnet-4-20250514"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "anthropic", "my-key", server.URL)
	require.NoError(t, err)
	assert.Len(t, models, 1)
	assert.Equal(t, "claude-sonnet-4-20250514", models[0].ID)
}

func TestFetchAnthropicHeader(t *testing.T) {
	var authHeader, xAPIKeyHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		xAPIKeyHeader = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"claude-opus-4-20250514"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	_, err := fetcher.Fetch(context.Background(), "anthropic", "api-key-123", server.URL)
	require.NoError(t, err)
	assert.Empty(t, authHeader, "Anthropic should NOT use Bearer auth")
	assert.Equal(t, "api-key-123", xAPIKeyHeader, "Anthropic should use x-api-key header")
}

func TestFetchGemini(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		assert.Equal(t, "test-api-key", r.URL.Query().Get("key"))
		// Gemini should not send an auth header
		assert.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-pro"},{"name":"models/gemini-2.5-flash"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "gemini", "test-api-key", server.URL)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	assert.Equal(t, "gemini-2.5-pro", models[0].ID)
	assert.Equal(t, "gemini-2.5-pro", models[0].Name)
	assert.Equal(t, "gemini-2.5-flash", models[1].ID)
}

func TestFetchGeminiNameStripping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[{"name":"models/gemini-2.0-flash"},{"name":"gemini-2.5-pro"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "gemini", "key", server.URL)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	// Name with "models/" prefix gets stripped
	assert.Equal(t, "gemini-2.0-flash", models[0].ID)
	// Name already without prefix stays as-is
	assert.Equal(t, "gemini-2.5-pro", models[1].ID)
}

func TestFetchOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tags", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"), "Ollama should not send auth header")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[{"name":"llama3.1:8b"},{"name":"mistral:7b"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "ollama", "", server.URL)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	assert.Equal(t, "llama3.1:8b", models[0].ID)
	assert.Equal(t, "mistral:7b", models[1].ID)
}

func TestFetchOllamaNoAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[{"name":"codellama:7b"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	_, err := fetcher.Fetch(context.Background(), "ollama", "should-not-be-sent", server.URL)
	require.NoError(t, err)
	assert.Empty(t, receivedAuth, "Ollama should not send an Authorization header even if apiKey is provided")
}

func TestFetchDeepSeek(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		assert.Equal(t, "Bearer deepseek-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "deepseek", "deepseek-key", server.URL)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	assert.Equal(t, "deepseek-chat", models[0].ID)
	assert.Equal(t, "deepseek-reasoner", models[1].ID)
}

func TestFetchOpenRouter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		assert.Equal(t, "Bearer or-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"anthropic/claude-sonnet-4-20250514"},{"id":"openai/gpt-4o"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "openrouter", "or-key", server.URL)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	assert.Equal(t, "anthropic/claude-sonnet-4-20250514", models[0].ID)
	assert.Equal(t, "openai/gpt-4o", models[1].ID)
}

func TestFetchCustom(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		assert.Equal(t, "Bearer custom-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"custom-model-v1"},{"id":"custom-model-v2"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "custom", "custom-key", server.URL)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	assert.Equal(t, "custom-model-v1", models[0].ID)
	assert.Equal(t, "custom-model-v2", models[1].ID)
}

func TestFetchFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	fetcher := NewModelFetcher()

	models, err := fetcher.Fetch(context.Background(), "openai", "key", server.URL)
	require.NoError(t, err)
	assert.Equal(t, OpenAIModels(), models)

	models, err = fetcher.Fetch(context.Background(), "anthropic", "key", server.URL)
	require.NoError(t, err)
	assert.Equal(t, AnthropicModels(), models)

	models, err = fetcher.Fetch(context.Background(), "gemini", "key", server.URL)
	require.NoError(t, err)
	assert.Equal(t, GeminiModels(), models)

	models, err = fetcher.Fetch(context.Background(), "deepseek", "key", server.URL)
	require.NoError(t, err)
	assert.Equal(t, DeepSeekModels(), models)

	models, err = fetcher.Fetch(context.Background(), "ollama", "key", server.URL)
	require.NoError(t, err)
	assert.Equal(t, OllamaModels(), models)

	models, err = fetcher.Fetch(context.Background(), "openrouter", "key", server.URL)
	require.NoError(t, err)
	assert.Equal(t, OpenRouterModels(), models)

	models, err = fetcher.Fetch(context.Background(), "custom", "key", server.URL)
	require.NoError(t, err)
	assert.Equal(t, OpenAIModels(), models)
}

func TestFetchFilterNonChatModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"text-embedding-3-small"},{"id":"whisper-1"},{"id":"text-moderation-latest"},{"id":"dall-e-3"},{"id":"tts-1"},{"id":"gpt-4o-mini"},{"id":"ft:gpt-3.5-turbo-instruct"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "openai", "key", server.URL)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	assert.Equal(t, "gpt-4o", models[0].ID)
	assert.Equal(t, "gpt-4o-mini", models[1].ID)
}

func TestFetchDefaultFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"gpt-4o"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "openai", "key", server.URL)
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.True(t, models[0].SupportsTools, "fetched models should have SupportsTools=true by default")
	assert.True(t, models[0].SupportsStreaming, "fetched models should have SupportsStreaming=true by default")
	assert.Zero(t, models[0].CostIn, "fetched models should have CostIn=0 by default")
	assert.Zero(t, models[0].CostOut, "fetched models should have CostOut=0 by default")
	assert.Zero(t, models[0].MaxTokens, "fetched models should have MaxTokens=0 by default")
	assert.False(t, models[0].SupportsVision)
	assert.False(t, models[0].Reasoning)
}

func TestFetchInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "openai", "key", server.URL)
	require.NoError(t, err)
	// Should fall back to hardcoded models
	assert.Equal(t, OpenAIModels(), models)
}

func TestFetchEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	models, err := fetcher.Fetch(context.Background(), "openai", "key", server.URL)
	require.NoError(t, err)
	assert.Empty(t, models)
}

func TestFetchGeminiNoAuthHeader(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		assert.Contains(t, r.URL.RawQuery, "key=gemini-key-123")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-pro"}]}`))
	}))
	defer server.Close()

	fetcher := NewModelFetcher()
	_, err := fetcher.Fetch(context.Background(), "gemini", "gemini-key-123", server.URL)
	require.NoError(t, err)
	assert.Empty(t, receivedAuth, "Gemini should not send an Authorization header; key is passed via URL param")
}
