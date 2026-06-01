package checks

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// providerEndpoint maps provider type → a HEAD/GET-able URL that confirms
// reachability + auth. We avoid running real model.list because it costs
// money on some providers; an authenticated 200 from /v1/models is enough.
// ProviderEndpoint returns the probe URL, auth header name, and key prefix
// for reaching a provider's model list endpoint. Used by both the health-check
// doctor and the API layer to validate provider credentials without running
// an expensive model call.
func ProviderEndpoint(p struct {
	Type    string
	BaseURL string
}) (url, header, prefix string) {
	switch p.Type {
	case "openai", "anthropic", "gemini", "deepseek", "openrouter":
		base := p.BaseURL
		if base == "" {
			base = providers.CanonicalBaseURL(p.Type)
		}
		suffix := "/v1/models"
		if p.Type == "gemini" {
			suffix = "/v1beta/models"
		}
		header, prefix := "Authorization", "Bearer "
		if p.Type == "anthropic" {
			header, prefix = "x-api-key", ""
		}
		if p.Type == "gemini" {
			header, prefix = "", "?key="
		}
		return base + suffix, header, prefix
	case "ollama":
		base := p.BaseURL
		if base == "" {
			base = providers.CanonicalBaseURL("ollama")
		}
		return base + "/api/tags", "", ""
	case "custom":
		if p.BaseURL == "" {
			return "", "", ""
		}
		return p.BaseURL + "/models", "Authorization", "Bearer "
	}
	return "", "", ""
}

// providerNeedsKey returns false for provider types that are known to work
// without an API key (local or self-hosted providers like Ollama, LM Studio,
// or any provider with a localhost base URL).
func providerNeedsKey(providerType string) bool {
	switch providerType {
	case "ollama", "lmstudio", "local":
		return false
	}
	return true
}

// RegisterProviders adds one parallel reachability check per configured
// provider. Each runs under its own 5s context budget (default).
//
// WARNING: These checks issue live HTTP requests using real API keys.
// While they only hit free model-list endpoints (/v1/models etc.), some
// pay-per-request providers may still count these toward rate limits.
// To skip provider checks, run with --skip-provider-check.
func RegisterProviders(r *doctor.Runner, d Deps) {
	if d.Cfg == nil {
		return
	}
	for _, p := range d.Cfg.Providers {
		p := p // capture
		r.Register(doctor.SubsystemCheck{
			ID:       "provider." + p.Name,
			Name:     "Provider reachable: " + p.Name,
			Category: doctor.CatProvider,
			Parallel: true,
			Fn: func(ctx context.Context) doctor.Result {
				url, header, prefix := ProviderEndpoint(struct {
					Type    string
					BaseURL string
				}{Type: p.Type, BaseURL: p.BaseURL})
				if url == "" {
					return skip(fmt.Sprintf("no probe URL for type %q", p.Type))
				}
				if providerNeedsKey(p.Type) && p.APIKey == "" {
					return failf("run /config and set api_key for "+p.Name,
						"no api key configured for provider %q", p.Name)
				}

				// Build the probe URL — Gemini and similar providers that use
				// query-param auth have their key marker in prefix.
				probeURL := url
				if prefix != "" {
					probeURL = url + prefix + p.APIKey
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
				if err != nil {
					return failf("file a bug — request build failed", "build request: %v", err)
				}
				if header != "" {
					req.Header.Set(header, prefix+p.APIKey)
				}
				resp, err := d.HTTP.Do(req)
				if err != nil {
					var nerr net.Error
					if e, ok := err.(net.Error); ok && e.Timeout() {
						nerr = e
					}
					if nerr != nil {
						return warnf("retry on a working network — request timed out",
							"network timeout reaching %s", probeURL)
					}
					return failf("check network access to "+probeURL,
						"network error: %v", err)
				}
				defer resp.Body.Close()
				switch {
				case resp.StatusCode == 200:
					return okf("HTTP 200 from %s", probeURL)
				case resp.StatusCode == 401 || resp.StatusCode == 403:
					return warnf("run /config and set a valid api_key for "+p.Name,
						"HTTP %d (auth) from %s", resp.StatusCode, probeURL)
				case resp.StatusCode >= 500:
					return warnf("provider may be down; check status page",
						"HTTP %d from %s", resp.StatusCode, probeURL)
				default:
					return warnf("inspect endpoint manually",
						"HTTP %d from %s", resp.StatusCode, probeURL)
				}
			},
		})
	}
}
