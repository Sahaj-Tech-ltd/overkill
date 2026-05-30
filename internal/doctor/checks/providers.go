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
func providerEndpoint(p struct {
	Type    string
	BaseURL string
}) (url, header, prefix string) {
	switch p.Type {
	case "openai":
		return "https://api.openai.com/v1/models", "Authorization", "Bearer "
	case "anthropic":
		// Anthropic gates this behind an x-api-key header rather than Bearer.
		return "https://api.anthropic.com/v1/models", "x-api-key", ""
	case "gemini":
		// Gemini uses ?key= rather than a header; we'll add it inline below.
		return "https://generativelanguage.googleapis.com/v1beta/models", "", ""
	case "deepseek":
		return "https://api.deepseek.com/v1/models", "Authorization", "Bearer "
	case "openrouter":
		return "https://openrouter.ai/api/v1/models", "Authorization", "Bearer "
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
				url, header, prefix := providerEndpoint(struct {
					Type    string
					BaseURL string
				}{Type: p.Type, BaseURL: p.BaseURL})
				if url == "" {
					return skip(fmt.Sprintf("no probe URL for type %q", p.Type))
				}
				if p.Type != "ollama" && p.APIKey == "" {
					return failf("run /config and set api_key for "+p.Name,
						"no api key configured for provider %q", p.Name)
				}

				// Gemini takes ?key= as a query param, not a header.
				probeURL := url
				if p.Type == "gemini" {
					probeURL = url + "?key=" + p.APIKey
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
