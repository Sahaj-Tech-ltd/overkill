package checks

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
	"github.com/stretchr/testify/require"
)

type probeTransport func(*http.Request) (*http.Response, error)

func (fn probeTransport) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestProviderProbesKeepKeysOutOfURLsAndReports(t *testing.T) {
	const key = "fixture/key+?secret&=test"
	for _, kind := range []string{"openai", "anthropic", "gemini", "custom", "deepseek", "openrouter"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				require.Equal(t, "/private/v1/models", request.URL.Path)
				require.Empty(t, request.URL.RawQuery)
				require.NotContains(t, request.RequestURI, "secret")
				switch kind {
				case "anthropic":
					require.Equal(t, key, request.Header.Get("x-api-key"))
					require.NotEmpty(t, request.Header.Get("anthropic-version"))
				case "gemini":
					require.Equal(t, key, request.Header.Get("x-goog-api-key"))
				default:
					require.Equal(t, "Bearer "+key, request.Header.Get("Authorization"))
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			cfg := config.Default()
			cfg.Providers = []config.ProviderConfig{{Name: "test", Type: kind, BaseURL: server.URL + "/private/v1/", APIKey: key}}
			result := runOne(t, RegisterProviders, newTestDeps(t, cfg), "provider.test")
			require.Equal(t, doctor.SevOK, result.Status)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			for _, form := range []string{key, url.QueryEscape(key), url.PathEscape(key)} {
				require.NotContains(t, string(encoded), form)
			}
		})
	}
}

func TestProviderDiagnosticRedactsTransportErrorsAndRejectsRedirects(t *testing.T) {
	const key = "probe-private/key+token"
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Name: "test", Type: "custom", BaseURL: "https://example.test/v1", APIKey: key}}
	deps := newTestDeps(t, cfg)
	deps.HTTP = &http.Client{Transport: probeTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("rejected " + key + " escaped " + url.QueryEscape(key))
	})}
	result := runOne(t, RegisterProviders, deps, "provider.test")
	require.Equal(t, doctor.SevFail, result.Status)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), key)
	require.NotContains(t, string(encoded), url.QueryEscape(key))
	require.Contains(t, string(encoded), "[redacted]")
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/collect", http.StatusFound)
	}))
	defer origin.Close()
	cfg.Providers[0].BaseURL = origin.URL
	result = runOne(t, RegisterProviders, newTestDeps(t, cfg), "provider.test")
	require.Equal(t, doctor.SevWarn, result.Status)
	require.Zero(t, forwarded.Load())
}

func TestCanonicalProbePathsAndInvalidEndpoints(t *testing.T) {
	for kind, expected := range map[string]string{"openai": "https://api.openai.com/v1/models", "anthropic": "https://api.anthropic.com/v1/models", "gemini": "https://generativelanguage.googleapis.com/v1beta/models", "openrouter": "https://openrouter.ai/api/v1/models"} {
		endpoint, _, _ := ProviderEndpoint(struct {
			Type    string
			BaseURL string
		}{Type: kind})
		require.Equal(t, expected, endpoint)
	}
	for _, invalid := range []string{"https://secret:password@example.test", "https://example.test/?key=secret", "https://example.test/#secret", "file:///etc/passwd"} {
		endpoint, _, _ := ProviderEndpoint(struct {
			Type    string
			BaseURL string
		}{Type: "custom", BaseURL: invalid})
		require.Empty(t, endpoint)
	}
}
