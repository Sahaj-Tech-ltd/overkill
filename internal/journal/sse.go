// Package journal — SSE memory dashboard endpoint (§4.19 last
// open item). Streams observation + alert events to subscribers
// over Server-Sent Events so users can build their own dashboard
// without us baking in a UI choice.
//
// Why SSE not WebSocket: one-way is the right shape (the
// dashboard reads, doesn't write), browsers handle reconnect for
// free, and curl can subscribe with no extra tooling.
//
// What gets streamed:
//   - `observation` — every observation stored via Broadcast
//   - `alert` — every alert created via BroadcastAlert
//   - `heartbeat` — empty event every 30s so reverse proxies
//     don't time out idle connections
//
// Auth: loopback-only by default. The caller (`overkill dashboard`
// CLI) sets a bearer Token; clients pass `Authorization: Bearer X`.
// Empty token disables auth — fine for local-only deployments.
package journal

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// DashboardServer is the SSE endpoint hub. Subscribers register via
// Subscribe and receive events through their channel until they
// close it. Best-effort delivery: a slow subscriber gets its event
// dropped rather than backing up the broadcast path.
type DashboardServer struct {
	Listen string // default 127.0.0.1:7802
	Token  string // empty disables auth (loopback deployments)

	mu          sync.RWMutex
	subscribers map[chan dashboardEvent]struct{}
	closed      bool // set when Run shuts down; broadcasts become no-ops

	server *http.Server
}

// dashboardEvent is one SSE message. Type maps to the SSE `event:`
// line; Data is JSON-encoded and goes in `data:`.
type dashboardEvent struct {
	Type string
	Data any
}

// NewDashboardServer wires the server. Caller controls Listen +
// Token; defaults are loopback-only with no auth.
func NewDashboardServer() *DashboardServer {
	return &DashboardServer{
		subscribers: map[chan dashboardEvent]struct{}{},
	}
}

// Run starts the HTTP server and blocks until ctx cancels.
func (d *DashboardServer) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/dashboard/events", d.handleEvents)
	mux.HandleFunc("/dashboard/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	addr := d.Listen
	if addr == "" {
		addr = config.DefaultSSEDashboardAddr
	}
	d.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- d.server.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.server.Shutdown(shutCtx)
		// Mark closed first so in-flight broadcasts become no-ops,
		// then close all subscriber channels under lock.
		d.mu.Lock()
		d.closed = true
		for ch := range d.subscribers {
			close(ch)
		}
		d.subscribers = map[chan dashboardEvent]struct{}{}
		d.mu.Unlock()
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// BroadcastObservation fans an observation event to every live
// subscriber. Non-blocking: slow subscribers drop the event rather
// than gating the publisher.
func (d *DashboardServer) BroadcastObservation(obs *Observation) {
	d.broadcast(dashboardEvent{Type: "observation", Data: obs})
}

// BroadcastAlert mirrors BroadcastObservation for alert events.
func (d *DashboardServer) BroadcastAlert(alert *Alert) {
	d.broadcast(dashboardEvent{Type: "alert", Data: alert})
}

func (d *DashboardServer) broadcast(ev dashboardEvent) {
	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return // server is shutting down, don't send to closing channels
	}
	defer d.mu.RUnlock()
	for ch := range d.subscribers {
		select {
		case ch <- ev:
		default:
			// Subscriber buffer full — drop the event for them.
			// Rather a slow client misses an update than the
			// publisher serializes on it.
		}
	}
}

// authorized checks the bearer token (or accepts everything when
// no token is configured). Uses constant-time comparison to avoid
// timing side-channel attacks on the bearer token.
func (d *DashboardServer) authorized(r *http.Request) bool {
	if d.Token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	presented := strings.TrimPrefix(h, "Bearer ")
	return subtle.ConstantTimeCompare([]byte(presented), []byte(d.Token)) == 1
}

// handleEvents is the SSE endpoint. Clients see Content-Type
// text/event-stream and receive a stream of `event:` + `data:`
// blocks separated by blank lines.
func (d *DashboardServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !d.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	ch := make(chan dashboardEvent, 32)
	d.mu.Lock()
	d.subscribers[ch] = struct{}{}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		if _, ok := d.subscribers[ch]; ok {
			delete(d.subscribers, ch)
		}
		d.mu.Unlock()
	}()

	// Send a hello so the client knows it's connected before any
	// real event arrives.
	fmt.Fprintf(w, "event: hello\ndata: {\"ok\":true}\n\n")
	flusher.Flush()

	// Heartbeat ticker so reverse proxies / load balancers don't
	// close the connection on perceived idle.
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload)
			flusher.Flush()
		}
	}
}

// SubscriberCount returns the number of live SSE clients. Exposed
// for tests and operator visibility (`dashboard status` could read
// this).
func (d *DashboardServer) SubscriberCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.subscribers)
}
