STOP. This is a mechanical status sync. Do NOT plan or analyze. Execute these 3 steps literally, then STOP.

**Parse `$ARGUMENTS`:** `$ARGUMENTS` contains two words separated by a space. The FIRST word is your agent name. The SECOND word is the space name. Example: if `$ARGUMENTS` is `Overlord sdk-backend-replacement`, then your agent name is `Overlord` and the space name is `sdk-backend-replacement`.

If `$ARGUMENTS` contains only ONE word, it is the space name. Run `tmux display-message -p '#S'` to get your tmux session (format: `agentdeck_NAME_hash`), extract NAME, and use that as your agent name.

## Step 1: Read the blackboard

```bash
curl -s http://localhost:8899/spaces/SPACE_NAME/raw
```

Replace SPACE_NAME with the space name from `$ARGUMENTS`. Scan for anything addressed to you. Do NOT analyze other agents.

**Important rule**: Always use `curl`, never use Fetch tool.  Fetch will *not* work on localhost. **Always** use curl. This is important! 

## Step 2: Write your status JSON and POST it

Create `/tmp/boss_checkin.json` reflecting your CURRENT state. Do not change your work — just report what you are doing right now.

```bash
cat > /tmp/boss_checkin.json << 'CHECKIN'
{
  "status": "active",
  "summary": "AGENT_NAME: <one-line description of what you are currently doing>",
  "branch": "<your current git branch or empty string>",
  "pr": "<your open MR number e.g. #748 or empty string>",
  "repo_url": "<full HTTPS URL of your GitLab repo e.g. https://gitlab.cee.redhat.com/ocm/platform>",
  "phase": "<your current phase or empty string>",
  "test_count": 0,
  "items": ["<what you have done or are doing>"],
  "next_steps": "<what you will do next>"
}
CHECKIN
```

Replace AGENT_NAME with your agent name from `$ARGUMENTS`. Keep summary under 120 chars. Include `"pr"` and `"repo_url"` if you have an open merge request — the dashboard links them. Both are **sticky** (sent once, preserved automatically). Add `"blockers"` array only if you are genuinely blocked. Add `"questions"` array with `[?BOSS]` prefix only if you need the human to decide something.

Then POST it:

```bash
curl -s -X POST http://localhost:8899/spaces/SPACE_NAME/agent/AGENT_NAME \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: AGENT_NAME' \
  -d @/tmp/boss_checkin.json
```

Replace SPACE_NAME and AGENT_NAME with the values from `$ARGUMENTS`. You MUST see `accepted for` in the response. If you do not, something is wrong — retry once.

## Step 3: STOP

Do not start any work. Do not analyze the blackboard. Do not make plans. STOP HERE.
