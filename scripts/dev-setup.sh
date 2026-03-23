#!/usr/bin/env bash
# dev-setup.sh — one-shot bootstrap for a per-worktree boss dev instance.
#
# Usage: bash scripts/dev-setup.sh [DEV_PORT]
#
# What it does:
#   1. Creates data-dev/ directory
#   2. Builds the boss binary (make dev-build)
#   3. Prints the MCP registration command for claude
#
# Each worktree is isolated — its own data-dev/, port, and PID file —
# so multiple agents can run dev instances in parallel without conflict.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKTREE_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$WORKTREE_ROOT"

# Determine port
if [ -n "${1:-}" ]; then
    DEV_PORT="$1"
elif [ -n "${DEV_PORT:-}" ]; then
    : # use env var as-is
else
    # Auto-detect first free port >= 9000
    DEV_PORT=9000
    while ss -tlnH 2>/dev/null | awk '{print $4}' | grep -qE ":${DEV_PORT}$" || \
          { command -v lsof >/dev/null 2>&1 && lsof -ti:"$DEV_PORT" >/dev/null 2>&1; }; do
        DEV_PORT=$((DEV_PORT + 1))
    done
fi

echo "==> Setting up dev instance in: $WORKTREE_ROOT"
echo "==> Port: $DEV_PORT"

# Create data-dev directory
mkdir -p data-dev

# Save the chosen port so make dev-start picks it up
echo "$DEV_PORT" > data-dev/boss.port

# Build the binary
echo "==> Building boss binary (make dev-build)..."
DEV_PORT="$DEV_PORT" make dev-build

echo ""
echo "==> Dev instance ready. To start it:"
echo ""
echo "    make dev-start DEV_PORT=$DEV_PORT"
echo ""
echo "==> To register with claude MCP:"
echo ""
echo "    claude mcp add odis-dev --transport http http://localhost:${DEV_PORT}/mcp"
echo ""
echo "==> Workflow:"
echo "    make dev-start        # start isolated instance on port $DEV_PORT"
echo "    make dev-status       # check status + last log lines"
echo "    make dev-restart      # rebuild + restart to pick up code changes"
echo "    make dev-stop         # shut down"
echo ""
