package imagegen

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageDownloadPinsDNSAndPreservesTLS(t *testing.T) {
	var hits int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://127.0.0.1/private", 302)
			return
		}
		_, _ = io.WriteString(w, "image fixture")
	}))
	defer server.Close()
	var addresses []string
	transport := imageDownloadTransport(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		addresses = append(addresses, address)
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	transport.TLSClientConfig.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	path, err := downloadImageWithClient(t.Context(), "https://example.com/image", "test-image", ".png", client)
	require.NoError(t, err)
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "image fixture", string(data))
	require.Equal(t, []string{"8.8.8.8:443"}, addresses)
	_, err = downloadImageWithClient(t.Context(), "https://example.com/redirect", "test-image", ".png", client)
	require.ErrorContains(t, err, "HTTP 302")
	require.Equal(t, 2, hits)
	_, err = downloadImageWithClient(t.Context(), "https://wrong-host.example/image", "test-image", ".png", client)
	require.Error(t, err)
	require.Equal(t, 2, hits, "TLS hostname rejection occurs before HTTP request")
}

func TestImageDownloadRejectsDNSRebindingAndMixedAnswers(t *testing.T) {
	for _, answers := range [][]string{{"127.0.0.1"}, {"8.8.8.8", "10.0.0.1"}, {"::ffff:127.0.0.1"}, {"100.64.0.1"}, {"169.254.169.254"}, {"2001:db8::1"}} {
		t.Run(strings.Join(answers, ","), func(t *testing.T) {
			calls := 0
			dialed := 0
			transport := imageDownloadTransport(func(context.Context, string, string) ([]netip.Addr, error) {
				calls++
				if calls == 1 {
					return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
				}
				ips := []netip.Addr{}
				for _, a := range answers {
					ips = append(ips, netip.MustParseAddr(a))
				}
				return ips, nil
			}, func(context.Context, string, string) (net.Conn, error) {
				dialed++
				return nil, errors.New("fixture dial boundary")
			})
			_, err := transport.DialContext(t.Context(), "tcp", "images.example:443")
			require.ErrorContains(t, err, "fixture dial boundary")
			_, err = transport.DialContext(t.Context(), "tcp", "images.example:443")
			require.ErrorContains(t, err, "protected")
			require.Equal(t, 1, dialed)
		})
	}
}

type imageRoundTripper func(*http.Request) (*http.Response, error)

func (f imageRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type imageZeros struct{}

func (imageZeros) Read(b []byte) (int, error) { clear(b); return len(b), nil }

type imageBrokenBody struct{}

func (imageBrokenBody) Read(b []byte) (int, error) { copy(b, "partial"); return 7, io.ErrUnexpectedEOF }

func TestImageDownloadBoundsPrivateFilesAndCleansFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		length    int64
		body      io.Reader
		wantError bool
	}{
		{"success", 4, strings.NewReader("data"), false},
		{"declared too large", maxDownloadedImageBytes + 1, strings.NewReader(""), true},
		{"chunked too large", -1, io.LimitReader(imageZeros{}, maxDownloadedImageBytes+1), true},
		{"partial body", -1, imageBrokenBody{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, key := range []string{"TMPDIR", "TEMP", "TMP"} {
				t.Setenv(key, dir)
			}
			client := &http.Client{Transport: imageRoundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, ContentLength: tc.length, Body: io.NopCloser(tc.body), Header: make(http.Header)}, nil
			})}
			path, err := downloadImageWithClient(t.Context(), "https://images.example/file?signature=fixture", "test-image", ".png", client)
			if tc.wantError {
				require.Error(t, err)
				require.Empty(t, path)
			} else {
				require.NoError(t, err)
				info, err := os.Stat(path)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0600), info.Mode().Perm())
				require.NoError(t, os.Remove(path))
			}
			files, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestImageDownloadRejectsUnsafeURLsBeforeTransport(t *testing.T) {
	client := &http.Client{Transport: imageRoundTripper(func(*http.Request) (*http.Response, error) { t.Fatal("unsafe URL reached transport"); return nil, nil })}
	for _, address := range []string{"http://example.com/img", "https://user:secret@example.com/img", "https://example.com:8443/img", "https://example.com/img#fragment", "file:///etc/passwd", "https://127.0.0.1/img", "https://[::1]/img", "https://169.254.169.254/img"} {
		_, err := downloadImageWithClient(t.Context(), address, "test-image", ".png", client)
		require.Error(t, err)
	}
}
