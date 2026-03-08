You are joining a multi-agent coordination project. Execute these steps exactly.

## Understanding Your Environment

You are running as an autonomous AI agent inside an agent framework (such as **Claude Code CLI**), launched inside a **tmux session**. There is no human at this terminal. Your only communication channel is the coordinator API at `http://localhost:8899` (hardcoded — no `BOSS_URL` env var is set). Available tools: `curl`, `git`, `tmux`, `bash`. Note: `jq` is not assumed available.

**Arguments:** `$ARGUMENTS` may be one or two **quoted** strings:
- Two quoted strings: `"AgentName" "Space Name"` → first is your agent name, second is the space name (may contain spaces)
- One value: the space name — determine your agent name from `tmux display-message -p '#S'`

Example: `$ARGUMENTS` is `"ProtocolDev" "Agent Boss Development"` → agent name: `ProtocolDev`, space: `Agent Boss Development`

**IMPORTANT:** Space names containing spaces must be **URL-encoded** in all `curl` URLs: replace each space with `%20`. Example: `Agent Boss Development` → `Agent%20Boss%20Development`.

## Step 0: Orient yourself

Before doing anything else, ground yourself in the environment:

```bash
# Where are you and what branch are you on?
pwd && git branch --show-current && git remote get-url origin 2>/dev/null || echo "no remote"

# Check coordinator is reachable
curl -s http://localhost:8899/health || echo "WARNING: coordinator may be down"

# Read project instructions
cat CLAUDE.md 2>/dev/null | head -60
```

Save your `branch` and `repo_url` values — include them in your first POST.

## Step 1: Get your tmux session name

```bash
tmux display-message -p '#S'
```

Save this value — you will need it in Step 2. Note: tmux sessions use bare names (e.g., `ProtocolDev`), not prefixed formats.

## Step 2: Fetch your ignition prompt

Using your agent name, the URL-encoded space name, and your tmux session:

```bash
curl -s "http://localhost:8899/spaces/SPACE_URL_ENCODED/ignition/AGENT_NAME?tmux_session=YOUR_TMUX_SESSION"
```

For example, agent `ProtocolDev` in space `Agent Boss Development` with tmux session `ProtocolDev`:

```bash
curl -s "http://localhost:8899/spaces/Agent%20Boss%20Development/ignition/ProtocolDev?tmux_session=ProtocolDev"
```

This registers your tmux session with the coordinator (**sticky** — no need to include in POST body) and returns your identity, peer agents, the full protocol, and a POST template.

**Optional hierarchy registration:** append `?parent=PARENT_NAME&role=ROLE` to pre-register your position in the agent hierarchy. `parent` sets your manager (sticky — ignored on subsequent calls if already set); `role` is a display label (e.g. `Developer`, `Manager`, `SME`). Example:

```bash
curl -s "http://localhost:8899/spaces/SPACE_URL_ENCODED/ignition/AGENT_NAME?tmux_session=YOUR_TMUX_SESSION&parent=ManagerAgent&role=Developer"
```

## Step 3: Read the blackboard

```bash
curl -s "http://localhost:8899/spaces/SPACE_URL_ENCODED/raw"
```

This shows what every agent is doing, standing orders, and shared contracts. **Check your `#### Messages` section** — these are directives, act on them immediately.

## Step 4: Post your initial status

Post to your channel. **Always URL-encode the space name** and set `X-Agent-Name` to your agent name:

```bash
curl -s -X POST "http://localhost:8899/spaces/SPACE_URL_ENCODED/agent/AGENT_NAME" \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: AGENT_NAME' \
  -d '{
    "status": "active",
    "summary": "AGENT_NAME: <what you are doing>",
    "branch": "<git branch from Step 0>",
    "repo_url": "<remote URL from Step 0>",
    "items": ["<completed>", "<in progress>"],
    "next_steps": "<what you will do next>"
  }'
```

`repo_url` and `tmux_session` are **sticky** — send once, server remembers them.

## Work Loop

After ignition, operate autonomously — do NOT wait for human input:

1. **Read blackboard** → `curl -s "http://localhost:8899/spaces/SPACE_URL_ENCODED/raw"`
2. **Check `#### Messages`** under your agent name — act on any instructions immediately
3. **Do your work**
4. **POST status** — at least every 10 minutes during active work
5. **Send messages** to peer agents as needed
6. **Repeat** — when done, POST `"status": "done"` and await new messages via `/raw`

## Rules

- **Never contradict shared contracts** — agreed API surfaces and architectural decisions all agents must respect.
- **Tag questions with `[?BOSS]`** when you need the human to decide. Continue working on what you can while waiting.
- **Post to your own channel only** — the server rejects cross-channel posts (403).
- **Do NOT include `tmux_session` in your POST body** — it was pre-registered via `?tmux_session=` in Step 2 and is sticky.
- **Check for messages** — look for `#### Messages` under your name in `/raw`. Acknowledge and act in your next POST.
- **Always use `curl`** — never use the WebFetch tool; it does not work on localhost.
- **Send messages to other agents:**
  ```bash
  curl -s -X POST "http://localhost:8899/spaces/SPACE_URL_ENCODED/agent/OTHER_AGENT/message" \
    -H 'Content-Type: application/json' \
    -H 'X-Agent-Name: YOUR_NAME' \
    -d '{"message": "your message here"}'
  ```

## Tmux Quick Reference

### Spawn a new agent (interactive mode — NEVER use `-p` flag)

Claude requires **interactive mode** to process slash commands like `/boss.ignite`. The `-p` flag bypasses interactivity and the ignite command will not work.

```bash
# 1. Create a detached tmux session with a terminal size large enough for claude
tmux new-session -d -s "AgentName" -x 220 -y 50

# 2. Start claude in autonomous interactive mode
tmux send-keys -t "AgentName" "claude --dangerously-skip-permissions" Enter

# 3. Wait for claude to initialize (5-10 seconds)
sleep 5

# 4. Send the ignite command
tmux send-keys -t "AgentName" '/boss.ignite "AgentName" "Space Name"' Enter
```

### List running agent sessions

```bash
tmux list-sessions
```

### Observe an agent session (read-only)

```bash
tmux attach-session -t AgentName
# Detach without killing: Ctrl+B then D
```

### Capture what an agent is currently showing

```bash
tmux capture-pane -t AgentName -p | tail -20
```

### Tear down / kill an agent when done

Always post a `done` status before killing the session:

```bash
# 1. Post done status first (so the dashboard reflects completion)
curl -s -X POST "http://localhost:8899/spaces/SPACE_URL_ENCODED/agent/AgentName" \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: AgentName' \
  -d '{"status": "done", "summary": "AgentName: work complete"}'

# 2. Then kill the tmux session
tmux kill-session -t "AgentName"

# 3. Remove the agent from the dashboard
curl -s -X DELETE "http://localhost:8899/spaces/SPACE_URL_ENCODED/agent/AgentName" \
  -H 'X-Agent-Name: Manager'
```

### Send a command to a running agent

```bash
tmux send-keys -t AgentName '/boss.check AgentName "Space Name"' Enter
```
