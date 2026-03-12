# ADR: Authentication and Authorization Model for agent-boss

**Status:** Proposed  
**Date:** 2026-03-12  
**Authors:** ciso, cto  
**Related:** TASK-009 (Security Audit), TASK-010 (Auth Design)  
**Security findings addressed:** SEC-001, SEC-002, SEC-003, SEC-006

---

## Context

The security audit (TASK-009) identified that agent-boss has **zero authentication** on its HTTP API and MCP endpoint. Any process with TCP access to port 8899 can:

- Read all agent state, messages, and tasks
- POST to any agent's channel as any identity
- Spawn, stop, and restart agents
- Enable `--dangerously-skip-permissions` globally via PATCH `/settings`
- Delete spaces permanently

This ADR defines the authentication and authorization model to fix these gaps. It covers two implementation phases, defers multi-user to Phase 3, and includes a companion fix for SEC-003 (hardcoded `--dangerously-skip-permissions` in restart paths).

---

## Principal Model

Three classes of principal access the API, each with different trust levels and credential needs:

| Principal | Description | Credential |
|-----------|-------------|------------|
| **Human Operator** | The person running agent-boss (single operator assumed) | Static API token via `BOSS_API_TOKEN` env var |
| **Agent** | A spawned Claude Code session acting autonomously | Per-agent UUID token injected at spawn time via env var `BOSS_AGENT_TOKEN` |
| **CLI / External Client** | `boss` CLI commands, scripts, other tools | Same static API token as Human Operator (Phase 1) |

---

## Phase 1: Static Operator Token (Implement Now)

### Decision

A single static bearer token (`BOSS_API_TOKEN`) protects all mutating HTTP endpoints. If the env var is unset, the server starts in **open mode** (current behavior — backward compatible for local development). If set, the token is required on all non-read-only requests.

### Scope

**Protected (require token):** All `POST`, `PATCH`, `DELETE` requests; MCP tool calls (all tools mutate state); SSE connection upgrades.

**Unprotected (no token required):** `GET` requests to all read endpoints (`/spaces`, `/spaces/{name}`, `/spaces/{name}/agent/{name}`, `/spaces/{name}/tasks`, `/spaces/{name}/raw`, hierarchy, history). This covers dashboard polling, which is read-heavy.

### Middleware Sketch

```go
// authMiddleware wraps an http.Handler, requiring a Bearer token on mutating requests.
// If BOSS_API_TOKEN is empty, the middleware is a no-op (open mode).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if s.apiToken == "" {
            next.ServeHTTP(w, r)
            return
        }
        if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
            next.ServeHTTP(w, r)
            return
        }
        auth := r.Header.Get("Authorization")
        token := strings.TrimPrefix(auth, "Bearer ")
        // Use hmac.Equal for constant-time comparison to prevent timing attacks.
        if !strings.HasPrefix(strings.ToLower(auth), "bearer ") || !hmac.Equal([]byte(token), []byte(s.apiToken)) {
            writeJSONError(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

The `apiToken` field is populated from `BOSS_API_TOKEN` at server startup.

### Environment Variable

```bash
export BOSS_API_TOKEN="$(openssl rand -hex 32)"
DATA_DIR=./data /tmp/boss serve
```

### Settings Endpoint (SEC-002 Fix)

`PATCH /settings` is a mutating endpoint. With the auth middleware in place, it is automatically protected by the token check. No additional changes needed beyond applying the middleware.

### Frontend (Dashboard)

For Phase 1, the Vue dashboard stores the operator token in `localStorage` and includes it in `Authorization: Bearer` headers for all mutating fetch calls (spawn, stop, restart, reply, approve). A token input field is added to the Settings panel. Read-only GET polling requires no token.

### CLI Client

`client.go` gains an `authToken` field; `cmd/boss/main.go` reads `BOSS_API_TOKEN` from the environment and passes it to the client. The client sets `Authorization: Bearer <token>` on all requests.

---

## Phase 2: Per-Agent Tokens (Implement After Phase 1)

### Decision

At spawn time, the coordinator generates a UUID token for each agent, stores only its SHA-256 hash in the DB, and injects the raw token into the agent's tmux session as `BOSS_AGENT_TOKEN` **before** launching the Claude Code process. Agents authenticate using their token paired with their declared name; the coordinator verifies the pair against the DB.

This enables SEC-006 fix: agents can only post to their own channel.

### Token Storage

New table in SQLite:

```sql
CREATE TABLE agent_tokens (
    space_name  TEXT NOT NULL,
    agent_name  TEXT NOT NULL,
    token_hash  TEXT NOT NULL,  -- hex(SHA-256(raw_token))
    created_at  DATETIME NOT NULL,
    PRIMARY KEY (space_name, agent_name)
);
```

The raw token is **never stored** — only its SHA-256 hash. DB compromise does not leak usable credentials.

### Token Injection at Spawn

In `TmuxSessionBackend.CreateSession()`, after the tmux session is created and the shell is ready, inject the env var before launching the agent command:

```go
rawToken := uuid.NewString()
storeAgentTokenHash(spaceName, agentName, sha256hex(rawToken))

