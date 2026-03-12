# Hexagonal Architecture Design — agent-boss

## 1. Current State

`internal/coordinator/` is a monolith (~3 500 lines across ~35 files) that mixes
four distinct concerns in a single package:

| File(s) | Concern |
|---|---|
| `server.go`, `handlers_*.go` | HTTP routing and request handling |
| `types.go` | Domain types AND business logic (markdown rendering, hierarchy, staleness) |
| `db_adapter.go`, `storage.go` | SQLite persistence via GORM |
| `mcp_server.go`, `mcp_tools.go` | MCP server (Model Context Protocol) |
| `handlers_sse.go`, `journal.go` | Server-Sent Events streaming |
| `session_backend*.go`, `tmux.go` | Agent lifecycle (tmux, ambient) |
| `client.go` | HTTP client for programmatic access |
| `deck.go` | Multi-space deck management |

**Consequences of the monolith:**
- All external dependencies (GORM, sqlite, MCP SDK) are imported unconditionally.
- Business logic (e.g., staleness computation, hierarchy traversal) cannot be unit-tested
  without standing up the entire HTTP server and a SQLite database.
- Adding a new persistence backend (e.g. Postgres, in-memory) requires changing `server.go`.
- The test suite is integration-heavy (`server_test.go` starts a real HTTP server).

---

## 2. Target Architecture

The target follows the **Ports and Adapters** (hexagonal) pattern:

```
cmd/boss/
  main.go              # Composition root — wires adapters to domain

internal/
  domain/              # Pure business logic. Imports ONLY stdlib.
    types.go           # Space, Agent, Task, Message, KnowledgeSpace
    ports/
      storage.go       # StoragePort interface (outbound)
      session.go       # SessionPort interface (outbound, future)
      events.go        # EventPort interface (outbound, future)

  adapters/            # Infrastructure implementations of ports.
    sqlite/            # Implements StoragePort using GORM + glebarez/sqlite
    http/              # HTTP handlers — inbound adapter (calls domain)
    sse/               # SSE streaming — inbound adapter
    mcp/               # MCP server — inbound adapter

  coordinator/         # COMPOSITION ROOT ONLY — wires everything together.
                       # After migration: ~100 lines max.
```

### Package dependency diagram

```
cmd/boss/main.go
  └── internal/coordinator  (composition root)
        ├── internal/domain         (domain types + ports)
        ├── internal/adapters/sqlite
        ├── internal/adapters/http
        ├── internal/adapters/sse
        └── internal/adapters/mcp

internal/adapters/*  -->  internal/domain  (adapters implement ports)
internal/domain      -->  stdlib only
```

---

## 3. Package Definitions

### `internal/domain/`

**Purpose:** Pure business types and outbound port interfaces.

**Contents:**
- `types.go` — `Space`, `AgentRecord`, `AgentStatus`, `AgentConfig`, `Task`,
  `Message`, `StatusSnapshot`, `SpaceEvent`, `Interrupt`
- `ports/storage.go` — `StoragePort` interface (all persistence operations)
- `ports/session.go` — `SessionPort` interface (agent lifecycle, future phase)
- `ports/events.go` — `EventPort` interface (SSE broadcasting, future phase)

**Rules:**
- Imports **nothing** outside the Go standard library.
- Contains **no** GORM struct tags, HTTP handlers, or network calls.
- Business logic (staleness, hierarchy) may live here once extracted from types.go.

### `internal/adapters/sqlite/`

**Purpose:** Implements `StoragePort` using GORM + glebarez/sqlite (or Postgres).

**Boundary rules:**
- May import `internal/domain` and `internal/domain/ports`.
- Must **not** import any other adapter (`http/`, `sse/`, `mcp/`).
- May import `gorm.io/gorm`, `github.com/glebarez/sqlite`, etc.

### `internal/adapters/http/`

**Purpose:** HTTP routing and request handlers (inbound adapter).

**Boundary rules:**
- May import `internal/domain` and `internal/domain/ports`.
- Must **not** import `internal/adapters/sqlite` or `internal/adapters/sse` directly.
  It receives a `StoragePort` and `EventPort` through constructor injection.

### `internal/adapters/sse/`

**Purpose:** Server-Sent Events streaming (inbound adapter).

**Boundary rules:**
- Same as `http/` — no cross-adapter imports.

