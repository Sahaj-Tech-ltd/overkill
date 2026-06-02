package journal

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDashboard_HealthOK(t *testing.T) {
	d := NewDashboardServer()
	mux := newDashboardMux(d)
	req := httptest.NewRequest("GET", "/dashboard/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

func TestDashboard_AuthRequired(t *testing.T) {
	d := NewDashboardServer()
	d.Token = "secret"

	srv := httptest.NewServer(newDashboardMux(d))
	defer srv.Close()

	// No auth header → 401.
	resp, err := http.Get(srv.URL + "/dashboard/events")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestDashboard_BroadcastsObservationsToSubscribers(t *testing.T) {
	d := NewDashboardServer()
	srv := httptest.NewServer(newDashboardMux(d))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the SSE client in a goroutine and capture lines.
	type result struct {
		body string
		err  error
	}
	out := make(chan result, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/dashboard/events", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			out <- result{err: err}
			return
		}
		defer resp.Body.Close()
		var sb strings.Builder
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			sb.WriteString(sc.Text())
			sb.WriteByte('\n')
			if strings.Contains(sb.String(), `"id":"obs-1"`) {
				break
			}
		}
		out <- result{body: sb.String()}
	}()

	// Give the subscriber a beat to register.
	waitFor(t, func() bool { return d.SubscriberCount() >= 1 }, 2*time.Second)

	obs := &Observation{ID: "obs-1", Title: "test obs", SessionID: "s"}
	d.BroadcastObservation(obs)

	select {
	case r := <-out:
		if r.err != nil {
			t.Fatalf("subscriber err: %v", r.err)
		}
		if !strings.Contains(r.body, "event: hello") {
			t.Errorf("expected hello event: %s", r.body)
		}
		if !strings.Contains(r.body, "event: observation") {
			t.Errorf("expected observation event: %s", r.body)
		}
		if !strings.Contains(r.body, `"id":"obs-1"`) {
			t.Errorf("payload missing: %s", r.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not receive event in time")
	}
	cancel()
	wg.Wait()
}

func TestDashboard_SlowSubscriberDropsEvents(t *testing.T) {
	d := NewDashboardServer()
	// Inject a tiny subscriber with no buffer to simulate slowness.
	d.mu.Lock()
	ch := make(chan dashboardEvent) // unbuffered → BroadcastObservation can't enqueue
	d.subscribers[ch] = struct{}{}
	d.mu.Unlock()

	// Broadcast — must not block.
	done := make(chan struct{})
	go func() {
		d.BroadcastObservation(&Observation{ID: "obs-x"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("broadcast blocked on slow subscriber")
	}

	// Drain. The unbuffered subscriber should NOT have received the
	// event (dropped by the select-default path).
	select {
	case <-ch:
		t.Error("slow subscriber should have had its event dropped")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDashboard_BroadcastAlert(t *testing.T) {
	d := NewDashboardServer()
	d.mu.Lock()
	ch := make(chan dashboardEvent, 1)
	d.subscribers[ch] = struct{}{}
	d.mu.Unlock()

	d.BroadcastAlert(&Alert{ID: "a-1", Type: AlertPatternDetected, Message: "noticed something"})

	select {
	case ev := <-ch:
		if ev.Type != "alert" {
			t.Errorf("expected alert event type, got %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("alert not broadcast")
	}
}

func TestDashboard_NoTokenAcceptsAnyRequest(t *testing.T) {
	d := NewDashboardServer()
	req := httptest.NewRequest("GET", "/dashboard/health", nil)
	rec := httptest.NewRecorder()
	newDashboardMux(d).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("no-token mode should accept request, got %d", rec.Code)
	}
}

// helpers --------------------------------------------------------------

func newDashboardMux(d *DashboardServer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/dashboard/events", d.handleEvents)
	mux.HandleFunc("/dashboard/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// silence unused-import warning for io when test grows.
var _ = io.EOF
