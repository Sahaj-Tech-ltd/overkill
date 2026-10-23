//go:build unix

package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestManagerConfiguredLaunchTimeoutAndParentCancellation(t *testing.T) {
	for _, parentCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured-timeout", true: "parent-cancellation"}[parentCanceled], func(t *testing.T) {
			chrome := filepath.Join(t.TempDir(), "controlled-chrome")
			require.NoError(t, os.WriteFile(chrome, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700))
			opts := Options{ChromePath: chrome, Timeout: 80 * time.Millisecond}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if parentCanceled {
				opts.Timeout = 5 * time.Second
				cancel()
			}
			manager := NewManager(opts)
			defer manager.Close()
			start := time.Now()
			_, err := manager.Get(ctx)
			require.Error(t, err)
			if parentCanceled {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			require.Less(t, time.Since(start), 2*time.Second)
			require.False(t, manager.IsActive())
		})
	}
	negative := NewManager(Options{Timeout: -time.Second})
	_, err := negative.Get(t.Context())
	require.ErrorContains(t, err, "non-negative")
}

// A controlled CDP peer exercises chromedp's actual request/cancellation path.
// It performs protocol setup then deliberately withholds page-operation replies.
func controlledCDPBrowser(t *testing.T, timeout time.Duration) (*Manager, <-chan struct{}, context.CancelFunc, func()) {
	t.Helper()
	var hold atomic.Bool
	var initialized atomic.Bool
	requested := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var req struct {
				ID        int64  `json:"id"`
				Method    string `json:"method"`
				SessionID string `json:"sessionId"`
			}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			if hold.Load() && req.Method == "Runtime.evaluate" {
				select {
				case requested <- struct{}{}:
				default:
				}
				continue
			}
			result := map[string]any{}
			switch req.Method {
			case "Target.attachToTarget":
				result["sessionId"] = "controlled-session"
			case "Runtime.evaluate":
				result["result"] = map[string]any{"type": "object", "className": "Window", "objectId": "controlled-window"}
				if initialized.Load() {
					result["result"] = map[string]any{"type": "string", "value": "https://controlled.invalid/"}
				}
			case "Page.getFrameTree":
				result["frameTree"] = map[string]any{"frame": map[string]any{"id": "controlled-frame", "loaderId": "controlled-loader", "url": "about:blank", "securityOrigin": "about:blank", "mimeType": "text/html"}}
			case "DOM.getDocument":
				result["root"] = map[string]any{"nodeId": 1, "backendNodeId": 1, "nodeType": 9, "nodeName": "#document", "localName": "", "nodeValue": "", "children": []any{}}
			case "Target.getTargets":
				result["targetInfos"] = []any{map[string]any{"targetId": "controlled-page", "type": "page", "title": "controlled", "url": "about:blank", "attached": false}}
			}
			response := map[string]any{"id": req.ID, "result": result}
			if req.SessionID != "" {
				response["sessionId"] = req.SessionID
			}
			if err := conn.WriteJSON(response); err != nil {
				return
			}
			if req.Method == "Target.setDiscoverTargets" {
				if err := conn.WriteJSON(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": map[string]any{"targetId": "controlled-page", "type": "page", "title": "controlled", "url": "about:blank", "attached": false}}}); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	parent, parentCancel := context.WithCancel(t.Context())
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/devtools/browser/controlled"
	chrome := filepath.Join(t.TempDir(), "controlled-chrome")
	script := fmt.Sprintf("#!/bin/sh\nprintf 'DevTools listening on %%s\\n' %q >&2\nexec sleep 30\n", wsURL)
	require.NoError(t, os.WriteFile(chrome, []byte(script), 0o700))
	manager := NewManager(Options{ChromePath: chrome, Timeout: timeout})
	t.Cleanup(func() { parentCancel(); manager.Close() })
	// Keep the watchdog longer than the configured launch cap. In whole-repo
	// race runs process startup may compete with many compiler/test processes;
	// a setup watchdog must not win over the behavior under test.
	initTimer := time.AfterFunc(10*time.Second, parentCancel)
	_, err := manager.Get(parent)
	initTimer.Stop()
	require.NoError(t, err)
	initialized.Store(true)

	hold.Store(true)
	return manager, requested, parentCancel, func() { hold.Store(false) }
}

func TestManagerOperationUsesConfiguredTimeoutWithActualCDP(t *testing.T) {
	manager, requested, _, release := controlledCDPBrowser(t, 3*time.Second)
	started := time.Now()
	err := manager.Refresh()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 5*time.Second)
	select {
	case <-requested:
	default:
		t.Fatal("browser did not issue the real page URL request")
	}
	// A timed-out action cancels only its request context. The first successful
	// startup Run must continue to own a usable CDP session afterwards.
	release()
	browser, err := manager.Get(t.Context())
	require.NoError(t, err)
	url, err := browser.URL()
	require.NoError(t, err)
	require.Equal(t, "https://controlled.invalid/", url)
}

func TestManagerSuccessfulLaunchOutlivesStartupDeadline(t *testing.T) {
	manager, _, _, release := controlledCDPBrowser(t, 3*time.Second)
	release()
	// Exercise the lifetime after the original startup deadline, rather than
	// merely observing the manager's cached IsActive flag.
	time.Sleep(3200 * time.Millisecond)
	browser, err := manager.Get(t.Context())
	require.NoError(t, err)
	url, err := browser.URL()
	require.NoError(t, err)
	require.Equal(t, "https://controlled.invalid/", url)
}

func TestManagerOperationPreservesParentCancellationWithActualCDP(t *testing.T) {
	manager, requested, cancel, _ := controlledCDPBrowser(t, 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- manager.Refresh() }()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("CDP operation was not admitted")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("browser operation ignored parent cancellation")
	}
}
