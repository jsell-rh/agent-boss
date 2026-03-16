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

// authMiddleware wraps an http.Handler, requiring a Bearer token on mutating
// requests (POST, PATCH, DELETE, PUT). If s.apiToken is empty, the middleware
// is a no-op (open mode — backward compatible for local development).
//
// Two token classes are accepted:
//  1. Workspace token (s.apiToken / BOSS_API_TOKEN) — full access to all endpoints.
//  2. Per-agent token — valid only on agent-channel endpoints; the handler verifies
//     the token belongs to the specific agent being posted to.
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

		// Per-agent token: only permitted on agent-channel POST endpoints.
		// The handler is responsible for verifying the token matches the
		// specific agent (enforcing SEC-006 — agent channel isolation).
		if s.repo != nil && isAgentChannelPath(r.URL.Path) {
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
	// /spaces/.../agent/...
	if strings.Contains(path, "/agent/") {
		return true
	}
	// /agent/{name} (legacy)
	if strings.HasPrefix(path, "/agent/") {
		return true
	}
	return false
}

// generateAgentToken creates a cryptographically random UUID-style token for
// a specific agent, stores its SHA-256 hash in the DB, and returns the plain
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
