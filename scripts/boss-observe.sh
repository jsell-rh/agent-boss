#!/usr/bin/env bash
# boss-observe.sh — mid-session curl wrapper for boss observability.
#
# Use this script when boss-observe MCP server is not available in the current
# session (e.g. it wasn't registered at spawn time via --mcp-config). Agents
# can call these functions directly via the Bash tool.
#
# Config (override via env vars):
#   BOSS_URL         — boss server base URL (default: http://localhost:8899)
#   BOSS_API_TOKEN   — Bearer token for auth (default: empty = open mode)
#
# Usage:
#   # Source this file to get helper functions:
#   source scripts/boss-observe.sh
#
#   # Or run directly:
#   bash scripts/boss-observe.sh get-agent-status "My Space" arch2
#   bash scripts/boss-observe.sh list-spaces
#   bash scripts/boss-observe.sh get-recent-events "My Space" 20
#   bash scripts/boss-observe.sh get-session-output agent-boss-dev-arch2 50

set -euo pipefail

BOSS_URL="${BOSS_URL:-http://localhost:8899}"
BOSS_API_TOKEN="${BOSS_API_TOKEN:-}"

# --- internal helpers ---

_curl() {
  local args=(-sS -f)
  if [[ -n "$BOSS_API_TOKEN" ]]; then
    args+=(-H "Authorization: Bearer $BOSS_API_TOKEN")
  fi
  curl "${args[@]}" "$@"
}

_require_jq() {
  if ! command -v jq &>/dev/null; then
    echo "boss-observe: jq is required for JSON formatting. Raw JSON will be printed." >&2
    return 1
  fi
  return 0
}

_pretty() {
  if _require_jq 2>/dev/null; then
    jq .
  else
    cat
  fi
}

# --- commands ---

# list-spaces: list all spaces with agent counts.
list_spaces() {
  echo "=== Spaces ===" >&2
  _curl "${BOSS_URL}/spaces" | _pretty
}

# get-agent-status SPACE AGENT: combined status from boss + tmux output.
get_agent_status() {
  local space="${1:?Usage: get-agent-status SPACE AGENT}"
  local agent="${2:?Usage: get-agent-status SPACE AGENT}"

  echo "=== Agent: $agent (space: $space) ===" >&2

  # Fetch agent record from boss API.
  local encoded_space encoded_agent
  encoded_space=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$space'))" 2>/dev/null || printf '%s' "$space" | sed 's/ /%20/g')
  encoded_agent=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$agent'))" 2>/dev/null || printf '%s' "$agent")

  local agent_json
  agent_json=$(_curl "${BOSS_URL}/spaces/${encoded_space}/agent/${encoded_agent}" 2>/dev/null || echo '{}')
  echo "$agent_json" | _pretty

  # Extract session_id and show tmux output.
  local session_id
  session_id=$(echo "$agent_json" | (jq -r '.session_id // empty' 2>/dev/null || echo ""))
  if [[ -n "$session_id" ]]; then
    echo "" >&2
    echo "=== Tmux session: $session_id ===" >&2
    get_session_output "$session_id" 20
  fi
}

# get-recent-events SPACE [LIMIT] [EVENT_TYPE]: recent events from boss.
get_recent_events() {
  local space="${1:?Usage: get-recent-events SPACE [LIMIT] [EVENT_TYPE]}"
  local limit="${2:-20}"
  local event_type="${3:-}"

  local encoded_space
  encoded_space=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$space'))" 2>/dev/null || printf '%s' "$space" | sed 's/ /%20/g')

  local url="${BOSS_URL}/spaces/${encoded_space}/api/events"
  echo "=== Recent events (space: $space, limit: $limit) ===" >&2

  local events
  events=$(_curl "$url" 2>/dev/null || echo '[]')

  if [[ -n "$event_type" ]]; then
    events=$(echo "$events" | (jq --arg t "$event_type" '[.[] | select(.type == $t)]' 2>/dev/null || echo "$events"))
  fi

  # Tail to limit.
  echo "$events" | (jq --argjson n "$limit" '.[-$n:]' 2>/dev/null || echo "$events") | _pretty
}

# get-session-output SESSION_ID [LINES]: last N lines from tmux pane.
get_session_output() {
  local session_id="${1:?Usage: get-session-output SESSION_ID [LINES]}"
  local lines="${2:-50}"

  echo "=== Tmux output: $session_id (last $lines lines) ===" >&2

  if ! tmux has-session -t "$session_id" 2>/dev/null; then
    echo "Session '$session_id' not found." >&2
    return 1
  fi

  tmux capture-pane -p -t "$session_id" | tail -n "$lines"
}

# list-sessions [FILTER]: list all tmux sessions with idle/running status.
list_sessions() {
  local filter="${1:-}"

  echo "=== Tmux sessions ===" >&2

  if ! tmux list-sessions -F '#{session_name}' 2>/dev/null; then
    echo "No tmux sessions found (or tmux not running)." >&2
    return 0
  fi
}

# check-all SPACE: quick overview — list agents + their statuses.
check_all() {
  local space="${1:?Usage: check-all SPACE}"

  echo "=== Space overview: $space ===" >&2
  local encoded_space
  encoded_space=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$space'))" 2>/dev/null || printf '%s' "$space" | sed 's/ /%20/g')

  _curl "${BOSS_URL}/spaces/${encoded_space}/api/agents" 2>/dev/null | \
    (jq 'to_entries | map({agent: .key, status: .value.status.status, summary: .value.status.summary, session_id: .value.status.session_id}) | .[]' 2>/dev/null || cat) | _pretty
}

# --- main dispatch ---

cmd="${1:-help}"
shift 2>/dev/null || true

case "$cmd" in
  list-spaces)        list_spaces "$@" ;;
  get-agent-status)   get_agent_status "$@" ;;
  get-recent-events)  get_recent_events "$@" ;;
  get-session-output) get_session_output "$@" ;;
  list-sessions)      list_sessions "$@" ;;
  check-all)          check_all "$@" ;;
  help|--help|-h)
    cat <<'EOF'
boss-observe.sh — mid-session curl wrapper for boss observability

Usage: boss-observe.sh <command> [args...]

Commands:
  list-spaces                         List all spaces
  get-agent-status  SPACE AGENT       Agent status + tmux output
  get-recent-events SPACE [LIMIT] [TYPE]  Recent events (default limit: 20)
  get-session-output SESSION_ID [N]   Last N lines from tmux pane (default: 50)
  list-sessions     [FILTER]          List active tmux sessions
  check-all         SPACE             Quick overview of all agents in a space

Environment:
  BOSS_URL         Boss server URL (default: http://localhost:8899)
  BOSS_API_TOKEN   Bearer token (default: empty = open mode)

Examples:
  bash scripts/boss-observe.sh list-spaces
  bash scripts/boss-observe.sh get-agent-status "Agent Boss Dev" arch2
  bash scripts/boss-observe.sh get-recent-events "Agent Boss Dev" 10 agent_updated
  bash scripts/boss-observe.sh get-session-output agent-boss-dev-arch2 30
  bash scripts/boss-observe.sh check-all "Agent Boss Dev"
EOF
    ;;
  *)
    echo "boss-observe: unknown command '$cmd'. Run 'boss-observe.sh help' for usage." >&2
    exit 1
    ;;
esac
