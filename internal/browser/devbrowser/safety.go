// Package devbrowser — URL safety + SSRF guards.
//
// The "sandboxed AI-safe" half of the AI-safe browser. Unlike the
// general Playwright wrapper in internal/browser, dev-browser is
// designed for agent-driven traffic where the URL comes from the
// model and might be a hallucination, a corner case, or an
// adversarial input planted by a prompt-injection in a tool output.
//
// What this guards against:
//
//   - file:// — would read arbitrary local files
//   - http://localhost / 127.0.0.1 — SSRF to dev servers + metadata
//   - http://[private IPs] — SSRF to internal infra
//   - http://[cloud metadata IPs] — AWS/GCP/Azure secrets servers
//   - data: / javascript: — agent shouldn't be able to inject these
//   - Bare hostnames the agent tries to resolve as URLs
//
// What this does NOT guard against:
//
//   - Public URLs that themselves do nasty things (redirect to
//     private IPs, return malicious content). Mitigated by limiting
//     what the agent can DO with the page (no Evaluate, no file
//     downloads, etc.) rather than by pre-validating URLs.
package devbrowser

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// AllowedSchemes is the closed set of URL schemes dev-browser will
// open. http+https only — every other scheme is a footgun for agent
// traffic. Tests can append to this list but production callers
// should not.
var AllowedSchemes = map[string]bool{
	"http":  true,
	"https": true,
}

// validateURL parses raw, normalises it, and rejects anything that
// would let the agent read local files or reach private/cloud
// metadata endpoints. Returns the parsed URL on success so callers
// can use the normalised form instead of re-parsing.
func validateURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("dev-browser: empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("dev-browser: parse url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if !AllowedSchemes[scheme] {
		return nil, fmt.Errorf("dev-browser: scheme %q not allowed (http/https only)", scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("dev-browser: url missing host")
	}

	// Resolve the host. We deliberately do this synchronously even
	// though Chrome will do it again — pre-resolving here lets us
	// reject private IPs before opening the navigation.
	host := u.Hostname()
	if isBlockedHostname(host) {
		return nil, fmt.Errorf("dev-browser: host %q blocked", host)
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		// DNS failure isn't safety-relevant on its own — we let
		// chromedp surface the real error. But if the host LOOKS
		// like an IP and the IP is private, block it.
		if ip := net.ParseIP(host); ip != nil && isPrivateOrSpecialIP(ip) {
			return nil, fmt.Errorf("dev-browser: host %q is a private/special IP", host)
		}
		return u, nil
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip != nil && isPrivateOrSpecialIP(ip) {
			return nil, fmt.Errorf("dev-browser: %q resolves to private/special IP %s", host, addr)
		}
	}
	return u, nil
}

// isBlockedHostname catches the easy cases by name — localhost +
// well-known cloud metadata endpoints. IP-form addresses are caught
// by the resolver path.
func isBlockedHostname(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "localhost":
		return true
	case "metadata.google.internal", "metadata":
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}

// isPrivateOrSpecialIP returns true for loopback, RFC1918, link-local,
// cloud metadata, and other "you didn't mean this" ranges.
func isPrivateOrSpecialIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// IPv4 mapped to IPv6 → unwrap so the .Is*() checks below work.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsLoopback():
		return true
	case ip.IsPrivate():
		return true // RFC1918 + RFC4193
	case ip.IsLinkLocalUnicast():
		return true
	case ip.IsLinkLocalMulticast():
		return true
	case ip.IsUnspecified():
		return true // 0.0.0.0
	}
	// Cloud metadata: 169.254.169.254 (AWS/GCP/Azure)
	if ip.Equal(net.IPv4(169, 254, 169, 254)) {
		return true
	}
	// fd00::/8 — IPv6 ULA, already caught by IsPrivate but explicit
	// for the reader.
	return false
}
