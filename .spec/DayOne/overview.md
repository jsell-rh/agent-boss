# Day One Operation — Spec Overview

**TASK-059 | Author: LifecycleMgr | Status: Draft**

## Problem Statement

Starting a new Agent Boss project today requires manual steps that are error-prone,
non-reproducible, and invisible to the system:

1. Agents lose their working directory and initial prompts when sessions restart
2. There is no way to create an "agent template" — every agent must be configured manually
3. Duplicating a working agent configuration requires copy-pasting by hand
4. Bootstrap commands are symlinked from `./commands/` — fragile, backend-specific, invisible to non-tmux backends
5. A new user faces a blank dashboard with no guidance on what to do first

This spec defines five improvements that collectively make the first-hour experience reliable
and self-guiding.

## Spec Documents

| Document | Area |
| -------- | ---- |
| [agent-config.md](./agent-config.md) | Persist cwd/repo/initial prompts + agent duplication UX |
| [personas.md](./personas.md) | Reusable persona prompt injections |
| [mcp-bootstrap.md](./mcp-bootstrap.md) | MCP server replacing `./commands/*` symlinks |
| [day-one-ux.md](./day-one-ux.md) | Holistic new-user onboarding experience |

## Design Principles

- **Zero-friction defaults**: a new user can create a working space and agent in under 2 minutes
- **Reproducibility**: agent configuration is data, not shell state — it survives restarts
- **Backend agnosticism**: every improvement works for both tmux and ambient backends
- **One external Go dependency**: `mark3labs/mcp-go` for the MCP server (justified by protocol complexity)
- **Security by default**: `--dangerously-skip-permissions` is a global server toggle (off by default); one deliberate operator decision applies to all agents

## Shared Contracts

### AgentConfig (new top-level object)

```json
{
  "work_dir": "/home/jsell/code/sandbox/agent-boss",
  "repo_url": "https://github.com/jsell-rh/agent-boss.git",
  "initial_prompt": "You are LifecycleMgr. Read the blackboard and act on pending tasks.",
  "personas": [{"id": "senior-engineer"}],
  "backend": "tmux",
  "command": "claude"
}
```

Note: `command` defaults to `claude` (no `--dangerously-skip-permissions`). Users opt in
via the "Skip permission prompts" checkbox in the agent create dialog.

### Persona (new top-level object)

```json
{
  "id": "senior-engineer",
  "name": "Senior Engineer",
  "description": "Focuses on clean code, tests, and minimal changes",
  "prompt_injection": "You are a senior software engineer. Prefer small, focused changes..."
}
```

## Resolved Design Decisions (from boss review)

| Decision | Resolution |
| -------- | ---------- |
| Persona scope | **Global** (not space-scoped); UI shows which spaces/agents use each persona |
| Persona version re-inject | No auto-re-inject; mark stale agents with badge + quick restart action |
| Agent duplication spawn | **Auto-spawn** immediately on duplicate |
| Initial prompt default | No slash command fallback — MCP bootstrap resource provides context |
| Onboarding approach | **Inline empty states** (not wizard) |
| `boss init` scope | Registers MCP server with Claude; no `./commands/` setup |
| MCP transport | **HTTP streamable** (SSE deprecated); same port 8899 |
| MCP library | **mark3labs/mcp-go** |
| Boss identity | First-class dashboard user; not an agent record (optional only) |
| Restart scope | Individual agent + **fleet restart** (restart all) |
| `--dangerously-skip-permissions` | **Global server-wide toggle** (off by default); applies to all tmux agents uniformly |

## Non-Goals (this spec)

- Actual implementation code (this is a design spec only)
- Cloud backend persona support (tracked separately)
