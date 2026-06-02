package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// withRequestLog logs each request's method, path, status, and duration.
func withRequestLog(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next(sw, r)
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

// apiToken returns the configured API bearer token, or empty string if auth is disabled.
func apiToken() string {
	return os.Getenv("OVERKILL_API_TOKEN")
}

// withAPIAuth adds optional API token authentication. When OVERKILL_API_TOKEN
// is set in the environment, all requests must include an Authorization header
// of the form "Bearer <token>". When the env var is empty, auth is skipped
// but a startup warning is logged. Set OVERKILL_API_TOKEN to enable auth
// for production deployments.
func withAPIAuth(next http.HandlerFunc) http.HandlerFunc {
	token := apiToken()
	if token == "" {
		// Auto-generate a random token when none is configured.
		// No process — even local ones — can call the API without it.
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Printf("api: FATAL — failed to generate auth token: %v", err)
			os.Exit(1)
		}
		token = hex.EncodeToString(b)
		log.Printf("api: auto-generated API token (set OVERKILL_API_TOKEN to override)")
	}
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		presented := strings.TrimPrefix(auth, "Bearer ")
		if !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