### `internal/adapters/mcp/`

**Purpose:** MCP server and tool definitions (inbound adapter).

**Boundary rules:**
- Same as `http/` — no cross-adapter imports.

### `internal/coordinator/` (composition root)

**Purpose:** Wire adapters to domain and start the server. After Phase 3 this
package should contain ~100 lines: construct adapters, inject dependencies, call `.Start()`.

---

## 4. Import Rules

| Package | May import | Must NOT import |
|---|---|---|
| `domain/` | stdlib only | anything with a dot in the first path element |
| `domain/ports/` | stdlib, `domain/` | adapter packages, GORM, HTTP, etc. |
| `adapters/sqlite/` | stdlib, `domain/`, `domain/ports/`, GORM | other adapter packages |
| `adapters/http/` | stdlib, `domain/`, `domain/ports/`, `net/http` | sqlite/, sse/, mcp/ |
| `adapters/sse/` | stdlib, `domain/`, `domain/ports/` | sqlite/, http/, mcp/ |
| `adapters/mcp/` | stdlib, `domain/`, `domain/ports/`, MCP SDK | sqlite/, http/, sse/ |
| `coordinator/` (root) | all of the above | nothing — it IS the wiring point |

These rules are encoded as executable tests in `internal/domain/architecture_test.go`.

---

## 5. Migration Phases

### Phase 1 — Foundation (this PR)

**Goal:** Establish the domain package and prove the boundary rules are testable.

Deliverables:
1. `internal/domain/types.go` — pure domain structs (no GORM/JSON tags).
2. `internal/domain/ports/storage.go` — `StoragePort` interface (interface only).
3. `internal/domain/architecture_test.go` — boundary tests that document the
   current baseline (domain/ PASSES; coordinator/ FAILS as expected).
4. This design document.

**No existing code is changed.** `go test -race ./...` continues to pass.

### Phase 2 — Adapter Extraction

**Goal:** Pull infrastructure out of `coordinator/` into distinct adapter packages.

Steps:
1. Create `internal/adapters/sqlite/` and implement `StoragePort` by wrapping
   `internal/coordinator/db.Repository`. The GORM models stay in `db/`; the adapter
   translates between db models and domain types.
2. Create `internal/adapters/http/` holding the HTTP handlers from `handlers_*.go`.
   Handlers receive `StoragePort` (and future `EventPort`) via constructor injection.
3. Create `internal/adapters/sse/` from `handlers_sse.go` + `journal.go`.
4. Create `internal/adapters/mcp/` from `mcp_server.go` + `mcp_tools.go`.
5. Update boundary tests — all four adapter packages should now PASS isolation rules.

**Constraint:** At the end of Phase 2, `go test -race ./...` must still pass.
No API surface changes.

### Phase 3 — Composition Root

**Goal:** Reduce `internal/coordinator/` to a thin wiring layer.

Steps:
1. Delete the migrated code from `coordinator/` (handlers, storage, SSE, MCP).
2. Rewrite `coordinator/server.go` as a composition root: construct adapters,
   inject `StoragePort` into HTTP and MCP adapters, wire SSE fan-out.
3. `cmd/boss/main.go` calls `coordinator.NewServer()` as before — no CLI changes.
4. Update boundary tests — `coordinator/` should import only adapters + domain.
5. All existing tests pass; add new unit tests for domain logic without a real DB.

**Outcome:** Business logic (staleness, hierarchy) can be tested with `go test ./internal/domain/`
in under 1 second, no database required.

---

## 6. Boundary Test Baseline (Phase 1)

Run `go test -v ./internal/domain/` to see the current compliance:

| Rule | Package | Status (Phase 1 baseline) |
|---|---|---|
| domain/ imports stdlib only | `internal/domain` | **PASS** — newly created, zero external imports |
| domain/ports/ imports only stdlib + domain | `internal/domain/ports` | **PASS** — newly created |
| coordinator/ has no external imports | `internal/coordinator` | **FAIL** (expected) — imports gorm, sqlite, MCP SDK, etc. |
| adapters/ are isolated from each other | `internal/adapters/*` | **N/A** — adapters don't exist yet (Phase 2) |

The `TestCoordinatorImportBaseline` test logs the external imports without failing,
recording the starting point. The `TestAdapterIsolationBaseline` test skips gracefully
when no adapter packages exist.
