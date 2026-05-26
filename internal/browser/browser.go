// Package browser provides an agent-driven headless Chrome wrapper built on
// chromedp. A single Browser holds one chromedp session against a long-lived
// Chrome process; tools call into it through the Manager singleton.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// Options configures the browser process. An empty Options is valid and uses
// chromedp defaults (headless, auto-detected Chrome path).
type Options struct {
	Headless   bool
	ChromePath string
	UserAgent  string
}

// Browser wraps a chromedp session. Methods are safe to call from a single
// goroutine; the underlying chromedp context serializes navigation and DOM
// access. Use Manager when you need cross-tool coordination.
type Browser struct {
	mu         sync.Mutex
	allocCtx   context.Context
	allocClose context.CancelFunc
	ctx        context.Context
	ctxClose   context.CancelFunc
	opts       Options
	open       bool
}

// New constructs a Browser with the given options. Options.Headless defaults
// to true when zero-value (Open will set it explicitly).
func New(opts Options) *Browser {
	return &Browser{opts: opts}
}

// Open spawns the underlying Chrome process and waits until it is ready. It
// is safe to call Open more than once; subsequent calls are no-ops.
func (b *Browser) Open(parent context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open {
		return nil
	}

	allocOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if !b.opts.Headless {
		// Strip the headless flag from the default set.
		filtered := allocOpts[:0]
		for _, o := range allocOpts {
			// chromedp doesn't expose a simple "is headless flag" API, so just
			// override below with Flag("headless", false).
			filtered = append(filtered, o)
		}
		allocOpts = append(filtered, chromedp.Flag("headless", false))
	}
	if b.opts.ChromePath != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(b.opts.ChromePath))
	}
	if b.opts.UserAgent != "" {
		allocOpts = append(allocOpts, chromedp.UserAgent(b.opts.UserAgent))
	}
	// Disable GPU + sandbox combos that bite on headless Linux servers.
	allocOpts = append(allocOpts,
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)

	// Force a Run() so Chrome actually launches before we return.
	startCtx, startCancel := context.WithTimeout(browserCtx, 30*time.Second)
	defer startCancel()
	if err := chromedp.Run(startCtx); err != nil {
		browserCancel()
		allocCancel()
		return fmt.Errorf("browser: launch chrome: %w", err)
	}

	b.allocCtx = allocCtx
	b.allocClose = allocCancel
	b.ctx = browserCtx
	b.ctxClose = browserCancel
	b.open = true
	_ = parent // reserved for future signal-aware shutdown
	return nil
}

// Close tears down the chromedp session and the Chrome process. Idempotent.
func (b *Browser) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		return
	}
	if b.ctxClose != nil {
		b.ctxClose()
	}
	if b.allocClose != nil {
		b.allocClose()
	}
	b.open = false
}

// IsOpen reports whether the underlying browser is currently spawned.
func (b *Browser) IsOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

// runTimeout wraps the configured timeout around chromedp Run.
func (b *Browser) runTimeout(timeout time.Duration, actions ...chromedp.Action) error {
	if !b.open {
		return fmt.Errorf("browser: not open")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(b.ctx, timeout)
	defer cancel()
	return chromedp.Run(ctx, actions...)
}

// Navigate loads the given URL. Waits for ready state.
func (b *Browser) Navigate(url string) error {
	if err := b.runTimeout(30*time.Second, chromedp.Navigate(url), chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
		return fmt.Errorf("browser: navigate %q: %w", url, err)
	}
	return nil
}

// Screenshot captures a PNG of the current viewport. width/height ≤0 use the
// chromedp default viewport.
func (b *Browser) Screenshot(width, height int) ([]byte, error) {
	var buf []byte
	actions := []chromedp.Action{}
	if width > 0 && height > 0 {
		actions = append(actions, emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1, false))
	}
	actions = append(actions, chromedp.CaptureScreenshot(&buf))
	if err := b.runTimeout(30*time.Second, actions...); err != nil {
		return nil, fmt.Errorf("browser: screenshot: %w", err)
	}
	return buf, nil
}

// ScreenshotElement captures a PNG of a single CSS-selected element.
func (b *Browser) ScreenshotElement(selector string) ([]byte, error) {
	var buf []byte
	if err := b.runTimeout(30*time.Second, chromedp.Screenshot(selector, &buf, chromedp.ByQuery)); err != nil {
		return nil, fmt.Errorf("browser: screenshot %q: %w", selector, err)
	}
	return buf, nil
}

// Title returns the current page title.
func (b *Browser) Title() (string, error) {
	var t string
	if err := b.runTimeout(10*time.Second, chromedp.Title(&t)); err != nil {
		return "", fmt.Errorf("browser: title: %w", err)
	}
	return t, nil
}

// URL returns the current page URL.
func (b *Browser) URL() (string, error) {
	var u string
	if err := b.runTimeout(10*time.Second, chromedp.Location(&u)); err != nil {
		return "", fmt.Errorf("browser: url: %w", err)
	}
	return u, nil
}

// Text extracts visible text from the first element matching selector. An
// empty selector defaults to "body".
func (b *Browser) Text(selector string) (string, error) {
	if selector == "" {
		selector = "body"
	}
	var s string
	if err := b.runTimeout(10*time.Second, chromedp.Text(selector, &s, chromedp.ByQuery, chromedp.NodeVisible)); err != nil {
		return "", fmt.Errorf("browser: text %q: %w", selector, err)
	}
	return s, nil
}

