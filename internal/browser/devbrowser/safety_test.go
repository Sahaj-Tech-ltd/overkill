package devbrowser

import (
	"net"
	"strings"
	"testing"
)

func TestValidateURL_HTTPSPasses(t *testing.T) {
	u, err := validateURL("https://example.com/path?q=1")
	if err != nil {
		t.Fatalf("https URL should pass: %v", err)
	}
	if u.Host != "example.com" {
		t.Errorf("host: %s", u.Host)
	}
}

func TestValidateURL_HTTPPasses(t *testing.T) {
	if _, err := validateURL("http://example.com"); err != nil {
		t.Errorf("plain http should pass: %v", err)
	}
}

func TestValidateURL_BlocksFileScheme(t *testing.T) {
	_, err := validateURL("file:///etc/passwd")
	if err == nil {
		t.Error("file:// must be rejected (would read local files)")
	}
}

func TestValidateURL_BlocksJavascriptScheme(t *testing.T) {
	_, err := validateURL("javascript:alert(1)")
	if err == nil {
		t.Error("javascript: must be rejected")
	}
}

func TestValidateURL_BlocksDataScheme(t *testing.T) {
	_, err := validateURL("data:text/html,<script>x</script>")
	if err == nil {
		t.Error("data: must be rejected")
	}
}

func TestValidateURL_BlocksLocalhost(t *testing.T) {
	_, err := validateURL("http://localhost:8080/admin")
	if err == nil {
		t.Error("localhost must be rejected (SSRF to dev servers)")
	}
}

func TestValidateURL_BlocksLocalhostSubdomain(t *testing.T) {
	_, err := validateURL("http://foo.localhost/")
	if err == nil {
		t.Error("*.localhost must be rejected")
	}
}

func TestValidateURL_BlocksCloudMetadata(t *testing.T) {
	cases := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
	}
	for _, raw := range cases {
		if _, err := validateURL(raw); err == nil {
			t.Errorf("cloud-metadata endpoint must be rejected: %s", raw)
		}
	}
}

func TestValidateURL_BlocksPrivateIPv4(t *testing.T) {
	cases := []string{
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
		"http://127.0.0.1/",
		"http://127.1.2.3/", // 127/8 — still loopback
	}
	for _, raw := range cases {
		if _, err := validateURL(raw); err == nil {
			t.Errorf("private IPv4 must be rejected: %s", raw)
		}
	}
}

func TestValidateURL_BlocksLinkLocal(t *testing.T) {
	if _, err := validateURL("http://169.254.0.5/"); err == nil {
		t.Error("link-local must be rejected")
	}
}

func TestValidateURL_BlocksUnspecified(t *testing.T) {
	if _, err := validateURL("http://0.0.0.0/"); err == nil {
		t.Error("0.0.0.0 must be rejected")
	}
}

func TestValidateURL_EmptyErrors(t *testing.T) {
	if _, err := validateURL(""); err == nil {
		t.Error("empty URL must error")
	}
}

func TestValidateURL_MissingHostErrors(t *testing.T) {
	if _, err := validateURL("http:///path"); err == nil {
		t.Error("URL with no host must error")
	}
}

func TestValidateURL_TrimsWhitespace(t *testing.T) {
	u, err := validateURL("  https://example.com  ")
	if err != nil {
		t.Errorf("leading/trailing whitespace should trim, got %v", err)
	}
	if u != nil && u.Host != "example.com" {
		t.Errorf("host: %s", u.Host)
	}
}

func TestValidateURL_ErrorMentionsReason(t *testing.T) {
	_, err := validateURL("file:///etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Errorf("error should mention scheme: %v", err)
	}
	_, err = validateURL("http://192.168.1.1/")
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Errorf("error should mention private: %v", err)
	}
}

func TestIsPrivateOrSpecialIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.1.2.3", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.0.1", true}, // link-local
		{"169.254.169.254", true}, // cloud metadata
		{"0.0.0.0", true},
		{"::1", true},
		{"fe80::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false}, // example.com
	}
	for _, c := range tests {
		ip := net.ParseIP(c.ip)
		got := isPrivateOrSpecialIP(ip)
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.ip, got, c.want)
		}
	}
}

func TestIsBlockedHostname(t *testing.T) {
	if !isBlockedHostname("localhost") {
		t.Error("localhost")
	}
	if !isBlockedHostname("foo.localhost") {
		t.Error("foo.localhost")
	}
	if !isBlockedHostname("metadata.google.internal") {
		t.Error("gcp metadata")
	}
	if isBlockedHostname("example.com") {
		t.Error("public host should not be blocked")
	}
	// Case-insensitive
	if !isBlockedHostname("LOCALHOST") {
		t.Error("LOCALHOST")
	}
}
