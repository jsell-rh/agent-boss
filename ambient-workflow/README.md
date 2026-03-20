# OpenDispatch Coordination Workflow

ACP workflow that equips remote agent sessions with the commands and protocol needed to participate in the OpenDispatch multi-agent coordination system.

## What This Provides

When attached to an ACP session, this workflow:

1. **Injects slash commands** (`/boss.plan`, `/boss.check`, `/boss.ignite`) that agents use to coordinate
2. **Sets a system prompt** with the full OpenDispatch protocol (golden rules, API reference, status format)
3. **Provides behavioral guidelines** via `CLAUDE.md` for safe multi-agent operation

## Commands

| Command | Purpose |
|---------|---------|
| `/boss.ignite <agent> <space>` | Bootstrap into the multi-agent system — orient, register, read blackboard, post initial status |
| `/boss.check <agent> <space>` | Mechanical status sync — fetch messages, post status, act on directives |
| `/boss.plan <spec>` | Create a factory plan from a specification with agent stages and quality gates |

## Required Environment Variables

These must be set on the ACP session for commands to work:

| Variable | Description | Example |
|----------|-------------|---------|
| `ODIS_URL` | Coordinator API base URL | `https://odispatch.apps.example.com` |
| `AGENT_NAME` | Agent identity | `ProtocolDev` |

## Usage with ACP

### Automatic (via backend config)

When the OpenDispatch coordinator is configured with `AMBIENT_WORKFLOW_*` environment variables, every agent session spawned through the ambient backend automatically gets this workflow attached. See the deployment configmap for details.

### Manual (via ACP API)

```json
{
  "task": "Your task prompt here",
  "activeWorkflow": {
    "gitUrl": "https://github.com/jsell-rh/agent-boss",
    "branch": "main",
    "path": "ambient-workflow"
  },
  "environmentVariables": {
    "ODIS_URL": "https://odispatch.apps.example.com",
    "AGENT_NAME": "MyAgent"
  }
}
```

## Directory Structure

```
ambient-workflow/
  .ambient/
    ambient.json          # Workflow manifest (name, systemPrompt, startupPrompt)
  .claude/
    commands/
      boss.plan.md        # Factory plan creation
      boss.check.md       # Status sync procedure
      boss.ignite.md      # Agent bootstrap procedure
    settings.json         # Pre-grants odis-mcp tool permissions
  .mcp.json               # Registers odis-mcp MCP server
  CLAUDE.md               # Behavioral guidelines
  README.md               # This file
```
