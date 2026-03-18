# OpenDispatch — Quality Grades

Snapshot as of 2026-03-18 (updated after PRs #213–#242). Grades A–D. See [tech-debt-tracker.md](exec-plans/tech-debt-tracker.md) for action items.

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

- **374 LOC.** Properly decomposed after the TASK-014 refactor. Minor growth from fleet routing additions (PR #231).
- Handles Server struct definition, routing, and Start/Stop lifecycle only.
- Positive: routing is clear, SSE clients and liveness loop are well-separated.
- Concern: Server struct has ~20 fields spanning multiple concerns (nudge state, SSE state, registration, liveness, backends). A config struct would clarify initialization.
- No dedicated unit tests for `server.go` alone (covered by integration tests).

### `internal/coordinator/types.go` — **B**

- **804 LOC.** Comprehensive domain model: all entity types, hierarchy logic, markdown rendering.
- Positive: clean JSON serialization, backward-compat `UnmarshalJSON`, cycle detection.
- Concern: mixing domain types with rendering logic (`RenderMarkdown`, `renderAgentSection`, `renderTable`) inflates the file. Rendering belongs in a separate package.
- Contains a live `## TODO — REMOVE ME` comment on `DeprecatedTmuxSession` field (tech debt signal).
- `snapshot()` uses JSON round-trip for deep copy — functional but slow; acceptable for current load.

### `internal/coordinator/handlers_agent.go` — **C+**

- **1807 LOC.** Grew by 125 LOC since last snapshot (PRs #219–#241 added auth fixes, spawn improvements, and pagination ignition update).
- Handles agent status POST, spawn, kill, restart, messages, register, interrupt, approval — all in one file.
- Positive: each handler function is focused; no global state mutation outside server methods.
- Concern: file continues to grow and is increasingly hard to review. Split is overdue: `handlers_spawn.go`, `handlers_messages.go`, `handlers_interrupt.go`.
- Complex spawn path (backend selection, config resolution, ignition prompt) is hard to unit-test.

### `internal/coordinator/fleet.go` — **A-** _(new, PR #231)_

- **404 LOC.** Implements `boss export` and `boss import` — the agent-compose.yaml fleet blueprint feature.
- Positive: well-isolated module with its own `fleet_test.go` (449 LOC). Security validators (`ValidateFleetCommand`, `ValidateWorkDir`) are cleanly separated and tested.
- Positive: CLI-as-orchestrator design keeps server endpoints thin; import logic lives in the CLI, not the server.
- Minor: `ODIS_COMMAND_ALLOWLIST` and `ODIS_WORK_DIR_PREFIX` env vars are now live but were undocumented before this gardening run.

### `frontend/` Vue SPA — **C+**

- **~12,000+ LOC** across 22+ components (grew significantly from UX and perf sprints PRs #213–#231).
- Positive: Vue 3 + TypeScript with strong typing. SSE composable is clean. Pre-commit TS typecheck hook added (PR #213).
- Concern: Three components have grown well beyond 1000 LOC: `SpaceOverview.vue` (1448 LOC, was 1248), `ConversationsView.vue` (1410 LOC, was 1079), `AgentDetail.vue` (1300 LOC, was 1243). Trend is worsening; fleet import modal (`ImportFleetModal.vue`, 428 LOC) added as a new component (PR #230).
- Concern: no frontend unit tests. Only tested via manual QA and `server_test.go` integration tests on the API layer.
- Grade lowered from B- to C+: all three large components grew substantially with no decomposition.

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

- **305 tests** pass with `-race` in `internal/coordinator/` (PR #241 added `TestCheckMessagesPagination` + `TestCheckMessagesSmallBacklog`; PR #242 added `TestPerAgentTokenIsolation`; fleet.go added tests via `fleet_test.go`). Domain package (`internal/domain/`) adds `TestAdapterIsolationBaseline`. Multiple dedicated test files by subsystem:
  - `server_test.go` — HTTP integration tests, the primary coverage driver
  - `fleet_test.go` — fleet import/export + security validator tests (PR #231)
  - `hierarchy_test.go`, `lifecycle_test.go`, `journal_test.go` — focused unit tests
  - `protocol_test.go`, `sqlite_test.go`, `integration_test.go`
  - `session_backend_ambient_test.go` — ambient backend coverage
  - `internal/domain/architecture_test.go` — hexagonal architecture isolation test
- Race detector enabled by default in CI. TypeScript typecheck runs as a separate CI job (PR #213).
- Gap: no frontend unit tests. No chaos/load tests.

### `internal/domain/` Hexagonal Foundation — **B** _(new, PR #145)_

- PR #145 planted the hexagonal architecture foundation: `internal/domain/types.go` (domain entities), `internal/domain/ports/storage.go` (storage interface), and `internal/domain/architecture_test.go` (isolation guard).
- Positive: clean separation of domain types from coordinator implementation; `architecture_test.go` will enforce adapter isolation when Phase 2 creates `internal/adapters/`.
- Concern: Phase 2 (extracting `internal/adapters/sqlite`, `internal/adapters/http`, `internal/adapters/mcp`) is not yet started. The coordinator still holds all business logic.
- See [docs/design-docs/hexagonal-architecture.md](design-docs/hexagonal-architecture.md) for the full plan.

---

## Summary Table

| Subsystem | Grade | Biggest Risk |
|-----------|-------|-------------|
| `server.go` | B+ | Server struct sprawl |
| `types.go` | B | Rendering mixed with types; deprecated field |
| `handlers_agent.go` | C+ | 1807-LOC monolith, growing; split overdue |
| `fleet.go` | A- | New — undocumented env vars (now fixed) |
| Frontend Vue | C+ | Three components >1300 LOC, trend worsening; no unit tests |
| Task system | A- | Minor: stale logic undocumented |
| SSE / Events | B | Mutex lock-order discipline |
| Test coverage | A | No frontend tests |
| `internal/domain/` (hexagonal) | B | Phase 2 adapter extraction not yet started |