// Click clicks the first element matching selector.
func (b *Browser) Click(selector string) error {
	if err := b.runTimeout(15*time.Second, chromedp.Click(selector, chromedp.ByQuery)); err != nil {
		return fmt.Errorf("browser: click %q: %w", selector, err)
	}
	return nil
}

// Fill clears and types value into the first element matching selector.
func (b *Browser) Fill(selector, value string) error {
	if err := b.runTimeout(15*time.Second, chromedp.Clear(selector, chromedp.ByQuery), chromedp.SendKeys(selector, value, chromedp.ByQuery)); err != nil {
		return fmt.Errorf("browser: fill %q: %w", selector, err)
	}
	return nil
}

// Select picks value from a <select> element.
func (b *Browser) Select(selector, value string) error {
	if err := b.runTimeout(10*time.Second, chromedp.SetValue(selector, value, chromedp.ByQuery)); err != nil {
		return fmt.Errorf("browser: select %q: %w", selector, err)
	}
	return nil
}

// Eval runs an arbitrary JS expression and decodes the result via JSON.
func (b *Browser) Eval(jsExpr string) (any, error) {
	var raw json.RawMessage
	if err := b.runTimeout(15*time.Second, chromedp.Evaluate(jsExpr, &raw)); err != nil {
		return nil, fmt.Errorf("browser: eval: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Non-JSON-serialisable value (e.g. undefined). Return as raw string.
		return string(raw), nil
	}
	return v, nil
}

// WaitForSelector waits for the given selector to appear in the DOM.
func (b *Browser) WaitForSelector(selector string, timeout time.Duration) error {
	if err := b.runTimeout(timeout, chromedp.WaitVisible(selector, chromedp.ByQuery)); err != nil {
		return fmt.Errorf("browser: wait %q: %w", selector, err)
	}
	return nil
}

// WaitForNavigation waits until the body of the next page is ready. Useful
// after triggering a click that initiates navigation.
func (b *Browser) WaitForNavigation(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return b.runTimeout(timeout, chromedp.WaitReady("body", chromedp.ByQuery))
}

// OuterHTML returns the outerHTML of the first element matching selector.
func (b *Browser) OuterHTML(selector string) (string, error) {
	if selector == "" {
		selector = "html"
	}
	var html string
	if err := b.runTimeout(10*time.Second, chromedp.ActionFunc(func(ctx context.Context) error {
		nodes, err := dom.GetDocument().Do(ctx)
		if err != nil {
			return err
		}
		html, err = dom.GetOuterHTML().WithBackendNodeID(nodes.BackendNodeID).Do(ctx)
		return err
	})); err != nil {
		return "", fmt.Errorf("browser: outerhtml: %w", err)
	}
	// Then narrow to selector via JS for accuracy.
	var sel string
	if err := b.runTimeout(10*time.Second, chromedp.Evaluate(fmt.Sprintf(`(() => { const e = document.querySelector(%q); return e ? e.outerHTML : ""; })()`, selector), &sel)); err == nil && sel != "" {
		return sel, nil
	}
	return html, nil
}

// Markdown extracts the main content of the page and returns a tiny markdown
// rendering. Heuristic: prefer <main> > <article> > <body>.
func (b *Browser) Markdown() (string, error) {
	js := `(() => {
		const root = document.querySelector('main') || document.querySelector('article') || document.body;
		if (!root) return '';
		const out = [];
		const walk = (node) => {
			if (!node) return;
			if (node.nodeType === 3) { out.push(node.textContent); return; }
			if (node.nodeType !== 1) return;
			const tag = node.tagName.toLowerCase();
			if (['script','style','noscript','iframe'].includes(tag)) return;
			if (/^h[1-6]$/.test(tag)) {
				const n = parseInt(tag[1],10);
				out.push('\n\n' + '#'.repeat(n) + ' ' + node.textContent.trim() + '\n\n');
				return;
			}
			if (tag === 'a') {
				const href = node.getAttribute('href') || '';
				out.push('[' + node.textContent.trim() + '](' + href + ')');
				return;
			}
			if (tag === 'li') { out.push('\n- '); for (const c of node.childNodes) walk(c); return; }
			if (tag === 'p' || tag === 'div' || tag === 'section') {
				for (const c of node.childNodes) walk(c);
				out.push('\n\n');
				return;
			}
			if (tag === 'br') { out.push('\n'); return; }
			if (tag === 'code' || tag === 'pre') { out.push(String.fromCharCode(96) + node.textContent + String.fromCharCode(96)); return; }
			if (tag === 'strong' || tag === 'b') { out.push('**' + node.textContent.trim() + '**'); return; }
			if (tag === 'em' || tag === 'i') { out.push('*' + node.textContent.trim() + '*'); return; }
			for (const c of node.childNodes) walk(c);
		};
		walk(root);
		return out.join('');
	})()`
	var s string
	if err := b.runTimeout(15*time.Second, chromedp.Evaluate(js, &s)); err != nil {
		return "", fmt.Errorf("browser: markdown: %w", err)
	}
	// Collapse runs of blank lines.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s), nil
}
