package coordinator

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

// responseRecorder wraps http.ResponseWriter to capture the HTTP status code
// written by a handler. The status defaults to 200 (matching net/http behaviour
// when Write is called without an explicit WriteHeader).
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
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
		s.emit(DomainEvent{
			Level:     level,
			EventType: EventHTTPRequest,
			Msg:       fmt.Sprintf("%s %s → %d (%s)", r.Method, r.URL.Path, rr.status, dur.Round(time.Millisecond)),
			Fields: map[string]string{
				"method":   r.Method,
				"path":     r.URL.Path,
				"status":   fmt.Sprintf("%d", rr.status),
				"duration": dur.String(),
			},
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

// generateAgentToken mints a fresh per-agent Bearer token and persists its
// token for injection into the agent's tmux session. Safe to call on every
// spawn/restart — a new token replaces any previous one.
func (s *Server) generateAgentToken(spaceName, agentName string) string {
	if s.apiToken == "" || s.repo == nil {
		// Open mode or no DB: fall back to shared workspace token (Phase 1 behaviour).
		return s.apiToken
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// Fallback to shared token on RNG failure (extremely unlikely).
		return s.apiToken
	}
	token := hex.EncodeToString(raw) // 64-char hex string
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	if err := s.repo.SaveAgentTokenHash(spaceName, agentName, hash); err != nil {
		// Non-fatal: fall back to shared token if DB write fails.
		return s.apiToken
	}
	return token
}

// verifyAgentToken checks that the Bearer token presented in r matches the
// stored per-agent token hash for (spaceName, agentName). Returns true when:
//   - Auth is disabled (s.apiToken == ""), or
//   - The presented token is the workspace token (operator access), or
//   - The SHA-256 hash of the presented token matches the stored hash.
func (s *Server) verifyAgentToken(r *http.Request, spaceName, agentName string) bool {
	if s.apiToken == "" {
		return true // open mode
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return false
	}
	token := auth[7:]

	// Workspace token always grants access.
	if hmac.Equal([]byte(token), []byte(s.apiToken)) {
		return true
	}

	// Per-agent token: verify hash against DB.
	if s.repo == nil {
		return false
	}
	storedHash, err := s.repo.GetAgentTokenHash(spaceName, agentName)
	if err != nil || storedHash == "" {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	presented := hex.EncodeToString(sum[:])
	return hmac.Equal([]byte(presented), []byte(storedHash))
}

// tokenErrorf formats and returns an error string for auth failures.
// Kept here to avoid scattering auth messaging across multiple files.
func tokenErrorf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
