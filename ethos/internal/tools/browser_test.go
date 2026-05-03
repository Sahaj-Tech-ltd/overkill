package tools

import (
	"testing"
)

func TestBrowserHostPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		policy  BrowserHostPolicy
		url     string
		wantErr bool
	}{
		// Default deny on SSRF-prone hosts.
		{"localhost blocked by default", BrowserHostPolicy{}, "http://localhost:8080/x", true},
		{"127.0.0.1 blocked by default", BrowserHostPolicy{}, "http://127.0.0.1/", true},
		{"metadata endpoint blocked", BrowserHostPolicy{}, "http://169.254.169.254/latest/meta-data", true},
		{"public host allowed when no allowlist", BrowserHostPolicy{}, "https://example.com/", false},

		// Allowlist enforcement.
		{"allowlist hit", BrowserHostPolicy{Allowed: []string{"example.com"}}, "https://example.com/x", false},
		{"allowlist miss", BrowserHostPolicy{Allowed: []string{"example.com"}}, "https://other.com/x", true},
		{"allowlist wildcard hit", BrowserHostPolicy{Allowed: []string{".example.com"}}, "https://api.example.com/x", false},

		// User-supplied blocklist on top of defaults.
		{"user blocked host", BrowserHostPolicy{Blocked: []string{"banned.test"}}, "https://banned.test/x", true},

		// Blocklist takes precedence inside the user list.
		{"blocklist beats allowlist", BrowserHostPolicy{Allowed: []string{"banned.test"}, Blocked: []string{"banned.test"}}, "https://banned.test/x", true},

		// Schemes.
		{"javascript scheme blocked", BrowserHostPolicy{}, "javascript:alert(1)", true},
		{"chrome scheme blocked", BrowserHostPolicy{}, "chrome://flags", true},
		{"data scheme blocked", BrowserHostPolicy{}, "data:text/html,hi", true},
		{"unknown scheme blocked", BrowserHostPolicy{}, "gopher://example.com/", true},
		{"file scheme allowed", BrowserHostPolicy{}, "file:///etc/hosts", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.CheckURL(tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for url=%q got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for url=%q: %v", tc.url, err)
			}
		})
	}
}

func TestClassifyBrowserURLRisk(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url  string
		want string
	}{
		{"https://example.com", "low"},
		{"http://example.com", "low"},
		{"file:///etc/passwd", "medium"},
		{"ftp://example.com", "medium"},
		{"javascript:alert(1)", "high"},
		{"chrome://flags", "high"},
		{"gopher://x/", "medium"},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			if got := ClassifyBrowserURLRisk(tc.url); got != tc.want {
				t.Errorf("ClassifyBrowserURLRisk(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

func TestBrowserToolNames(t *testing.T) {
	t.Parallel()
	// Verify each tool reports the expected name. These are stable contract
	// strings consumed by the agent's risk classifier.
	wants := map[string]string{
		"browser_open":       (&BrowserOpenTool{}).Name(),
		"browser_navigate":   (&BrowserNavigateTool{}).Name(),
		"browser_screenshot": (&BrowserScreenshotTool{}).Name(),
		"browser_text":       (&BrowserTextTool{}).Name(),
		"browser_markdown":   (&BrowserMarkdownTool{}).Name(),
		"browser_click":      (&BrowserClickTool{}).Name(),
		"browser_fill":       (&BrowserFillTool{}).Name(),
		"browser_select":     (&BrowserSelectTool{}).Name(),
		"browser_eval":       (&BrowserEvalTool{}).Name(),
		"browser_wait":       (&BrowserWaitTool{}).Name(),
	}
	for want, got := range wants {
		if got != want {
			t.Errorf("tool name mismatch: got %q want %q", got, want)
		}
	}
}
