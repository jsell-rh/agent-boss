# Agent Boss — Quality Grades

Snapshot as of 2026-03-12. Grades A–D. See [tech-debt-tracker.md](exec-plans/tech-debt-tracker.md) for action items.

---

## Grading Rubric

| Grade | Meaning |
|-------|---------|
| **A** | Clean, well-tested, maintainable. Minor or no issues. |
| **B** | Good overall. Some complexity or gaps that should be addressed. |
| **C** | Functional but problematic. Refactoring needed soon. |
| **D** | Significant issues. High risk, hard to maintain. |

---

## Subsystem Grades

### `internal/coordinator/server.go` — **B+**

- **334 LOC.** Properly decomposed after the TASK-014 refactor.
- Handles Server struct definition, routing, and Start/Stop lifecycle only.
- Positive: routing is clear, SSE clients and liveness loop are well-separated.
- Concern: Server struct has ~20 fields spanning multiple concerns (nudge state, SSE state, registration, liveness, backends). A config struct would clarify initialization.
- No dedicated unit tests for `server.go` alone (covered by integration tests).

### `internal/coordinator/types.go` — **B**

- **802 LOC.** Comprehensive domain model: all entity types, hierarchy logic, markdown rendering.
- Positive: clean JSON serialization, backward-compat `UnmarshalJSON`, cycle detection.
- Concern: mixing domain types with rendering logic (`RenderMarkdown`, `renderAgentSection`, `renderTable`) inflates the file. Rendering belongs in a separate package.
- Contains a live `## TODO — REMOVE ME` comment on `DeprecatedTmuxSession` field (tech debt signal).
- `snapshot()` uses JSON round-trip for deep copy — functional but slow; acceptable for current load.

### `internal/coordinator/handlers_agent.go` — **C+**

- **1680 LOC.** The new monolith after the server.go split.
- Handles agent status POST, spawn, kill, restart, messages, register, interrupt, approval — all in one file.
- Positive: each handler function is focused; no global state mutation outside server methods.
- Concern: file is too large to review comfortably. Should be split by concern: `handlers_spawn.go`, `handlers_messages.go`, `handlers_interrupt.go`.
- Complex spawn path (backend selection, config resolution, ignition prompt) is hard to unit-test.

### `frontend/` Vue SPA — **B-**

- **~11,270 LOC** across 20+ components.
- Positive: Vue 3 + TypeScript with strong typing (`frontend/src/types/index.ts`). SSE composable is clean. `api/client.ts` is well-organized.
- Concern: `SpaceOverview.vue` (1246 LOC) and `AgentDetail.vue` (1243 LOC) are far too large. Each should be decomposed into smaller sub-components.
- Concern: no frontend unit tests. Only tested via manual QA and `server_test.go` integration tests on the API layer.
- `ConversationsView.vue` (997 LOC) is also a refactoring candidate.

### Task System (`handlers_task.go` + task fields in `types.go`) — **A-**

- **887 LOC** for handlers; types are embedded in `types.go`.
- Positive: clean Kanban state machine (backlog → in_progress → review → done → blocked). Task events tracked on every mutation. Parent/subtask relationships. Staleness detection.
- Positive: MCP tools expose task CRUD to agents cleanly.
- Minor: `IsStale` is computed at read time (not stored) — a good choice, but undocumented in comments.

### SSE / Events (`handlers_sse.go`, `journal.go`) — **B**

- Ring buffer (cap 200) per agent for `Last-Event-ID` replay. Fan-out to all SSE clients.
- Events persisted to SQLite via journal callback — survives restarts.
- Positive: per-agent filtering by `agent` query param; space-level and global subscription both work.
- Concern: SSE client map uses a pointer-keyed `map[*sseClient]struct{}` guarded by a separate `sseMu` mutex — correct but could race with `s.mu` if lock order is ever inverted. Careful review needed on any change.
- `journal.go` ring buffer logic is clean and well-commented.

### Test Coverage — **A**

- **244 tests** pass with `-race`. Multiple dedicated test files by subsystem:
  - `server_test.go` (4169 LOC) — HTTP integration tests, the primary coverage driver
  - `hierarchy_test.go`, `lifecycle_test.go`, `journal_test.go` — focused unit tests
  - `protocol_test.go`, `sqlite_test.go`, `integration_test.go`
  - `session_backend_ambient_test.go` — ambient backend coverage
- Race detector enabled by default in CI.
- Gap: no frontend tests. No chaos/load tests.

---

## Summary Table

| Subsystem | Grade | Biggest Risk |
|-----------|-------|-------------|
| `server.go` | B+ | Server struct sprawl |
| `types.go` | B | Rendering mixed with types; deprecated field |
| `handlers_agent.go` | C+ | 1680-LOC monolith handler |
| Frontend Vue | B- | Components >1000 LOC; no unit tests |
| Task system | A- | Minor: stale logic undocumented |
| SSE / Events | B | Mutex lock-order discipline |
| Test coverage | A | No frontend tests |
