// Package devbrowser provides a narrow structured Chrome interface.
//
// Restricted-network browsing is currently unavailable: the default manager
// refuses browser operations before starting Chrome or resolving a destination.
// An application can deliberately opt into trusted-network browsing using
// NewManagerWithOptions. That mode permits local/private network access and
// uses ordinary Chrome networking, including page scripts and redirects. It
// provides no request-time destination isolation or DNS-rebinding protection.
//
// The interface exposes Open, Snapshot, Click and Type, with structured results
// and operation timeouts. A narrow operation set is not a network sandbox.
package devbrowser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// Page is one named tab the agent has open. We hold the
// chromedp.Context that drives its tab and a mutex so two concurrent
// tool calls on the same page serialize cleanly (chromedp itself
// doesn't serialize ops on a single context).
type Page struct {
	Name     string
	Created  time.Time
	LastUsed time.Time

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
}

// Manager owns the parent chromedp context (the actual browser
// process) and a map of named Pages. Lazy-spawn: the browser only
// starts when the first Open call hits.
type Manager struct {
	mu sync.Mutex

	parent       context.Context
	parentCancel context.CancelFunc

	// Immutable application policy; not an agent-controlled operation argument.
	trustedNetwork bool
	pages          map[string]*Page
	// PageTimeout caps how long any single op (Open, Snapshot,
	// Click, Type) can take. Default 30s.
	PageTimeout time.Duration
	// IdleTimeout closes pages that haven't been touched in this
	// long. Default 30min. 0 disables.
	IdleTimeout time.Duration
}

// NewManager constructs the default restricted-network manager. Restricted
// browsing is unavailable, so browser operations fail closed without traffic.
func NewManager() *Manager { return NewManagerWithOptions(Options{}) }

// NewManagerWithOptions accepts explicit application-owned network policy.
// TrustedNetwork permits local/private access; it is not an isolation bypass
// that an agent can request through Open, Click, Type or Snapshot arguments.
func NewManagerWithOptions(options Options) *Manager {
	return &Manager{
		pages:          map[string]*Page{},
		trustedNetwork: options.TrustedNetwork,
		PageTimeout:    30 * time.Second,
		IdleTimeout:    30 * time.Minute,
	}
}

// Shutdown closes every open page + the parent browser. Idempotent.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pages {
		p.cancel()
	}
	m.pages = map[string]*Page{}
	if m.parentCancel != nil {
		m.parentCancel()
		m.parent = nil
		m.parentCancel = nil
	}
}

// ensureBrowser lazy-starts the underlying chromedp browser. Returns
// the parent context for tab creation.
func (m *Manager) ensureBrowser() (context.Context, error) {
	if err := m.checkNetworkMode(); err != nil {
		return nil, err
	}
	if m.parent != nil {
		return m.parent, nil
	}
	// Background context — chromedp wants a "browser parent" that
	// outlives any single page. We track the cancel so Shutdown can
	// tear the whole thing down.
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", false), // keep Chrome's sandbox ON
		)...)

	bctx, bcancel := chromedp.NewContext(allocCtx)
	// Touch the browser to spawn it now (otherwise the first Open
	// pays the spawn cost in its budget).
	if err := chromedp.Run(bctx); err != nil {
		allocCancel()
		bcancel()
		return nil, fmt.Errorf("dev-browser: spawn chrome: %w", err)
	}
	m.parent = bctx
	m.parentCancel = func() {
		bcancel()
		allocCancel()
	}
	return m.parent, nil
}