// Inject as env var — token travels in tmux send-keys buffer (visible in scrollback)
// but NOT in process args (ps aux) and NOT in the ignition prompt.
exportCmd := fmt.Sprintf("export BOSS_AGENT_TOKEN=%s", shellQuote(rawToken))
tmuxSendKeys(sessionID, exportCmd)  // before launching claude
tmuxSendKeys(sessionID, command)    // launch agent
```

**Ambient backend:** Injects the token as a proper environment variable in the session creation API call, avoiding tmux scrollback entirely.

**Accepted tradeoff:** The raw token is briefly visible in the tmux scrollback buffer. For Phase 2, this is acceptable — the token is single-use per agent lifetime and rotated on restart. Phase 3 can address this with a token exchange protocol.

### Agent Auth Flow

Agents include their token in all mutating requests:

```
Authorization: Bearer <BOSS_AGENT_TOKEN>
X-Agent-Name: ciso
```

Coordinator validation:
1. Hash the token value (SHA-256).
2. Look up `(space, X-Agent-Name)` in `agent_tokens`.
3. Compare hashes (constant-time).
4. On match: enforce that the agent can only POST to its own channel — `X-Agent-Name` must match the URL agent name.

The operator static token (`BOSS_API_TOKEN`) remains valid and grants full (admin) access.

### Agent-Scoped Access Rules

| Operation | Agent Token | Operator Token |
|-----------|-------------|----------------|
| POST status to own channel | ✅ | ✅ |
| POST status to another agent's channel | ❌ | ✅ |
| Send message (`send_message` MCP tool) | ✅ | ✅ |
| Spawn / stop / restart agents | ❌ | ✅ |
| PATCH `/settings` | ❌ | ✅ |
| DELETE space | ❌ | ✅ |
| Create / move / update tasks | ✅ | ✅ |
| Read any state (GET) | ✅ (unauthed) | ✅ |

---

## SEC-003 Fix: Hardcoded `--dangerously-skip-permissions` (Bundle with Phase 1)

Three locations in `lifecycle.go` hardcode `"claude --dangerously-skip-permissions"` as the default command regardless of the `allowSkipPermissions` server toggle:

- `spawnAgentService()` line ~356
- `restartAgentService()` line ~555
- `handleRestartAll()` goroutine line ~808

**Fix:**

```go
// Before (ignores the toggle):
command = "claude --dangerously-skip-permissions"

// After (respects the toggle):
if s.allowSkipPermissions {
    command = "claude --dangerously-skip-permissions"
} else {
    command = "claude"
}
```

Three-line change, safe to bundle with the Phase 1 auth PR.

---

## CORS and Security Headers (Companion to Phase 1)

**CORS (SEC-004):** Replace `Access-Control-Allow-Origin: *` on MCP, SSE, and registration endpoints with the actual server origin.

```go
// Use the configured external URL or fall back to localURL()
allowedOrigin := s.localURL()  // e.g. "http://localhost:8899"
if ext := os.Getenv("COORDINATOR_EXTERNAL_URL"); ext != "" {
    allowedOrigin = ext
}
w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
```

For the Vite dev server, also allow `http://localhost:5173`. A `BOSS_ALLOWED_ORIGINS` env var (comma-separated) can support both.

**Security headers (SEC-009):** Add a middleware that sets baseline headers on all responses:

```go
func securityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "SAMEORIGIN")
        w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
        next.ServeHTTP(w, r)
    })
}
```

