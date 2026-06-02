package providers

import (
	"context"
	"os"
)

type OllamaProvider struct {
	*OpenAIProvider
}

func NewOllamaProvider(baseURL string, models []Model) *OllamaProvider {
	if baseURL == "" {
		baseURL = os.Getenv("OLLAMA_HOST")
	}
	if baseURL == "" {
		baseURL = CanonicalBaseURL("ollama")
	}
	inner := NewOpenAIProvider("", models)
	inner.BaseProvider.name = "ollama"
	inner.BaseProvider.baseURL = baseURL + "/v1"
	delete(inner.BaseProvider.headers, "Authorization")
	return &OllamaProvider{OpenAIProvider: inner}
}

func (p *OllamaProvider) Complete(ctx context.Context, req Request) (Response, error) {
	return p.OpenAIProvider.Complete(ctx, req)
}

func (p *OllamaProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	return p.OpenAIProvider.Stream(ctx, req)
}