// Open navigates a named page to a URL. Creates the page on first
// call; reuses it on subsequent calls (so "search" → "google.com"
// then "search" → "duckduckgo.com" walks the same tab). URLs are
// checked for http(s) syntax and rejected userinfo. Only explicitly trusted
// networking can open pages; private/local destinations are allowed in that mode.
func (m *Manager) Open(name, rawURL string) (*Snapshot, error) {
	if name == "" {
		return nil, errors.New("dev-browser: page name required")
	}
	if err := m.checkNetworkMode(); err != nil {
		return nil, err
	}
	u, err := parseHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	page := m.pages[name]
	if page == nil {
		parent, err := m.ensureBrowser()
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		// Each named page gets its own tab via NewContext on the
		// browser parent. Cancelling this ctx closes the tab.
		pageCtx, pageCancel := chromedp.NewContext(parent)
		page = &Page{
			Name:    name,
			Created: time.Now().UTC(),
			ctx:     pageCtx,
			cancel:  pageCancel,
		}
		m.pages[name] = page
	}
	m.mu.Unlock()

	if err := m.checkNetworkMode(); err != nil {
		return nil, err
	}
	page.mu.Lock()
	defer page.mu.Unlock()
	page.LastUsed = time.Now().UTC()

	opCtx, cancel := context.WithTimeout(page.ctx, m.PageTimeout)
	defer cancel()
	if err := chromedp.Run(opCtx, chromedp.Navigate(u.String())); err != nil {
		return nil, fmt.Errorf("dev-browser: navigate %s: %w", u.String(), err)
	}
	snap, err := snapshotPage(opCtx)
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// Snapshot returns the current structured view of a named page
// without navigating. Useful after a Click or Type when the page
// has changed.
func (m *Manager) Snapshot(name string) (*Snapshot, error) {
	page, err := m.lookupPage(name)
	if err != nil {
		return nil, err
	}
	if err := m.checkNetworkMode(); err != nil {
		return nil, err
	}
	page.mu.Lock()
	defer page.mu.Unlock()
	page.LastUsed = time.Now().UTC()
	opCtx, cancel := context.WithTimeout(page.ctx, m.PageTimeout)
	defer cancel()
	snap, err := snapshotPage(opCtx)
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// Click clicks the first element matching the CSS selector on the
// named page. Returns the post-click snapshot so the agent sees the
// page state changed.
//
// We expose Click as a separate op (not just "evaluate
// document.querySelector(s).click()") so the agent can't smuggle a
// raw JS expression in.
func (m *Manager) Click(name, selector string) (*Snapshot, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, errors.New("dev-browser: selector required")
	}
	page, err := m.lookupPage(name)
	if err != nil {
		return nil, err
	}
	if err := m.checkNetworkMode(); err != nil {
		return nil, err
	}
	page.mu.Lock()
	defer page.mu.Unlock()
	page.LastUsed = time.Now().UTC()
	opCtx, cancel := context.WithTimeout(page.ctx, m.PageTimeout)
	defer cancel()
	if err := chromedp.Run(opCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("dev-browser: click %q: %w", selector, err)
	}
	snap, err := snapshotPage(opCtx)
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// Type types text into the first input matching the selector. Same
// safety story as Click — separate op, no JS injection vector. We
// clear the field first so successive Type calls don't append on top.
func (m *Manager) Type(name, selector, text string) (*Snapshot, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, errors.New("dev-browser: selector required")
	}
	page, err := m.lookupPage(name)
	if err != nil {
		return nil, err
	}
	if err := m.checkNetworkMode(); err != nil {
		return nil, err
	}
	page.mu.Lock()
	defer page.mu.Unlock()
	page.LastUsed = time.Now().UTC()
	opCtx, cancel := context.WithTimeout(page.ctx, m.PageTimeout)
	defer cancel()
	if err := chromedp.Run(opCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Clear(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, text, chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("dev-browser: type into %q: %w", selector, err)
	}
	snap, err := snapshotPage(opCtx)
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// Close drops a named page. No-op if the page doesn't exist.
func (m *Manager) Close(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pages[name]; ok {
		p.cancel()
		delete(m.pages, name)
	}
}

// ListPages returns the names of currently-open pages. Deterministic
// order (not guaranteed alphabetical — we iterate the map).
func (m *Manager) ListPages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.pages))
	for n := range m.pages {
		names = append(names, n)
	}
	return names
}

func (m *Manager) lookupPage(name string) (*Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pages[name]
	if !ok {
		return nil, fmt.Errorf("dev-browser: unknown page %q (open it first)", name)
	}
	return p, nil
}
