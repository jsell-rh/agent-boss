# Agent Experience Surface

**Status:** Enforced
**Enforcement:** `TestAgentExperienceSurfaceInvariants` in `internal/coordinator/lint_test.go`
**See also:** [auth-model.md](auth-model.md)

## Purpose

This document defines the **formal contract** for everything the coordinator must deliver to an agent at spawn time. It was introduced after TASK-015 (PR #155) revealed that BOSS_API_TOKEN auth was added to the HTTP layer but the MCP registration command at agent spawn time never received the `--header` flag — leaving all agents silently broken when auth was enabled.

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
- **Why:** When `BOSS_API_TOKEN` is set, all mutating endpoints require auth. Without the header, every MCP tool call returns 401.
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
// CORRECT: both fields set together
BackendOpts: TmuxCreateOpts{
    MCPServerURL:  s.localURL(),
    MCPServerName: s.mcpServerName(),
    AgentToken:    s.apiToken,   // required when MCPServerURL is set
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
             AgentToken:   s.apiToken,         // [2] auth token
         }
     })
       └─ TmuxSessionBackend.CreateSession()
            ├─ cd <workDir>
            ├─ claude mcp add boss-mcp --transport http <url>/mcp
            │       --header 'Authorization: Bearer <token>'
            └─ <command>  (e.g. "claude --dangerously-skip-permissions")

  └─ waitForIdle() then buildIgnitionText()   // [4] protocol reference
       └─ backend.SendInput(sessionID, ignitePrompt)
```

---

## Adding a New Spawn Site

If you add a new location that creates a `TmuxCreateOpts` with `MCPServerURL`:

1. Set `AgentToken: s.apiToken` alongside it.
2. Run `go test ./internal/coordinator/` — `TestAgentExperienceSurfaceInvariants` will fail if you forget.
3. Update this doc if the spawn flow changes structurally.
