package tools

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// privateCIDRs are the SSRF blocklist applied at the IP level. Covers
// RFC 1918 (LAN), loopback (v4 + v6), link-local (incl. AWS metadata
// at 169.254.169.254), CGNAT, and ULA v6. Hostnames that resolve to any
// of these are rejected. Public IPs always pass.
var privateCIDRs = func() []*net.IPNet {
	raw := []string{
		"127.0.0.0/8",    // loopback v4
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // link-local (cloud metadata services)
		"100.64.0.0/10",  // CGNAT
		"0.0.0.0/8",      // self
		"::1/128",        // loopback v6
		"fc00::/7",       // ULA v6
		"fe80::/10",      // link-local v6
	}
	out := make([]*net.IPNet, 0, len(raw))
	for _, c := range raw {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}()

// hostIsPrivate returns true when the host (literal IP or DNS name)
// resolves to any private range. In the browser tool this is the
// primary SSRF gate; in the web tool it also serves as a pre-check
// before the pinned-dialer layer (C9) and redirect re-validation (C10)
// provide defence-in-depth.
func hostIsPrivate(host string) bool {
	if host == "" {
		return true
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	check := func(ip net.IP) bool {
		if ip == nil {
			return false
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() || ip.IsUnspecified() {
			return true
		}
		for _, n := range privateCIDRs {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return check(ip)
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return true // fail closed on resolution failure
	}
	for _, ip := range addrs {
		if check(ip) {
			return true
		}
	}
	return false
}

type WebTool struct {
	client     *http.Client
	maxSize    int64
	allowLocal bool // when true, skip the SSRF block (test fixtures only)
}

// AllowLocalForTests bypasses the SSRF block so httptest servers
// (which bind 127.0.0.1) work in unit tests. Production callers must
// never set this — leaving it false keeps the agent from being a
// reflective probe for internal services.
func (w *WebTool) AllowLocalForTests() *WebTool {
	w.allowLocal = true
	return w
}

type WebInput struct {
	URL     string `json:"url"`
	Format  string `json:"format"`
	MaxSize int    `json:"max_size"`
}

type WebOutput struct {
	URL         string `json:"url"`
	Content     string `json:"content"`
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type"`
	Truncated   bool   `json:"truncated"`
}

func NewWebTool() *WebTool {
	return &WebTool{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		maxSize: 5 * 1024 * 1024,
	}
}

func (w *WebTool) Name() string {
	return "web"
}

func (w *WebTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in WebInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}

	if in.URL == "" {
		return nil, fmt.Errorf("web: url is required")
	}

	// Parse before any string-prefix sniff so we honour Scheme casing
	// (Go's http.NewRequest accepts mixed-case schemes; the previous
	// lowercase-prefix check could be bypassed with `HTTPS://`).
	parsed, err := url.Parse(in.URL)
	if err != nil {
		return nil, fmt.Errorf("web: parse url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("web: only http and https schemes are allowed")
	}
	// SSRF gate. Block RFC1918, loopback, link-local (169.254.169.254
	// is AWS/GCP metadata), CGNAT, and v6 equivalents. Bare `localhost`
	// is rejected by name before resolution. Tests can opt out via
	// AllowLocalForTests().
	if !w.allowLocal && hostIsPrivate(parsed.Hostname()) {
		return nil, fmt.Errorf("web: host %q resolves to a private/internal address", parsed.Hostname())
	}

	maxSize := w.maxSize
	if in.MaxSize > 0 {
		maxSize = int64(in.MaxSize)
	}

	// C9: DNS rebinding defence — resolve DNS once and pin the dialer to
	// the resolved IP so http.Client cannot be tricked into connecting
	// to a different address on a second resolution.
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("web: cannot resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("web: no addresses for %q", host)
	}

	// Pin transport to the first resolved IP. Preserve SNI by setting
	// TLSClientConfig.ServerName to the original hostname so HTTPS
	// handshakes still present the correct certificate name.
	pinnedDialer := &net.Dialer{Timeout: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return pinnedDialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
		TLSClientConfig: &tls.Config{
			ServerName: host,
		},
	}

	// C10: redirect SSRF — validate every redirect target before
	// following it so an attacker cannot bounce the request to a
	// private IP via a 302.
	perReqClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("web: stopped after 10 redirects")
			}
			redirectHost := req.URL.Hostname()
			if !w.allowLocal && hostIsPrivate(redirectHost) {
				return fmt.Errorf("web: redirect to private address %q blocked", redirectHost)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	req.Header.Set("User-Agent", "overkill/0.1.0-dev")

	resp, err := perReqClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")

	limited := io.LimitReader(resp.Body, maxSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}

	truncated := len(data) > int(maxSize)
	if truncated {
		data = data[:maxSize]
	}

	output := WebOutput{
		URL:         in.URL,
		Content:     string(data),
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		Truncated:   truncated,
	}

	raw, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	return raw, nil
}