CSP deferred: the Vue SPA with inline scripts requires careful tuning to avoid breaking the dashboard.

---

## Phase 3: Multi-User (Future, Not Designed Here)

When agent-boss evolves to support multiple human operators:

- Introduce a `users` table with hashed passwords or OAuth provider links.
- Issue per-user session tokens (JWT or opaque with expiry).
- Add RBAC: `admin` (full access), `operator` (spawn/stop agents, create tasks), `viewer` (read-only).
- The current `BOSS_API_TOKEN` becomes the bootstrap admin credential.
- Requires a login flow in the Vue frontend.

Not designed here — defer until a concrete multi-user use case emerges.

---

## Implementation Plan

### Phase 1 PR: `fix/auth-phase1`

Scope: auth middleware, SEC-003 fix, CORS narrowing, security headers, client token support.

Files to change:
| File | Change |
|------|--------|
| `internal/coordinator/server.go` | Add `apiToken` field; load from `BOSS_API_TOKEN`; wrap mux with `authMiddleware` and `securityHeaders` |
| `internal/coordinator/lifecycle.go` | Fix SEC-003 in 3 locations |
| `internal/coordinator/mcp_server.go` | Narrow CORS origin |
| `internal/coordinator/handlers_sse.go` | Narrow CORS origin |
| `internal/coordinator/protocol.go` | Narrow CORS origin |
| `internal/coordinator/client.go` | Add `authToken` field; send `Authorization` header |
| `cmd/boss/main.go` | Read `BOSS_API_TOKEN`, pass to client |
| `CLAUDE.md` | Document `BOSS_API_TOKEN` env var |
| `frontend/src/` | Store token in localStorage; send on mutating fetches; add token input to Settings |

### Phase 2 PR: `fix/auth-phase2`

Scope: DB schema, token generation at spawn, agent auth validation, scope enforcement.

Files to change:
| File | Change |
|------|--------|
| `internal/coordinator/db/models.go` | Add `AgentToken` model |
| `internal/coordinator/db/repository.go` | Add `StoreAgentToken`, `ValidateAgentToken` |
| `internal/coordinator/session_backend_tmux.go` | Inject `BOSS_AGENT_TOKEN` env var at spawn |
| `internal/coordinator/session_backend_ambient.go` | Inject `BOSS_AGENT_TOKEN` in session env |
| `internal/coordinator/server.go` | Agent token validation; scope enforcement middleware |
| `internal/coordinator/mcp_server.go` | MCP token validation |
| `docs/` | Update agent protocol docs to document `BOSS_AGENT_TOKEN` |

---

## Decision Summary

| Question | Decision |
|----------|----------|
| Single token vs per-agent? | Both: operator token (Phase 1) + per-agent tokens (Phase 2) |
| Store raw token or hash? | Hash only (SHA-256) — DB compromise does not leak usable credentials |
| Protect reads (GET)? | No in Phase 1–2 — local tool, dashboard polling is read-heavy |
| Multi-user? | Phase 3, explicitly deferred |
| Token delivery to agents | Env var via tmux send-keys before claude launch; tmux scrollback is accepted tradeoff |
| SEC-003 fix | Bundle with Phase 1 PR — simple 3-line fix in lifecycle.go |
| CORS fix | Bundle with Phase 1 PR |

---

## Addendum: Boss Corrections (2026-03-12)

Three corrections to the Phase 2 design incorporated after initial review:

### 1. MCP Endpoint Auth — Header Injection at Registration

The ADR secured the HTTP API but did not explicitly address agents accessing the MCP endpoint. The MCP server at `/mcp` is how agents call tools (`post_status`, `send_message`, etc.). It must also require the token.

**Problem:** The `claude mcp add` command that registers the boss MCP server with Claude Code is run at spawn time (in `session_backend_tmux.go`). If the MCP endpoint requires a Bearer token, this registration must include the auth header.

**Solution:** The `claude mcp add --transport http` command supports `--header` flags for custom HTTP headers. At spawn time, inject the header:

```bash
# Instead of:
claude mcp add boss-mcp --transport http http://localhost:8899/mcp

# Use:
claude mcp add boss-mcp --transport http http://localhost:8899/mcp \
  --header "Authorization: Bearer ${BOSS_AGENT_TOKEN}"
```

