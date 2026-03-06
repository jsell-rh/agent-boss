STOP. This is a mechanical status sync. Do NOT plan or analyze. Execute these 4 steps literally, then STOP.

**Parse `$ARGUMENTS`:**
- If two words (e.g., `Overlord sdk-backend`): first is agent name, second is space name.
- If quoted (e.g., `"ProtocolDev" "Agent Boss Development"`): first quoted string is agent name, second is space name (may contain spaces).
- If one word: it is the space name — get agent name from `tmux display-message -p '#S'`.

**URL-encode the space name** for all curl commands: replace spaces with `%20`.  
Example: `Agent Boss Development` → `Agent%20Boss%20Development`

## Step 1: Read the blackboard

```bash
curl -s "http://localhost:8899/spaces/SPACE_URL_ENCODED/raw"
```

Scan your section (`### YourAgentName`) for:
- **`#### Messages`** — messages from the boss or other agents. Note instructions.
- **Standing orders** — anything in shared contracts addressed to you.

Do NOT analyze other agents' sections.

**Rule**: Always use `curl`. Never use the WebFetch tool — it does not work on localhost.

## Step 2: Write your status JSON and POST it

Create `/tmp/boss_checkin.json` reflecting your CURRENT state. Do not change your work — just report it.

If you found messages in Step 1, acknowledge them in your `items` array.

```bash
cat > /tmp/boss_checkin.json << 'CHECKIN'
{
  "status": "active",
  "summary": "AGENT_NAME: <one-line description of what you are doing>",
  "branch": "<your current git branch or empty string>",
  "pr": "<open MR number e.g. #748 or empty string>",
  "repo_url": "<full HTTPS URL e.g. https://github.com/org/repo — sticky, send once>",
  "phase": "<current phase or empty string>",
  "test_count": 0,
  "items": ["<what you have done or are doing>"],
  "next_steps": "<what you will do next>"
}
CHECKIN

curl -s -X POST "http://localhost:8899/spaces/SPACE_URL_ENCODED/agent/AGENT_NAME" \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: AGENT_NAME' \
  -d @/tmp/boss_checkin.json
```

You MUST see `accepted for` in the response. If not, retry once.

**Note:** `repo_url` and `tmux_session` are sticky — the server remembers them after first send. You only need to include them on first check-in or if they change.

## Step 3: Act on messages

If messages in Step 1 contain instructions or task assignments, begin working on them now. If a message asks a question, answer it in your next status update.

If no messages, or messages were purely informational, skip this step.

## Step 4: STOP

If you had no actionable messages, STOP HERE. Do not start work. Do not analyze the blackboard. Do not make plans.
