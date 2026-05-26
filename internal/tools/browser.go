package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/browser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls/promptinject"
)

// annotateInjection attaches a prompt-injection summary to a tool-result
// payload when the rendered content trips the promptinject scanner. The
// scanner was written but never invoked anywhere — every browser fetch
// returned raw HTML/markdown to the model with no flag. We keep the
// content (the agent may need to see "the page that tried to override
// instructions") but add a structured warning so the agent + transcripts
// can react.
func annotateInjection(payload map[string]any, body string) map[string]any {
	findings := promptinject.Scan(body)
	if len(findings) == 0 {
		return payload
	}
	summaries := make([]map[string]any, 0, len(findings))
	for _, f := range findings {
		summaries = append(summaries, map[string]any{
			"severity": string(f.Severity),
			"pattern":  f.Pattern,
			"match":    f.Match,
		})
	}
	payload["_prompt_injection_warning"] = map[string]any{
		"max_severity": string(promptinject.MaxSeverity(findings)),
		"findings":     summaries,
	}
	return payload
}

// BrowserHostPolicy decides whether a given URL is reachable. Allowed hosts
// take precedence over blocked hosts only when the allow list is non-empty.
type BrowserHostPolicy struct {
	Allowed []string
	Blocked []string
}

// DefaultBlockedHosts is the conservative SSRF blocklist applied on top of
// any user-configured blocklist. Bare host strings, CIDR ranges, or the
// special tokens `localhost` / `metadata` are all valid entries.
var DefaultBlockedHosts = []string{
	"localhost",
	"127.0.0.1",
	"0.0.0.0",
	"::1",
	"169.254.0.0/16",
}

// CheckURL validates a URL against this policy. Returns nil when allowed,
// an error describing the violation otherwise.
func (p BrowserHostPolicy) CheckURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("browser: invalid url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "javascript" || scheme == "chrome" || scheme == "data" {
		return fmt.Errorf("browser: scheme %q not allowed", scheme)
	}
	if scheme != "http" && scheme != "https" && scheme != "about" {
		return fmt.Errorf("browser: scheme %q not allowed", scheme)
	}
	host := u.Hostname()
	if host == "" {
		// about:blank etc — allow.
		return nil
	}

	// Blocklist (default + user-configured).
	for _, b := range append(append([]string{}, DefaultBlockedHosts...), p.Blocked...) {
		if hostMatches(host, b) {
			return fmt.Errorf("browser: host %q is blocked (%s)", host, b)
		}
	}
	// Allowlist (only when non-empty).
	if len(p.Allowed) > 0 {
		for _, a := range p.Allowed {
			if hostMatches(host, a) {
				return nil
			}
		}
		return fmt.Errorf("browser: host %q not in allowlist", host)
	}
	return nil
}

// hostMatches returns true when host satisfies pattern. Patterns may be:
//   - exact hostname ("example.com")
//   - CIDR ("169.254.0.0/16")
//   - leading dot wildcard (".example.com" matches "a.example.com")
func hostMatches(host, pattern string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	if strings.Contains(pattern, "/") {
		_, cidr, err := net.ParseCIDR(pattern)
		if err == nil {
			if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(pattern, ".") {
		return strings.HasSuffix(host, pattern) || host == pattern[1:]
	}
	return host == pattern
}

// ---------------------------------------------------------------------------
// Tool wiring
// ---------------------------------------------------------------------------

const browserScreenshotMaxBytes = 5 * 1024 * 1024

type browserToolBase struct {
	mgr    *browser.Manager
	policy BrowserHostPolicy
}

func (t *browserToolBase) ensureOpen(ctx context.Context) (*browser.Browser, error) {
	return t.mgr.Get(ctx)
}

// BrowserOpenTool — navigate to a URL.
type BrowserOpenTool struct{ browserToolBase }

func NewBrowserOpenTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserOpenTool {
	return &BrowserOpenTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserOpenTool) Name() string { return "browser_open" }
func (t *BrowserOpenTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, fmt.Errorf("browser_open: %w", err)
	}
	if args.URL == "" {
		return nil, fmt.Errorf("browser_open: url is required")
	}
	if err := t.policy.CheckURL(args.URL); err != nil {
		return nil, err
	}
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	if err := b.Navigate(args.URL); err != nil {
		return nil, err
	}
	title, _ := b.Title()
	cur, _ := b.URL()
	return json.Marshal(map[string]any{"title": title, "url": cur})
}

// BrowserNavigateTool — alias of open with explicit risk semantics per scheme.
type BrowserNavigateTool struct{ browserToolBase }

func NewBrowserNavigateTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserNavigateTool {
	return &BrowserNavigateTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserNavigateTool) Name() string { return "browser_navigate" }
func (t *BrowserNavigateTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, fmt.Errorf("browser_navigate: %w", err)
	}
	if args.URL == "" {
		return nil, fmt.Errorf("browser_navigate: url is required")
	}
	if err := t.policy.CheckURL(args.URL); err != nil {
		return nil, err
	}
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	if err := b.Navigate(args.URL); err != nil {
		return nil, err
	}
	title, _ := b.Title()
	cur, _ := b.URL()
	return json.Marshal(map[string]any{"title": title, "url": cur})
}

// BrowserScreenshotTool — PNG bytes (base64-encoded).
type BrowserScreenshotTool struct{ browserToolBase }

func NewBrowserScreenshotTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserScreenshotTool {
	return &BrowserScreenshotTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserScreenshotTool) Name() string { return "browser_screenshot" }
func (t *BrowserScreenshotTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		Selector string `json:"selector"`
	}
	_ = json.Unmarshal(in, &args)
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	var png []byte
	if args.Selector != "" {
		png, err = b.ScreenshotElement(args.Selector)
	} else {
		png, err = b.Screenshot(args.Width, args.Height)
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"width":  args.Width,
		"height": args.Height,
	}
	if len(png) > browserScreenshotMaxBytes {
		png = png[:browserScreenshotMaxBytes]
		out["truncated"] = true
		out["note"] = "screenshot truncated to 5MB"
	}
	out["base64_png"] = base64.StdEncoding.EncodeToString(png)
	return json.Marshal(out)
}

// BrowserTextTool — extract visible text by selector.
type BrowserTextTool struct{ browserToolBase }

func NewBrowserTextTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserTextTool {
	return &BrowserTextTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserTextTool) Name() string { return "browser_text" }
func (t *BrowserTextTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Selector string `json:"selector"`
	}
	_ = json.Unmarshal(in, &args)
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	s, err := b.Text(args.Selector)
	if err != nil {
		return nil, err
	}
	return json.Marshal(annotateInjection(map[string]any{"text": s}, s))
}

// BrowserMarkdownTool — render the current page as markdown.
type BrowserMarkdownTool struct{ browserToolBase }

func NewBrowserMarkdownTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserMarkdownTool {
	return &BrowserMarkdownTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserMarkdownTool) Name() string { return "browser_markdown" }
func (t *BrowserMarkdownTool) Execute(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	md, err := b.Markdown()
	if err != nil {
		return nil, err
	}
	return json.Marshal(annotateInjection(map[string]any{"markdown": md}, md))
}

// BrowserClickTool — click element.
type BrowserClickTool struct{ browserToolBase }

func NewBrowserClickTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserClickTool {
	return &BrowserClickTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserClickTool) Name() string { return "browser_click" }
func (t *BrowserClickTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Selector string `json:"selector"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, fmt.Errorf("browser_click: %w", err)
	}
	if args.Selector == "" {
		return nil, fmt.Errorf("browser_click: selector required")
	}
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	before, _ := b.URL()
	if err := b.Click(args.Selector); err != nil {
		return nil, err
	}
	after, _ := b.URL()
	out := map[string]any{"ok": true}
	if after != before {
		out["new_url"] = after
	}
	return json.Marshal(out)
}

// BrowserFillTool — type text into an input.
type BrowserFillTool struct{ browserToolBase }

func NewBrowserFillTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserFillTool {
	return &BrowserFillTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserFillTool) Name() string { return "browser_fill" }
func (t *BrowserFillTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Selector string `json:"selector"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, fmt.Errorf("browser_fill: %w", err)
	}
	if args.Selector == "" {
		return nil, fmt.Errorf("browser_fill: selector required")
	}
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	if err := b.Fill(args.Selector, args.Value); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true})
}

// BrowserSelectTool — pick a value from a <select>.
type BrowserSelectTool struct{ browserToolBase }

func NewBrowserSelectTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserSelectTool {
	return &BrowserSelectTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserSelectTool) Name() string { return "browser_select" }
func (t *BrowserSelectTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Selector string `json:"selector"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, fmt.Errorf("browser_select: %w", err)
	}
	if args.Selector == "" {
		return nil, fmt.Errorf("browser_select: selector required")
	}
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	if err := b.Select(args.Selector, args.Value); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true})
}

// BrowserEvalTool — run arbitrary JS. Always classified high-risk.
type BrowserEvalTool struct{ browserToolBase }

func NewBrowserEvalTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserEvalTool {
	return &BrowserEvalTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserEvalTool) Name() string { return "browser_eval" }
func (t *BrowserEvalTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		JS string `json:"js"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, fmt.Errorf("browser_eval: %w", err)
	}
	if args.JS == "" {
		return nil, fmt.Errorf("browser_eval: js required")
	}
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	v, err := b.Eval(args.JS)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"result": v})
}

// BrowserWaitTool — wait for selector to appear.
type BrowserWaitTool struct{ browserToolBase }

func NewBrowserWaitTool(mgr *browser.Manager, policy BrowserHostPolicy) *BrowserWaitTool {
	return &BrowserWaitTool{browserToolBase{mgr: mgr, policy: policy}}
}
func (t *BrowserWaitTool) Name() string { return "browser_wait" }
func (t *BrowserWaitTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Selector  string `json:"selector"`
		TimeoutMs int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, fmt.Errorf("browser_wait: %w", err)
	}
	if args.Selector == "" {
		return nil, fmt.Errorf("browser_wait: selector required")
	}
	b, err := t.ensureOpen(ctx)
	if err != nil {
		return nil, err
	}
	timeout := 30 * time.Second
	if args.TimeoutMs > 0 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}
	if err := b.WaitForSelector(args.Selector, timeout); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true})
}

// ClassifyBrowserURLRisk returns the recommended risk level for navigating to
// a given URL. Public so internal/agent can call it from classifyToolRisk.
func ClassifyBrowserURLRisk(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "medium"
	}
	switch strings.ToLower(u.Scheme) {
	case "javascript", "chrome":
		return "high"
	case "file", "ftp":
		return "medium"
	case "http", "https":
		return "low"
	}
	return "medium"
}
