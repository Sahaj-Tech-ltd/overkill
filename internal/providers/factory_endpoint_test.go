package providers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFactoryHonorsConfiguredProtocolEndpoint(t *testing.T) {
	for _, kind := range []string{"openai", "anthropic", "gemini", "openrouter", "groq"} {
		t.Run(kind, func(t *testing.T) {
			provider, err := NewProvider(FactoryConfig{Type: kind, APIKey: "fixture", BaseURL: "http://127.0.0.1:12345/private/v1/"})
			require.NoError(t, err)
			var endpoint string
			switch p := provider.(type) {
			case *OpenAIProvider:
				endpoint = p.baseURL
			case *OpenAICompatProvider:
				endpoint = p.baseURL
			case *AnthropicProvider:
				endpoint = p.baseURL
			case *GeminiProvider:
				endpoint = p.baseURL
			default:
				t.Fatalf("unexpected protocol %T", provider)
			}
			require.Equal(t, "http://127.0.0.1:12345/private/v1", endpoint)
		})
	}
}

func TestFactoryRejectsInvalidEndpointWithoutExposingCredentials(t *testing.T) {
	for _, endpoint := range []string{"invalid", "ftp://example.test", "https://secret:password@example.test", "https://example.test/?token=secret"} {
		_, err := NewProvider(FactoryConfig{Type: "openai", APIKey: "fixture", BaseURL: endpoint})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "password")
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestAnthropicHistoricalOriginUsesNativeVersionedPrefix(t *testing.T) {
	p, err := NewProvider(FactoryConfig{Type: "anthropic", APIKey: "fixture", BaseURL: CanonicalBaseURL("anthropic")})
	require.NoError(t, err)
	require.Equal(t, "https://api.anthropic.com/v1", p.(*AnthropicProvider).baseURL)
}
