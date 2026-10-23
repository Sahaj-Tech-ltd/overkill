package coordination

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"time"
)

var destinationName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// ValidateCallbackDestinations validates operator configuration, never model input.
func ValidateCallbackDestinations(destinations map[string]string) error {
	if len(destinations) > 64 {
		return fmt.Errorf("callbacks: at most 64 destinations are supported")
	}
	for name, address := range destinations {
		u, err := url.Parse(address)
		if !destinationName.MatchString(name) || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || len(address) > 4096 || (u.Port() != "" && u.Port() != "443") {
			return fmt.Errorf("callbacks: named destinations require HTTPS on port 443 without credentials or fragments")
		}
		if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !callbackPublicIP(ip) {
			return fmt.Errorf("callbacks: destination must use public addresses")
		}
	}
	return nil
}

func callbackPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "::/96", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20", "fec0::/10", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}

// newCallbackTransport resolves and validates every DNS answer on every dial,
// then pins the socket address. TLS still verifies the original URL hostname.
// No proxy, redirects, alternate TLS dial path, or shared connection pool.
func newCallbackTransport() *http.Transport {
	return newCallbackTransportWithNetwork(net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: 5 * time.Second}).DialContext)
}

// Network injection stays private; tests can exercise rebinding and TLS against
// a controlled socket without permitting private destinations in production.
func newCallbackTransportWithNetwork(lookup func(context.Context, string, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	return &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16384,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("callbacks: invalid destination")
			}
			ips, err := lookup(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("callbacks: destination lookup failed")
			}
			for _, ip := range ips {
				if !callbackPublicIP(ip) {
					return nil, fmt.Errorf("callbacks: destination resolved to a protected address")
				}
			}
			return dial(ctx, network, net.JoinHostPort(ips[0].String(), port))
		}}
}