In Go (`session_backend_tmux.go`), the `mcpCmd` construction becomes:

```go
mcpCmd := fmt.Sprintf(
    "claude mcp add boss-mcp --transport http %s/mcp --header %s 2>/dev/null || true",
    mcpServerURL,
    shellQuote("Authorization: Bearer "+rawToken),
)
```

This ensures the claude MCP client authenticates with the coordinator on every tool call. The token is injected into the `BOSS_AGENT_TOKEN` env var first (see correction 2), so the env var is available when the mcp registration command runs.

**Phase 1 note:** During Phase 1 (static `BOSS_API_TOKEN`), the same pattern applies — the operator token is injected into the MCP registration header. Agents spawned by the coordinator inherit the operator token for MCP access.

### 2. tmux Environment — Use `set-environment` Instead of `send-keys`

The initial design used `tmux send-keys` to run `export BOSS_AGENT_TOKEN=xxx` in the shell before launching claude. Boss correctly notes that tmux supports setting session environment variables directly without keystroke injection.

**Better approach:** `tmux set-environment -t <session> <VAR> <value>` sets the variable in the tmux session's environment. Any process subsequently started in that session (via `send-keys`) inherits it. This avoids:
- The variable appearing in the shell's command history
- Any race condition between the export command and the claude launch

```go
// Set the env var directly in the tmux session environment
ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
defer cancel()
if err := exec.CommandContext(ctx, "tmux", "set-environment", "-t", sessionID,
    "BOSS_AGENT_TOKEN", rawToken).Run(); err != nil {
    // non-fatal: log and continue
}

// Also set for the MCP registration command to reference
if err := exec.CommandContext(ctx2, "tmux", "set-environment", "-t", sessionID,
    "BOSS_API_TOKEN", rawToken).Run(); err != nil {
    // non-fatal
}

// Now launch claude — it inherits BOSS_AGENT_TOKEN from the session env
tmuxSendKeys(sessionID, command)
```

**Scrollback note:** The raw token no longer appears in the tmux scrollback buffer at all with this approach. This is strictly better than the original send-keys design and removes the previously "accepted tradeoff."

**Important:** `tmux set-environment` sets variables on the session, not the running shell. The shell must be started *after* the variable is set for it to inherit via `new-window` or `new-session`. For existing shells already running, `set-environment` won't propagate automatically — but since we set it before launching any commands, this is fine for spawn.

### 3. Ambient Backend — Explicit Token Injection Design

The ambient backend (`session_backend_ambient.go`) creates sessions via an HTTP API. Token injection must use the ambient API's session creation mechanism rather than tmux.

**Approach:** `AmbientCreateOpts` gains a `EnvVars map[string]string` field. At spawn time, the coordinator sets:

```go
AmbientCreateOpts{
    DisplayName: agentName,
    Repos:       spawnRepos,
    EnvVars: map[string]string{
        "BOSS_AGENT_TOKEN": rawToken,
    },
}
```

The ambient backend passes `EnvVars` to the session creation API as environment variable overrides. The spawned process inherits them without any tmux send-keys involvement.

**MCP registration for ambient:** Since ambient sessions don't use tmux, the `claude mcp add` command must be sent as an initial input to the session (via `SendInput`) after the session reaches running state, with the token embedded in the `--header` flag:

```go
mcpCmd := fmt.Sprintf(
    "claude mcp add boss-mcp --transport http %s/mcp --header %s",
    mcpServerURL,
    shellQuote("Authorization: Bearer "+rawToken),
)
backend.SendInput(sessionID, mcpCmd)
```

This is analogous to what the tmux backend already does for MCP registration (in `CreateSession`), extended with the auth header.

**Updated implementation table for Phase 2:**

| File | Change |
|------|--------|
| `internal/coordinator/session_backend_tmux.go` | Use `tmux set-environment` for `BOSS_AGENT_TOKEN`; add `--header` to `claude mcp add` cmd |
| `internal/coordinator/session_backend_ambient.go` | Add `EnvVars` to `AmbientCreateOpts`; pass token via session env API; add auth header to MCP registration `SendInput` |
| `internal/coordinator/session_backend.go` | Add `EnvVars map[string]string` to `SessionCreateOpts` |
| `internal/coordinator/mcp_server.go` | Enforce token on all MCP tool calls (validate same as HTTP middleware) |
