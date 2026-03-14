package coordinator

import (
	"crypto/hmac"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// corsOrigins caches the computed allowed-origins list (init on first use).
var (
	corsOnce    sync.Once
	corsOrigins []string
)

func initCORSOrigins() {
	corsOrigins = []string{"http://localhost:8899", "http://localhost:5173"}
	if ext := os.Getenv("BOSS_ALLOWED_ORIGINS"); ext != "" {
		for _, o := range strings.Split(ext, ",") {
			if o = strings.TrimSpace(o); o != "" {
				corsOrigins = append(corsOrigins, o)
			}
		}
	}
}

// setCORSOriginHeader reflects the request Origin back if it is in the
// allowed-origins allowlist (defaults: localhost:8899 and localhost:5173;
// extended via BOSS_ALLOWED_ORIGINS env var, comma-separated).
// Call this instead of setting "Access-Control-Allow-Origin: *".
// Vary: Origin is always set so caching proxies do not serve one user's
// CORS response to a different origin.
func setCORSOriginHeader(w http.ResponseWriter, r *http.Request) {
	corsOnce.Do(initCORSOrigins)
	w.Header().Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	for _, o := range corsOrigins {
		if strings.EqualFold(origin, o) {
			w.Header().Set("Access-Control-Allow-Origin", o)
			return
		}
	}
}

// securityHeadersMiddleware adds standard security headers to every response.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// errBufMax is the maximum bytes buffered from an error response body.
// Large enough to capture any writeJSONError message; small enough to be free.
const errBufMax = 256

// responseRecorder wraps http.ResponseWriter to capture the HTTP status code
// and, for error responses (4xx/5xx), the first errBufMax bytes of the body.
// The status defaults to 200 (matching net/http behaviour when Write is called
// without an explicit WriteHeader).
type responseRecorder struct {
	http.ResponseWriter
	status int
	errBuf strings.Builder
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

// Write passes bytes through to the underlying writer. For error responses it
// also buffers up to errBufMax bytes so the middleware can log the reason.
func (rr *responseRecorder) Write(b []byte) (int, error) {
	if rr.status >= 400 {
		if remaining := errBufMax - rr.errBuf.Len(); remaining > 0 {
			rr.errBuf.Write(b[:min(len(b), remaining)])
		}
	}
	return rr.ResponseWriter.Write(b)
}

// requestLoggingMiddleware logs every HTTP request as a DomainEvent after the
// handler returns. 4xx responses are logged at warn level; 5xx at error level;
// everything else at info. SSE endpoints are skipped — they are long-lived
// connections whose log entry would appear only after the stream closes.
func (s *Server) requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip SSE streams — they block until the client disconnects.
		if r.URL.Path == "/events" || strings.HasSuffix(r.URL.Path, "/events") {
			next.ServeHTTP(w, r)
			return
		}
		rr := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rr, r)
		dur := time.Since(start)

		level := LevelInfo
		if rr.status >= 500 {
			level = LevelError
		} else if rr.status >= 400 {
			level = LevelWarn
		}
		msg := fmt.Sprintf("%s %s → %d (%s)", r.Method, r.URL.Path, rr.status, dur.Round(time.Millisecond))
		fields := map[string]string{
			"method":   r.Method,
			"path":     r.URL.Path,
			"status":   fmt.Sprintf("%d", rr.status),
			"duration": dur.String(),
		}
		if body := strings.TrimSpace(rr.errBuf.String()); body != "" {
			msg += " — " + body
			fields["error_body"] = body
		}
		s.emit(DomainEvent{
			Level:     level,
			EventType: EventHTTPRequest,
			Msg:       msg,
			Fields:    fields,
		})
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
