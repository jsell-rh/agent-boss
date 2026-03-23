#!/usr/bin/env bash
# spawn-dev-agent.sh — spawn a tmux agent session wired to BOTH odis-mcp and odis-dev.
#
# Usage: bash scripts/spawn-dev-agent.sh <agent-name> <space> [work-dir]
#
# The spawned tmux session gets:
#   odis-mcp.*  — production coordinator (check-in, post_status, tasks, messages)
#   odis-dev.*  — local dev instance (test API behavior against your branch's code)
#
# If ODIS_API_TOKEN (or legacy BOSS_API_TOKEN) is set, it is included as an
# Authorization header for odis-mcp.
# If ODIS_MCP_URL (or legacy BOSS_MCP_URL) is unset, defaults to http://localhost:8899/mcp.
#
# The agent runs in a restart loop — if claude exits it relaunches automatically.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKTREE_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ── Args ──────────────────────────────────────────────────────────────────────
if [ $# -lt 2 ]; then
    echo "Usage: $0 <agent-name> <space> [work-dir]" >&2
    exit 1
fi

AGENT_NAME="$1"
SPACE="$2"
WORK_DIR="${3:-$WORKTREE_ROOT}"

# ── Config ────────────────────────────────────────────────────────────────────
ODIS_MCP_URL="${ODIS_MCP_URL:-${BOSS_MCP_URL:-http://localhost:8899/mcp}}"
DEV_PORT_FILE="$WORKTREE_ROOT/data-dev/boss.port"
MCP_CONFIG_FILE="$WORKTREE_ROOT/data-dev/mcp-config-${AGENT_NAME}.json"

# Sanitize agent name for tmux session ID (lowercase alphanumeric + hyphens)
SESSION_ID="dev-$(echo "$AGENT_NAME" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9' '-' | sed 's/-$//')"

# ── Ensure dev instance is running ────────────────────────────────────────────
echo "==> Checking dev instance..."
cd "$WORKTREE_ROOT"

if [ ! -f "$DEV_PORT_FILE" ] || ! ( [ -f data-dev/boss.pid ] && kill -0 "$(cat data-dev/boss.pid)" 2>/dev/null ); then
    echo "==> Dev instance not running — starting it..."
    make dev-start
    # Give it a moment to open the port
    sleep 2
fi

DEV_PORT="$(cat "$DEV_PORT_FILE")"
DEV_MCP_URL="http://localhost:${DEV_PORT}/mcp"
echo "==> Dev instance at port ${DEV_PORT} (${DEV_MCP_URL})"

# ── Generate MCP config JSON ──────────────────────────────────────────────────
mkdir -p "$(dirname "$MCP_CONFIG_FILE")"

API_TOKEN="${ODIS_API_TOKEN:-${BOSS_API_TOKEN:-}}"
if [ -n "${API_TOKEN:-}" ]; then
    # Include Authorization header for production odis-mcp
    cat > "$MCP_CONFIG_FILE" <<JSON
{
  "mcpServers": {
    "odis-mcp": {
      "type": "http",
      "url": "${ODIS_MCP_URL}",
      "headers": {
        "Authorization": "Bearer ${API_TOKEN}"
      }
    },
    "odis-dev": {
      "type": "http",
      "url": "${DEV_MCP_URL}"
    }
  }
}
JSON
else
    cat > "$MCP_CONFIG_FILE" <<JSON
{
  "mcpServers": {
    "odis-mcp": {
      "type": "http",
      "url": "${ODIS_MCP_URL}"
    },
    "odis-dev": {
      "type": "http",
      "url": "${DEV_MCP_URL}"
    }
  }
}
JSON
fi

echo "==> MCP config written to $MCP_CONFIG_FILE"
echo "    odis-mcp  → $ODIS_MCP_URL"
echo "    odis-dev  → $DEV_MCP_URL"

# ── Kill existing session if any ──────────────────────────────────────────────
if tmux has-session -t "$SESSION_ID" 2>/dev/null; then
    echo "==> Killing existing session $SESSION_ID..."
    tmux kill-session -t "$SESSION_ID"
fi

# ── Create tmux session ───────────────────────────────────────────────────────
echo "==> Creating tmux session: $SESSION_ID"
tmux new-session -d -s "$SESSION_ID" -x 220 -y 50

sleep 0.3

# cd to work dir
tmux send-keys -t "$SESSION_ID" "cd $(printf '%q' "$WORK_DIR")" Enter
sleep 0.3

# The claude command uses --mcp-config for both servers, --strict-mcp-config to
# exclude any globally registered servers (clean environment for dev testing).
# Wrapped in a restart loop: if claude exits unexpectedly, it relaunches automatically.
ALLOWED_TOOLS="mcp__odis-mcp__post_status,mcp__odis-mcp__check_messages,mcp__odis-mcp__send_message,mcp__odis-mcp__ack_message,mcp__odis-mcp__request_decision,mcp__odis-mcp__create_task,mcp__odis-mcp__list_tasks,mcp__odis-mcp__move_task,mcp__odis-mcp__update_task,mcp__odis-mcp__spawn_agent,mcp__odis-mcp__restart_agent,mcp__odis-mcp__stop_agent,mcp__odis-dev__post_status,mcp__odis-dev__check_messages,mcp__odis-dev__send_message,mcp__odis-dev__ack_message,mcp__odis-dev__request_decision,mcp__odis-dev__create_task,mcp__odis-dev__list_tasks,mcp__odis-dev__move_task,mcp__odis-dev__update_task,mcp__odis-dev__spawn_agent,mcp__odis-dev__restart_agent,mcp__odis-dev__stop_agent"
CLAUDE_CMD="claude --dangerously-skip-permissions --mcp-config $(printf '%q' "$MCP_CONFIG_FILE") --strict-mcp-config --allowedTools $ALLOWED_TOOLS"
RESTART_LOOP="while true; do $CLAUDE_CMD; echo '[spawn-dev-agent] claude exited — restarting in 2s...'; sleep 2; done"

tmux send-keys -t "$SESSION_ID" "$RESTART_LOOP" Enter

echo ""
echo "==> Agent session ready!"
echo ""
echo "    Session:    $SESSION_ID"
echo "    Agent:      $AGENT_NAME"
echo "    Space:      $SPACE"
echo "    Work dir:   $WORK_DIR"
echo "    odis-mcp:   $ODIS_MCP_URL"
echo "    odis-dev:   $DEV_MCP_URL"
echo ""
echo "    Attach:     tmux attach -t $SESSION_ID"
echo "    Dev status: make dev-status"
echo "    Rebuild:    make dev-restart"
echo ""
echo "==> Register with production boss coordinator:"
echo ""
echo "    curl -s -X POST ${BOSS_MCP_URL%/mcp}/spaces/${SPACE}/agent/${AGENT_NAME}/spawn \\"
echo "      -H 'Content-Type: application/json' \\"
echo "      -d '{\"session_id\": \"${SESSION_ID}\"}'"
echo ""
