STOP. This is a mechanical status sync. Do NOT plan or analyze. Execute these 4 steps literally, then STOP.

**Parse `$ARGUMENTS`:**
- If two words (e.g., `Overlord sdk-backend`): first is agent name, second is space name.
- If quoted (e.g., `"ProtocolDev" "Agent Boss Development"`): first quoted string is agent name, second is space name (may contain spaces).
- If one word: it is the space name — get agent name from `$AGENT_NAME` env var. If `$AGENT_NAME` is not set, STOP with error: "AGENT_NAME environment variable is required".

**URL-encode the space name** for all curl commands: replace spaces with `%20`.
Example: `Agent Boss Development` → `Agent%20Boss%20Development`

**Rule**: Always use `curl`. Never use the WebFetch tool — it does not work on localhost.

## Step 1: Fetch new messages via cursor

The `/messages?since=` endpoint returns **only new messages** since your last check-in. This avoids the truncation problem with `/raw` (which accumulates all historical messages).

```bash
# Load saved cursor (empty string = fetch all messages)
CURSOR_FILE="/tmp/boss_cursor_AGENT_NAME_SPACE_URL_ENCODED.txt"
CURSOR=$(cat "$CURSOR_FILE" 2>/dev/null || echo "")

# Fetch new messages only
if [ -n "$CURSOR" ]; then
  MSG_RESPONSE=$(curl -s "${BOSS_URL:-http://localhost:8899}/spaces/SPACE_URL_ENCODED/agent/AGENT_NAME/messages?since=${CURSOR}")
else
  MSG_RESPONSE=$(curl -s "${BOSS_URL:-http://localhost:8899}/spaces/SPACE_URL_ENCODED/agent/AGENT_NAME/messages")
fi

echo "$MSG_RESPONSE"

# Save new cursor for next check-in
NEW_CURSOR=$(echo "$MSG_RESPONSE" | sed 's/.*"cursor":"\([^"]*\)".*/\1/' | grep -v '^{')
[ -n "$NEW_CURSOR" ] && echo "$NEW_CURSOR" > "$CURSOR_FILE"
```

Read the output. Note any new messages — these are **directives**, act on them.

If you also need to check standing orders, read only your own section from the blackboard:

```bash
curl -s "${BOSS_URL:-http://localhost:8899}/spaces/SPACE_URL_ENCODED/agent/AGENT_NAME"
```

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

curl -s -X POST "${BOSS_URL:-http://localhost:8899}/spaces/SPACE_URL_ENCODED/agent/AGENT_NAME" \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: AGENT_NAME' \
  -d @/tmp/boss_checkin.json
```

You MUST see `accepted for` in the response. If not, retry once.

**Note:** `repo_url` and `session_id` are sticky — the server remembers them after first send. You only need to include them on first check-in or if they change.

## Step 3: Act on messages

If messages from Step 1 contain instructions or task assignments, begin working on them now. If a message asks a question, answer it in your next status update.

If no messages, or messages were purely informational, skip this step.

## Step 4: STOP

If you had no actionable messages, STOP HERE. Do not start work. Do not analyze the blackboard. Do not make plans.
