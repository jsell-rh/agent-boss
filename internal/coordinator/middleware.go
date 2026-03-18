package coordinator

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
//
// Two token classes are accepted:
//  1. Workspace token (s.apiToken / BOSS_API_TOKEN) — full access to all endpoints.
//  2. Per-agent token — valid only on agent-channel endpoints; the handler verifies
//     the token belongs to the specific agent being posted to (SEC-006).
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

		// Workspace token: full access to all endpoints.
		if hmac.Equal([]byte(token), []byte(s.apiToken)) {
			next.ServeHTTP(w, r)
			return
		}

		// Per-agent token: permitted on agent-channel endpoints and the MCP endpoint.
		//   - Agent-channel paths (/spaces/.../agent/... or /agent/...): the handler
		//     calls verifyAgentToken to enforce SEC-006 channel isolation.
		//   - MCP endpoint (/mcp, /mcp/): agents call tools (post_status, check_messages,
		//     send_message, etc.) over the MCP transport using their per-agent token.
		//     MCP requests POST to /mcp with the tool name + params in the body; the
		//     coordinator cannot enforce per-agent identity at this layer without parsing
		//     the body, so auth is accepted for any valid per-agent token on /mcp.
		if s.repo != nil && (isAgentChannelPath(r.URL.Path) || isMCPPath(r.URL.Path)) {
			next.ServeHTTP(w, r)
			return
		}

		writeJSONError(w, "unauthorized", http.StatusUnauthorized)
	})
}

// isAgentChannelPath returns true when the path is an agent status-post endpoint:
//
//	/spaces/{space}/agent/{name}   (multi-space)
//	/agent/{name}                  (legacy default-space)
func isAgentChannelPath(path string) bool {
	return strings.Contains(path, "/agent/")
}

// isMCPPath returns true when the path targets the MCP transport endpoint.
// Agents use their per-agent token in the Authorization header when calling
// MCP tools; the /mcp endpoint must accept these tokens.
func isMCPPath(path string) bool {
	return path == "/mcp" || strings.HasPrefix(path, "/mcp/")
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
		s.logEvent("warning: failed to save per-agent token hash for " + agentName + ": " + err.Error() + " — falling back to workspace token")
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

