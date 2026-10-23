package coordination

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// NewPeerHTTPClient admits explicit loopback HTTP for local agents. HTTPS uses
// the callback transport's public-IP validation, DNS pinning and TLS policy.
// Advertised discovery endpoints are never dialled automatically.
func NewPeerHTTPClient(endpoint string) (*http.Client, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("coordination: explicit endpoint required")
	}
	if err := ValidatePeerEndpoint(endpoint); err != nil {
		return nil, err
	}
	transport := newCallbackTransport()
	if len(endpoint) >= 7 && endpoint[:7] == "http://" {
		transport = &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 16384, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("coordination: invalid local endpoint")
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("coordination: local endpoint lookup failed")
			}
			for _, ip := range ips {
				if !ip.IsLoopback() {
					return nil, fmt.Errorf("coordination: HTTP endpoint resolved outside loopback")
				}
			}
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		}}
	} else if err := ValidateCallbackDestinations(map[string]string{"peer": endpoint}); err != nil {
		return nil, fmt.Errorf("coordination: HTTPS endpoint requires protected public destination")
	}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
