package imagegen

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"time"
)

const maxDownloadedImageBytes = 50 << 20

// Provider-returned URLs are untrusted. Resolve on every connection, reject
// mixed public/private DNS answers, and dial the checked IP while TLS verifies
// the original hostname. Proxies, redirects and connection reuse are disabled.
func imageDownloadTransport(lookup func(context.Context, string, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	return &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 16384, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" {
			return nil, fmt.Errorf("imagegen: invalid download destination")
		}
		ips, err := lookup(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("imagegen: download lookup failed")
		}
		for _, ip := range ips {
			if !publicImageIP(ip) {
				return nil, fmt.Errorf("imagegen: protected download address")
			}
		}
		return dial(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}}
}

func publicImageIP(ip netip.Addr) bool {
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

func downloadImage(ctx context.Context, address, prefix, ext string) (string, error) {
	transport := imageDownloadTransport(net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: 5 * time.Second}).DialContext)
	defer transport.CloseIdleConnections()
	return downloadImageWithClient(ctx, address, prefix, ext, &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
}

// Client injection is private and used only by socket/response fixtures.
func downloadImageWithClient(ctx context.Context, address, prefix, ext string, client *http.Client) (out string, err error) {
	u, parseErr := url.Parse(address)
	if parseErr != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || len(address) > 16384 || (u.Port() != "" && u.Port() != "443") {
		return "", fmt.Errorf("imagegen: download requires public HTTPS on port 443")
	}
	if ip, e := netip.ParseAddr(u.Hostname()); e == nil && !publicImageIP(ip) {
		return "", fmt.Errorf("imagegen: protected download address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", fmt.Errorf("imagegen: invalid download request")
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("imagegen: download transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("imagegen: download HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxDownloadedImageBytes {
		return "", fmt.Errorf("imagegen: image exceeds 50 MiB limit")
	}
	f, err := os.CreateTemp("", prefix+"-*"+ext)
	if err != nil {
		return "", fmt.Errorf("imagegen: create download file: %w", err)
	}
	name := f.Name()
	success := false
	defer func() {
		_ = f.Close()
		if !success {
			_ = os.Remove(name)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadedImageBytes+1))
	if err != nil {
		return "", fmt.Errorf("imagegen: download body failed")
	}
	if n > maxDownloadedImageBytes {
		return "", fmt.Errorf("imagegen: image exceeds 50 MiB limit")
	}
	if err = f.Close(); err != nil {
		return "", fmt.Errorf("imagegen: close download file: %w", err)
	}
	success = true
	return name, nil
}
