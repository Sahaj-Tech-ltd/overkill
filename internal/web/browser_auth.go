package web

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// browserSameOrigin refuses cross-origin browser requests. Header clients may
// omit Origin; browser cookie issuance additionally requires a real bearer.
func browserSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && u.Host == r.Host && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func (s *Server) handleBrowserAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "POST or DELETE only", http.StatusMethodNotAllowed)
		return
	}
	if !browserSameOrigin(r) {
		http.Error(w, "same origin required", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || s.cfg.Token == "" || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(s.cfg.Token)) != 1 {
			http.Error(w, "valid bearer required", http.StatusUnauthorized)
			return
		}
		// The token stays in an HttpOnly session cookie. It is never put in a URL
		// or browser storage. Plain HTTP loopback needs a non-Secure cookie.
		http.SetCookie(w, &http.Cookie{Name: "overkill-token", Value: s.cfg.Token, Path: "/api", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
		http.SetCookie(w, &http.Cookie{Name: "overkill-token", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	} else {
		// Logout only deletes credentials; it never mints or reveals a token.
		http.SetCookie(w, &http.Cookie{Name: "overkill-token", Path: "/api", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
		// Remove the former root-path cookie too, including old installed profiles.
		http.SetCookie(w, &http.Cookie{Name: "overkill-token", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
