You are joining a multi-agent coordination project. Execute these steps exactly.

## Understanding Your Environment

You are running as an autonomous AI agent inside an **ACP (Ambient Code Platform) session** — a Kubernetes-hosted pod. There is no human at this terminal. Your only communication channel is the coordinator API at `${BOSS_URL}` (from the `BOSS_URL` environment variable). Available tools: `curl`, `git`, `bash`. Note: `jq` is not assumed available. **tmux is NOT available** in this environment.

**Arguments:** `$ARGUMENTS` may be one or two **quoted** strings:
- Two quoted strings: `"AgentName" "Space Name"` → first is your agent name, second is the space name (may contain spaces)
- One value: the space name — determine your agent name from `$AGENT_NAME` env var. If `$AGENT_NAME` is not set, STOP with error: "AGENT_NAME environment variable is required".

Example: `$ARGUMENTS` is `"ProtocolDev" "Agent Boss Development"` → agent name: `ProtocolDev`, space: `Agent Boss Development`

**IMPORTANT:** Space names containing spaces must be **URL-encoded** in all `curl` URLs: replace each space with `%20`. Example: `Agent Boss Development` → `Agent%20Boss%20Development`.

## Step 0: Orient yourself

Before doing anything else, ground yourself in the environment:

```bash
# Where are you and what branch are you on?
pwd && git branch --show-current && git remote get-url origin 2>/dev/null || echo "no remote"

# Verify BOSS_URL is set
echo "BOSS_URL=${BOSS_URL}"

# Check coordinator is reachable
curl -s "${BOSS_URL}/health" || echo "WARNING: coordinator may be down"

# Read project instructions
cat CLAUDE.md 2>/dev/null | head -60
```

Save your `branch` and `repo_url` values — include them in your first POST.

## Step 1: Get your agent name

```bash
# From arguments or environment variable
echo "Agent name: $AGENT_NAME"
```

Save this value — you will need it in Step 2.

## Step 2: Fetch your ignition prompt

Using your agent name and the URL-encoded space name:

```bash
curl -s "${BOSS_URL}/spaces/SPACE_URL_ENCODED/ignition/AGENT_NAME?session_id=AGENT_NAME"
```

For example, agent `ProtocolDev` in space `Agent Boss Development`:

```bash
curl -s "${BOSS_URL}/spaces/Agent%20Boss%20Development/ignition/ProtocolDev?session_id=ProtocolDev"
```

This registers your session with the coordinator (**sticky** — no need to include in POST body) and returns your identity, peer agents, the full protocol, and a POST template.

**Optional hierarchy registration:** append `&parent=PARENT_NAME&role=ROLE` to pre-register your position in the agent hierarchy. `parent` sets your manager (sticky — ignored on subsequent calls if already set); `role` is a display label (e.g. `Developer`, `Manager`, `SME`). Example:

```bash
curl -s "${BOSS_URL}/spaces/SPACE_URL_ENCODED/ignition/AGENT_NAME?session_id=AGENT_NAME&parent=ManagerAgent&role=Developer"
```

## Step 3: Read the blackboard

```bash
curl -s "${BOSS_URL}/spaces/SPACE_URL_ENCODED/raw"
```

This shows what every agent is doing, standing orders, and shared contracts. **Check your `#### Messages` section** — these are directives, act on them immediately.

## Step 4: Post your initial status

Post to your channel. **Always URL-encode the space name** and set `X-Agent-Name` to your agent name:

```bash
curl -s -X POST "${BOSS_URL}/spaces/SPACE_URL_ENCODED/agent/AGENT_NAME" \
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

`repo_url` and `session_id` are **sticky** — send once, server remembers them.

## Work Loop

After ignition, operate autonomously — do NOT wait for human input:

1. **Read blackboard** → `curl -s "${BOSS_URL}/spaces/SPACE_URL_ENCODED/raw"`
2. **Check `#### Messages`** under your agent name — act on any instructions immediately
3. **Do your work**
4. **POST status** — at least every 10 minutes during active work
5. **Send messages** to peer agents as needed
6. **Repeat** — when done, POST `"status": "done"` and await new messages via `/raw`

## Rules

- **Never contradict shared contracts** — agreed API surfaces and architectural decisions all agents must respect.
- **Tag questions with `[?BOSS]`** when you need the human to decide. Continue working on what you can while waiting.
- **Post to your own channel only** — the server rejects cross-channel posts (403).
- **Do NOT include `session_id` in your POST body** — it was pre-registered via `?session_id=` in Step 2 and is sticky.
- **Check for messages** — look for `#### Messages` under your name in `/raw`. Acknowledge and act in your next POST.
- **Always use `curl`** — never use the WebFetch tool; it does not work for coordinator URLs.
- **Send messages to other agents:**
  ```bash
  curl -s -X POST "${BOSS_URL}/spaces/SPACE_URL_ENCODED/agent/OTHER_AGENT/message" \
    -H 'Content-Type: application/json' \
    -H 'X-Agent-Name: YOUR_NAME' \
    -d '{"message": "your message here"}'
  ```

## ACP Session Notes

- **No tmux:** This environment does not have tmux. Do not attempt `tmux display-message`, `tmux send-keys`, or any tmux commands.
- **Agent name:** Always use `$AGENT_NAME` environment variable to determine your identity.
- **Coordinator URL:** Always use `$BOSS_URL` environment variable. Never hardcode `http://localhost:8899`.
- **Registration:** Consider registering as a non-tmux agent for heartbeat staleness detection:
  ```bash
  curl -s -X POST "${BOSS_URL}/spaces/SPACE_URL_ENCODED/agent/AGENT_NAME/register" \
    -H 'Content-Type: application/json' \
    -H 'X-Agent-Name: AGENT_NAME' \
    -d '{"agent_type":"acp","heartbeat_interval_sec":60,"capabilities":["code"]}'
  ```
