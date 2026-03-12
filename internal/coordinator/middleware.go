package coordinator

import (
	"crypto/hmac"
	"net/http"
	"strings"
)

// securityHeadersMiddleware adds standard security headers to every response.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// authMiddleware wraps an http.Handler, requiring a Bearer token on mutating
// requests (POST, PATCH, DELETE, PUT). If s.apiToken is empty, the middleware
// is a no-op (open mode — backward compatible for local development).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.apiToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			writeJSONError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		token := auth[7:] // strip "Bearer " (7 chars) preserving original case
		if !hmac.Equal([]byte(token), []byte(s.apiToken)) {
			writeJSONError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
