# MCP Bootstrap Server — Replacing `./commands/*` Symlinks

**TASK-059 | Area: (4) MCP server replacing symlink approach**

## Current State: The Symlink Problem

The current bootstrap mechanism relies on symlinks in `./commands/`:

```
commands/
  boss.check.md    -> /home/jsell/.claude/commands/boss.check.md
  boss.ignite.md   -> /home/jsell/.claude/commands/boss.ignite.md
  boss.plan.md     -> /home/jsell/.claude/commands/boss.plan.md
```

These symlinks allow Claude Code to discover slash commands like `/boss.check` and
`/boss.ignite`. Problems:

1. **Fragile**: Symlinks break when the repo is cloned to a different path
2. **tmux-only**: The ambient backend sends initial prompts directly — it cannot "type"
   a slash command. There is no equivalent bootstrap mechanism for non-tmux backends
3. **Invisible**: The coordinator has no visibility into what commands are available or
   what version they are
4. **Out-of-band**: Commands live outside the coordinator data model. When the server
   is updated, command files must be manually synced
5. **Not portable**: A user on a fresh machine must manually create the symlinks

## Proposed: Boss MCP Server

Replace the symlink approach with a local MCP (Model Context Protocol) server embedded
in the `boss` binary. The MCP server exposes **resources** that any MCP-compatible AI
backend reads on startup — no slash commands required.

### Library

Use **[mark3labs/mcp-go](https://github.com/mark3labs/mcp-go)** — the idiomatic Go MCP
server library. This is the first external Go dependency added to the server, justified
because implementing MCP protocol from scratch (session management, capability negotiation,
streaming) is significant boilerplate.

### Transport: HTTP (Streamable)

Use the **HTTP streamable transport** (SSE transport is deprecated in MCP spec).
The MCP server shares port 8899 with the coordinator API, mounted under `/mcp`.

```
POST /mcp    — JSON-RPC over HTTP (MCP streamable HTTP transport)
```

Authentication: none (localhost only; same trust as the rest of the API).

### Resources

Reference `.spec/CollaborationProtocol` for full protocol context. Key resources exposed:

| URI | Description |
| --- | ----------- |
| `boss://bootstrap/{space}/{agent}` | Assembled bootstrap instructions for a specific agent |
| `boss://protocol` | The agent collaboration protocol (AGENT_PROTOCOL.md content) |
| `boss://space/{space}/blackboard` | Current blackboard state for a space (raw text) |

The bootstrap resource (`boss://bootstrap/{space}/{agent}`) contains:

```
[global persona prompts, in order]

You are {agent} in space {space}.
Coordinator: http://localhost:8899
Space: {space}
Session: {session_id}

[agent initial_prompt, if set in AgentConfig]

--- Current Status ---
[condensed blackboard section for this agent]
```

This replaces the need for slash command files entirely. The AI reads the bootstrap
resource on startup and has everything it needs to begin work.

### Injecting MCP Config into Agent Sessions

The server registers the boss MCP server with Claude **before** starting the agent.
For the tmux backend:

```bash
# 1. Register boss MCP server (idempotent — safe if already registered)
claude mcp add boss-mcp --transport http http://localhost:8899/mcp

# 2. Start Claude (MCP server auto-connects on startup)
claude --dangerously-skip-permissions
```

The tmux backend workflow:
1. `tmux new-session -d -s {session_id}` — create session
2. Send: `claude mcp add boss-mcp --transport http http://localhost:8899/mcp` + Enter
3. Wait 300ms
4. Send: `claude --dangerously-skip-permissions` + Enter
5. Claude initializes, reads `boss://bootstrap/{space}/{agent}`, and begins work

The server checks `claude mcp list` output to detect if `boss-mcp` is already registered
before running `claude mcp add` (avoids duplicates).

For the **ambient backend**: pass the MCP server URL in `AmbientCreateOpts`. Ambient
backends support MCP configuration natively in their session creation API.

### Slash Command Files — Removal

The `./commands/` directory and its `.md` files are **deleted** — not deprecated.
There is no backward compatibility period. Agents that previously relied on `/boss.check`,
`/boss.ignite`, or `/boss.plan` slash commands receive equivalent instructions through
the MCP bootstrap resource.

---

## MCP Server Implementation Notes

- Use `mark3labs/mcp-go` for the MCP server implementation
- HTTP streamable transport on `POST /mcp`, same port as coordinator (8899)
- Resources are generated dynamically from coordinator state (not static files)
- The bootstrap resource reuses the existing ignition logic internally
- The protocol resource embeds `AGENT_PROTOCOL.md` at build time via `//go:embed`
- The blackboard resource reuses the existing `/raw` rendering logic internally
- No authentication (localhost trust model)
