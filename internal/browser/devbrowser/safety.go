package devbrowser

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrNetworkIsolationUnavailable means the requested restricted browser cannot
// be provided. The current Chrome API does not enforce a pinned, validated
// destination for every redirect, subresource, worker and other network request.
// Initial URL/DNS checks or CDP interception alone are not network isolation.
var ErrNetworkIsolationUnavailable = errors.New("dev-browser: restricted-network browsing is unavailable: request-time destination isolation is not implemented")

// Options are application-owned construction policy, never model tool input.
type Options struct {
	// TrustedNetwork explicitly permits Chrome to access public, local and private
	// networks. Only enable for browsing whose network access the operator trusts,
	// such as an authenticated local development workflow. This does not provide
	// restricted-network isolation, redirect checks or DNS-rebinding protection.
	TrustedNetwork bool
}

func (m *Manager) checkNetworkMode() error {
	if !m.trustedNetwork {
		return ErrNetworkIsolationUnavailable
	}
	return nil
}

// parseHTTPURL is syntax validation for trusted-network browsing, not an SSRF
// guard. It does not resolve DNS or claim to constrain subsequent page requests.
func parseHTTPURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("dev-browser: empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("dev-browser: parse url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("dev-browser: scheme %q not allowed (http/https only)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("dev-browser: url missing host")
	}
	if u.User != nil {
		return nil, fmt.Errorf("dev-browser: URL userinfo is not allowed; use an explicit browser authentication flow")
	}
	return u, nil
}
