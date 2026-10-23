package providers

import (
	"fmt"
	"net/url"
	"strings"
)

// ResolveBaseURL returns the API prefix shared by execution and diagnostics.
// Preserve private gateway paths; normalize the historical official Anthropic
// origin default, whose native protocol endpoints require the /v1 prefix.
func ResolveBaseURL(kind, configured string) (string, error) {
	base := configured
	if base == "" {
		base = CanonicalBaseURL(kind)
	}
	endpoint, err := url.Parse(base)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", fmt.Errorf("providers: base_url must be an HTTP(S) endpoint without credentials, query or fragment")
	}
	base = strings.TrimRight(base, "/")
	if kind == "anthropic" && base == "https://api.anthropic.com" {
		base += "/v1"
	}
	return base, nil
}
