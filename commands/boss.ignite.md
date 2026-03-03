You are joining a multi-agent coordination project. Execute these steps exactly.

**Arguments:** `$ARGUMENTS` is `<your-agent-name> <space-name>` (two words, space-separated). Parse them: the FIRST word is your agent name, the SECOND word is the workspace/space name. If only one word is provided, it is the space name — determine your agent name from your tmux session or ask the human.

## Step 1: Get your tmux session name

```bash
tmux display-message -p '#S'
```

Save this value — you will need it in Step 2.

## Step 2: Fetch your ignition prompt

Using your agent name (first word of `$ARGUMENTS`), the space name (second word of `$ARGUMENTS`), and your tmux session from Step 1:

```bash
curl -s "http://localhost:8899/spaces/SPACE_NAME/ignition/AGENT_NAME?tmux_session=YOUR_TMUX_SESSION"
```

For example, if `$ARGUMENTS` is `Overlord sdk-backend-replacement` and your tmux session is `agentdeck_Overlord_abc123`:

```bash
curl -s "http://localhost:8899/spaces/sdk-backend-replacement/ignition/Overlord?tmux_session=agentdeck_Overlord_abc123"
```

This registers your tmux session with the coordinator and returns your identity, peer agents, the full protocol, and a POST template.

## Step 3: Read the blackboard

```bash
curl -s http://localhost:8899/spaces/SPACE_NAME/raw
```

This shows what every agent is doing, what decisions have been made, and what standing orders exist.

## Step 4: Post your initial status

Using the protocol and template from Step 2, post your initial status to your channel. Include `status`, `summary`, `branch`, `items`, and `next_steps`.

## Rules

- **Never contradict shared contracts** — these are agreed API surfaces and architectural decisions all agents must respect.
- **Tag questions with `[?BOSS]`** when you need the human to make a decision.
- **Post to your own channel only** — the server rejects cross-channel posts.
- **Do NOT include `tmux_session` in your POST** — it was pre-registered in Step 2 and is sticky.
