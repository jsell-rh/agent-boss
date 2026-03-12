# Tech Debt Tracker

Known technical debt in Agent Boss as of 2026-03-12 (updated after PRs #144–#148). Sourced from existing docs and code review. See [QUALITY.md](../QUALITY.md) for quality grades.

Items are ordered by priority: **high** → **medium** → **low**.

---

## High Priority

### TD-001: `handlers_agent.go` is a 1680-LOC monolith
- **File:** `internal/coordinator/handlers_agent.go`
- **Issue:** All agent HTTP handlers are in one file: status POST, spawn, kill, restart, messages, register, interrupt, approval. After the TASK-014 server.go split, this became the new concentration point.
- **Impact:** Hard to review, hard to unit-test spawn logic in isolation.
- **Fix:** Split into `handlers_spawn.go`, `handlers_messages.go`, `handlers_interrupt.go` (~400–600 LOC each).

### TD-002: `DeprecatedTmuxSession` field still in `AgentUpdate`
- **File:** `internal/coordinator/types.go:99`
- **Issue:** `DeprecatedTmuxSession string \`json:"tmux_session,omitempty"\`` is marked with a prominent `## TODO — REMOVE ME` comment. Agents that still post `tmux_session` are relying on backward compat.
- **Impact:** Confusion in the data model; grows payload size.
- **Fix:** Audit which agents/clients still use `tmux_session`. Remove field once confirmed unused. Add a migration that reads the field on startup if needed.

### TD-003: Frontend components >1000 LOC
- **Files:** `frontend/src/components/SpaceOverview.vue` (1248 LOC), `frontend/src/components/AgentDetail.vue` (1243 LOC), `frontend/src/components/ConversationsView.vue` (1079 LOC)
- **Issue:** Components are too large to maintain. Each handles data fetching, rendering, and event coordination. PR #147 grew `ConversationsView.vue` from 997 → 1079 LOC, pushing it over the 1000-LOC threshold.
- **Impact:** Hard to test, slow to review, fragile under changes.
- **Fix:** Extract sub-components. SpaceOverview → `AgentGrid`, `TaskBoard`, `SpaceHeader`. AgentDetail → `AgentStatusCard`, `AgentHistoryPanel`, `AgentMessageList`.

### TD-004: No frontend unit tests
- **Area:** `frontend/`
- **Issue:** Zero Vitest/Jest unit tests. All frontend coverage is implicit via API integration tests in `server_test.go`.
- **Impact:** Frontend regressions are invisible until manual QA.
- **Fix:** Add Vitest. Start with composables (`useSSE.ts`, `useTime.ts`) and utility functions in `api/client.ts`.

---

## Medium Priority

### TD-005: `types.go` mixes domain types with rendering
- **File:** `internal/coordinator/types.go`
- **Issue:** Markdown rendering functions (`RenderMarkdown`, `renderAgentSection`, `renderTable`) live alongside domain types. These are >150 LOC of rendering logic in a types file.
- **Impact:** Inflates `types.go`; couples domain to presentation.
- **Fix:** Move rendering to `render.go` or `markdown.go`.

### TD-006: Server struct has too many fields (structural sprawl)
- **File:** `internal/coordinator/server.go:40–85`
- **Issue:** `Server` struct has ~20 fields: SSE state, nudge state, registration, liveness, backends, personas, permissions toggle. All initialized in `NewServer`.
- **Impact:** Hard to understand what the server "is" vs what it "does".
- **Fix:** Group related fields into embedded sub-structs: `sseState`, `nudgeState`, `agentRegistry`.

### TD-007: `server_test.go` is a 4169-LOC mega-test file
- **File:** `internal/coordinator/server_test.go`
- **Issue:** All HTTP integration tests in one file. Hard to navigate and contributes to slow test runs (39s).
- **Impact:** Long compile + test cycle; hard to find relevant tests.
- **Fix:** Split by domain: `agents_test.go`, `tasks_test.go`, `sse_test.go`, `spawn_test.go`.

### TD-008: `snapshot()` uses JSON round-trip for deep copy
- **File:** `internal/coordinator/types.go:394–399`
- **Issue:** `KnowledgeSpace.snapshot()` marshals and unmarshals the whole space for a deep copy. Correct, but O(n) allocations on every save.
- **Impact:** Acceptable at current scale but will slow as agent/task count grows.
- **Fix:** Implement explicit deep-copy methods, or use `encoding/gob` which is ~2× faster than JSON.

### ~~TD-009: CLAUDE.md Project Structure section is outdated~~

> **RESOLVED** in PR #144 (2026-03-12) — CLAUDE.md Project Structure table updated to reflect the current file split (separate handler files, MCP server, SSE handlers). Knowledge Base section added.

### TD-010: Paude integration proposed but never built
- **File:** `docs/paude.md`
- **Issue:** `paude.md` proposes a Paude integration for agent orchestration. No code exists.
- **Impact:** Dead proposal creating confusion about current capabilities.
- **Fix:** Either implement it or mark the doc as `superseded`.

---

## Low Priority

### TD-011: Postgres is second-class
- **File:** `internal/coordinator/postgres_test.go`, `db/`
- **Issue:** Postgres is listed as supported via `DB_TYPE=postgres` but tests are gated (`postgres_test.go` uses build tags or env vars). CI runs SQLite only.
- **Impact:** Postgres regressions may go undetected.
- **Fix:** Add Postgres CI job (GitHub Actions service container).

### TD-012: `mcp_tools.go` approaching monolith territory
- **File:** `internal/coordinator/mcp_tools.go` (1104 LOC)
- **Issue:** All MCP tool implementations in one file. Currently manageable but growing with each new tool.
- **Impact:** Will become a problem as MCP surface expands.
- **Fix:** Split by domain when >1500 LOC: `mcp_agent_tools.go`, `mcp_task_tools.go`, `mcp_space_tools.go`.

### TD-013: SSE mutex lock-order discipline
- **File:** `internal/coordinator/server.go`, `handlers_sse.go`
- **Issue:** `sseMu` (for SSE clients) and `s.mu` (for space state) are separate mutexes. If any code path ever holds both, a deadlock is possible if lock order is inconsistent.
- **Impact:** Low risk currently (locks appear not to nest), but fragile.
- **Fix:** Document lock order in a comment at the top of `server.go`. Add a locking audit to code review checklist.

### TD-014: Software Factory proposals are stale
- **Files:** `docs/software-factory.md`, `docs/software-factory2.md`, `docs/agent-boss-factory-proposal.md`
- **Issue:** Three overlapping factory-scale vision documents from early 2026. None are actionable plans.
- **Impact:** Cognitive overhead for new contributors trying to understand project direction.
- **Fix:** Consolidate into one forward-looking roadmap or archive as `proposed`.

### TD-015: Hexagonal architecture Phase 2 not yet started
- **Files:** `internal/domain/`, `internal/coordinator/`
- **Issue:** PR #145 created the hexagonal domain foundation (`internal/domain/types.go`, `internal/domain/ports/storage.go`), but Phase 2 — extracting `internal/adapters/sqlite`, `internal/adapters/http`, `internal/adapters/mcp`, `internal/adapters/sse` from the coordinator — has not begun. The architecture test (`TestAdapterIsolationBaseline`) currently passes only by acknowledging the adapters don't exist yet.
- **Impact:** The coordinator package remains a 7000+ LOC monolith containing HTTP handlers, persistence, MCP, SSE, and business logic. The domain isolation benefit of the hexagonal design is not yet realized.
- **Fix:** Implement Phase 2 as described in [docs/design-docs/hexagonal-architecture.md](../design-docs/hexagonal-architecture.md). The `architecture_test.go` will begin enforcing isolation once adapter packages exist.
