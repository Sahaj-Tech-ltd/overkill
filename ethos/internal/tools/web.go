package tools

import (
	"context"
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
		"127.0.0.0/8",     // loopback v4
		"10.0.0.0/8",      // RFC 1918
		"172.16.0.0/12",   // RFC 1918
		"192.168.0.0/16",  // RFC 1918
		"169.254.0.0/16",  // link-local (cloud metadata services)
		"100.64.0.0/10",   // CGNAT
		"0.0.0.0/8",       // self
		"::1/128",         // loopback v6
		"fc00::/7",        // ULA v6
		"fe80::/10",       // link-local v6
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
// resolves to any private range. DNS rebinding is partially mitigated:
// we resolve once here and the http.Client may resolve again — for
// stronger guarantees the caller could pin the dialer to the resolved
// IP, but for an agent-side SSRF block this catches the common cases.
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	req.Header.Set("User-Agent", "Overkill/1.0")

	resp, err := w.client.Do(req)
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
