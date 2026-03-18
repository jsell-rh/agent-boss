# Agent Experience Surface

**Status:** Enforced
**Enforcement:** `TestAgentExperienceSurfaceInvariants` in `internal/coordinator/lint_test.go`
**See also:** [auth-model.md](auth-model.md)

## Purpose

This document defines the **formal contract** for everything the coordinator must deliver to an agent at spawn time. It was introduced after TASK-015 (PR #155) revealed that ODIS_API_TOKEN auth was added to the HTTP layer but the MCP registration command at agent spawn time never received the `--header` flag — leaving all agents silently broken when auth was enabled.

The lesson: _encode invariants mechanically, not in documentation alone._

---

## The Agent Experience Contract

Every spawned agent **must** receive all of the following:

### 1. MCP Server URL

- **What:** The URL of the Boss coordinator MCP endpoint.
- **How:** `TmuxCreateOpts.MCPServerURL` → `claude mcp add <name> --transport http <url>/mcp`
- **Why:** Without this, the agent cannot call any MCP tools (`post_status`, `check_messages`, etc.).

### 2. MCP Auth Header

- **What:** `Authorization: Bearer <token>` header passed to `claude mcp add`.
- **How:** `TmuxCreateOpts.AgentToken` → `--header 'Authorization: Bearer <token>'`
- **Why:** When `ODIS_API_TOKEN` is set, all mutating endpoints require auth. Without the header, every MCP tool call returns 401.
- **Per-agent tokens (SEC-006 / PR #242):** Each agent receives a unique token minted at spawn time via `generateAgentToken(spaceName, agentName)`. The token is isolated to that agent's channel — it cannot post to a sibling agent's endpoint. The workspace `ODIS_API_TOKEN` remains the operator-level credential; per-agent tokens are scoped narrower.
- **Invariant:** If `MCPServerURL` is set, `AgentToken` **must** also be set. The structural test `TestAgentExperienceSurfaceInvariants` enforces this at build time.

### 3. Working Directory

- **What:** The filesystem path the agent should operate in.
- **How:** `TmuxCreateOpts.WorkDir` → `cd <workDir>` before launching.
- **Why:** Agents reference local files and run git commands; the cwd determines what they can see.

### 4. Protocol Reference

- **What:** The ignition prompt — the full agent protocol document — sent to the agent after launch.
- **How:** `buildIgnitionText()` in `server.go`, sent via `backend.SendInput()` after the session reaches idle.
- **Why:** The ignition prompt tells the agent its name, workspace, MCP tools, collaboration norms, and task assignments. Without it, the agent has no context.

---

## Invariant: MCPServerURL implies AgentToken

The following rule is mechanically enforced:

> Any `TmuxCreateOpts` struct literal that sets `MCPServerURL` must also set `AgentToken`.

**Rationale:** These two fields are logically coupled — you cannot register an authenticated MCP server without also passing auth credentials. Omitting one silently breaks agent connectivity when auth is enabled.

**Enforcement:** `TestAgentExperienceSurfaceInvariants` in `internal/coordinator/lint_test.go` parses all Go source files in `internal/coordinator/` and fails the build if any `TmuxCreateOpts` literal sets `MCPServerURL` without `AgentToken`.

**To satisfy the invariant:**

```go
// CORRECT: both fields set together (per-agent token since SEC-006 / PR #242)
BackendOpts: TmuxCreateOpts{
    MCPServerURL:  s.localURL(),
    MCPServerName: s.mcpServerName(),
    AgentToken:    s.generateAgentToken(spaceName, agentName),   // required when MCPServerURL is set
}

// WRONG: auth token missing — will fail TestAgentExperienceSurfaceInvariants
BackendOpts: TmuxCreateOpts{
    MCPServerURL: s.localURL(),
    // AgentToken omitted — breaks agent MCP auth silently
}
```

---

## How Spawn Delivers the Contract

The spawn flow in `lifecycle.go` / `handlers_agent.go`:

```
spawnAgentService()
  └─ backend.CreateSession(ctx, SessionCreateOpts{
         BackendOpts: TmuxCreateOpts{
             WorkDir:      spawnWorkDir,       // [3] working directory
             MCPServerURL: s.localURL(),       // [1] MCP URL
             AgentToken:   s.generateAgentToken(spaceName, agentName), // [2] per-agent auth token (SEC-006)
         }
     })
       └─ TmuxSessionBackend.CreateSession()
            ├─ cd <workDir>
            ├─ claude mcp add odis-mcp --transport http <url>/mcp
            │       --header 'Authorization: Bearer <token>'
            └─ <command>  (e.g. "claude --dangerously-skip-permissions")

  └─ waitForIdle() then buildIgnitionText()   // [4] protocol reference
       └─ backend.SendInput(sessionID, ignitePrompt)
```

---

## Adding a New Spawn Site

If you add a new location that creates a `TmuxCreateOpts` with `MCPServerURL`:

1. Set `AgentToken: s.generateAgentToken(spaceName, agentName)` alongside it. Do **not** use the global `s.apiToken` — each agent must receive its own isolated token (SEC-006).
2. Run `go test ./internal/coordinator/` — `TestAgentExperienceSurfaceInvariants` will fail if you forget.
3. Update this doc if the spawn flow changes structurally.

---

## Dev Agent Experience Surface

Agents spawned via `make dev-spawn AGENT=<name> SPACE=<space>` (or `scripts/spawn-dev-agent.sh`) receive an **extended surface** on top of the standard contract above.

### Two MCP servers

```json
{
  "mcpServers": {
    "odis-mcp": {
      "type": "http",
      "url": "http://localhost:8899/mcp",
      "headers": {"Authorization": "Bearer <ODIS_API_TOKEN>"}
    },
    "boss-dev": {
      "type": "http",
      "url": "http://localhost:<DEV_PORT>/mcp"
    }
  }
}
```

| Server | What it connects to | When to use it |
|--------|---------------------|----------------|
| `odis-mcp` | Production coordinator (`data/boss.db`) | Check in, post status, tasks, messages |
| `boss-dev` | Local isolated dev instance (`data-dev/boss.db`) | Test API changes against the branch's binary |

The dev instance port is auto-assigned (≥ 9000) and stored in `data-dev/boss.port`. `spawn-dev-agent.sh` reads this file after running `make dev-start`.

### Extended capabilities

| Capability | How |
|------------|-----|
| Check in / tasks / messages | `odis-mcp.*` tools |
| Test API changes locally | `boss-dev.*` tools |
| Observe running sessions | `boss-observe.*` tools (when registered) |
| Rebuild and redeploy binary | `make dev-restart` |
| Full Playwright e2e | `make e2e` |
| E2e against dev instance | `make e2e-dev` |
| Capture UI screenshots | `make e2e-screenshots` |
| Interactive browser | Playwright MCP (optional) |

### Isolation guarantee

`data-dev/boss.db` is completely separate from `data/boss.db`. Dev agents experiment freely without affecting shared production state or other agents' dashboard visibility.

### Spawn flow for dev agents

```
make dev-spawn AGENT=worker1 SPACE="OpenDispatch Dev"
  └─ scripts/spawn-dev-agent.sh worker1 "OpenDispatch Dev"
       ├─ make dev-start          (no-op if already running)
       ├─ DEV_PORT=$(cat data-dev/boss.port)
       ├─ build --mcp-config JSON with odis-mcp + boss-dev
       └─ claude --mcp-config /tmp/mcp-config.json \
                 --strict-mcp-config \
                 [--dangerously-skip-permissions if ODIS_ALLOW_SKIP_PERMISSIONS=true]
```

### Checklist before shipping spawn infrastructure changes

- [ ] All `TmuxCreateOpts` literals with `MCPServerURL` also set `AgentToken`
- [ ] `go test ./internal/coordinator/ ./internal/domain/...` passes
- [ ] `scripts/spawn-dev-agent.sh` is executable and tested
- [ ] `make dev-spawn` target exists in `Makefile`
- [ ] CLAUDE.md Dev Loop section documents `make dev-spawn`
- [ ] This file updated if the spawn flow changes
