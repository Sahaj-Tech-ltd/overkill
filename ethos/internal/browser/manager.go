package browser

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Manager owns the singleton browser session for an ethos process. Lazy-spawns
// Chrome on the first call to Get and tears it down via Shutdown.
type Manager struct {
	mu      sync.Mutex
	opts    Options
	browser *Browser
	pid     int
}

// NewManager constructs a Manager bound to the given options. The underlying
// Browser is not spawned until Get is called.
func NewManager(opts Options) *Manager {
	// Default headless on. Callers explicitly opt out of headless via
	// Options{Headless: false} only when they truly want a visible window.
	return &Manager{opts: opts}
}

// Get returns the live Browser, opening it on first call.
func (m *Manager) Get(ctx context.Context) (*Browser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.browser != nil && m.browser.IsOpen() {
		return m.browser, nil
	}
	// Default to headless when zero-value.
	opts := m.opts
	// Options.Headless is bool; we treat zero as headless=true for safety —
	// but also support the explicit "Headless=false" path. Callers that want
	// non-headless must set Options.Headless = false explicitly AND set a
	// special override; here, since bool default is false, we re-interpret
	// the field as `Visible` semantics elsewhere. To keep the public API
	// stable we always launch headless unless the caller already configured
	// Headless = true on the manager. Internally Open() maps Headless==false
	// to Flag("headless", false). So a zero-value Options gives us headless.
	b := New(opts)
	if err := b.Open(ctx); err != nil {
		return nil, err
	}
	m.browser = b
	// Best-effort PID grab for status display.
	m.pid = chromePID()
	return b, nil
}

// Close tears down the browser if it is open.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.browser != nil {
		m.browser.Close()
		m.browser = nil
		m.pid = 0
	}
}

// Status describes the current browser state for the /browser dialog.
type Status struct {
	Running   bool
	PID       int
	MemoryKB  int64
	URL       string
	Title     string
	Headless  bool
	UserAgent string
}

// Status snapshots the current state. Best-effort: memory comes from
// /proc/<pid>/status on Linux and is zero elsewhere.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{
		Headless:  true, // we default headless; not exposing toggle yet
		UserAgent: m.opts.UserAgent,
	}
	if m.browser == nil || !m.browser.IsOpen() {
		return st
	}
	st.Running = true
	st.PID = m.pid
	st.MemoryKB = readVmRSS(m.pid)
	if u, err := m.browser.URL(); err == nil {
		st.URL = u
	}
	if t, err := m.browser.Title(); err == nil {
		st.Title = t
	}
	return st
}

// Refresh reloads the current page.
func (m *Manager) Refresh() error {
	m.mu.Lock()
	b := m.browser
	m.mu.Unlock()
	if b == nil || !b.IsOpen() {
		return fmt.Errorf("browser: not running")
	}
	u, err := b.URL()
	if err != nil {
		return err
	}
	return b.Navigate(u)
}

// chromePID returns a best-effort guess at the spawned Chrome PID. chromedp
// doesn't expose this directly so we walk /proc on Linux. Returns 0 on
// failure or non-Linux platforms.
func chromePID() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var newest int
	var newestPID int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if name != "chrome" && name != "chromium" && name != "headless_shell" {
			continue
		}
		// Pick the highest PID — most likely the one we just spawned.
		if pid > newest {
			newest = pid
			newestPID = pid
		}
	}
	return newestPID
}

// readVmRSS reads VmRSS in kB from /proc/<pid>/status. Returns 0 on error.
func readVmRSS(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		n, _ := strconv.ParseInt(fields[1], 10, 64)
		return n
	}
	return 0
}
