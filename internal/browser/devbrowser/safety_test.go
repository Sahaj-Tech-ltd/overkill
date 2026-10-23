package devbrowser

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTrustedURLSyntax(t *testing.T) {
	for _, raw := range []string{"https://example.com/path?q=1", "http://localhost:8080/login", "http://127.0.0.1/", "http://10.0.0.1/", "http://[::1]:8080/", "  https://example.test  "} {
		t.Run(raw, func(t *testing.T) {
			u, err := parseHTTPURL(raw)
			require.NoError(t, err)
			require.NotEmpty(t, u.Hostname())
		})
	}
}
func TestTrustedURLRejectsUserinfoAndUnsafeSchemes(t *testing.T) {
	for _, raw := range []string{"", "http:///path", "http://user:secret@localhost/", "https://user@example.com/", "file:///etc/passwd", "data:text/html,hello", "javascript:alert(1)", "chrome://settings", "ftp://example.com/", "//example.com/"} {
		t.Run(raw, func(t *testing.T) { _, err := parseHTTPURL(raw); require.Error(t, err) })
	}
}
