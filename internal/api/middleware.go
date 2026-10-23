package api

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

// statusWriter captures the HTTP status code for logging.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code != 0 {
		return
	}
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if w.code == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// withRequestLog logs each request's method, path, status, and duration.
func withRequestLog(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next(sw, r)
		if sw.code == 0 {
			sw.code = http.StatusOK
		}
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.code, time.Since(start))
	}
}

// withPanicRecovery recovers from panics in handler goroutines and returns
// a 500 instead of crashing the server.
func withPanicRecovery(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic in handler: %v\n%s", err, debug.Stack())
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next(w, r)
	}
}

// withCORS adds CORS headers for localhost development.
// In production, gateways handle CORS at the reverse-proxy layer;
// this wildcard is intentionally permissive for dev ergonomics
// (TUI, local dashboards, bridge scripts on loopback). Do NOT
// expose this endpoint to the public internet without an upstream
// reverse proxy that sets stricter Origin headers (B101).
//
// Origins can be configured via OVERKILL_API_CORS_ORIGINS (comma-separated
// list, e.g. "http://localhost:3000,http://localhost:5173"). When unset,
// allows all origins ("*") for local development.
func withCORS(next http.HandlerFunc) http.HandlerFunc {
	allowedOrigins := os.Getenv("OVERKILL_API_CORS_ORIGINS")

	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowOrigin := "*"

		if allowedOrigins != "" {
			// Restrict to the configured origins.
			allowOrigin = ""
			for _, o := range strings.Split(allowedOrigins, ",") {
				if origin == strings.TrimSpace(o) {
					allowOrigin = origin
					break
				}
			}
		}
		if allowOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

// withAPIAuth uses the immutable credential owned by this server.
func withAPIAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		presented := strings.TrimPrefix(auth, "Bearer ")
		if token == "" || !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
