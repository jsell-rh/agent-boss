#!/bin/bash
#
# Agent Ignition Script for Paude + Claude Code
#
# Handles the complete agent initialization process:
# 1. Register with Agent Boss
# 2. Create tmux session
# 3. Start Claude Code
# 4. Get ignition context
# 5. Maintain session
#

set -euo pipefail

# Configuration from environment
AGENT_NAME="${AGENT_NAME:-agent}"
WORKSPACE_NAME="${WORKSPACE_NAME:-default}"
AGENT_ROLE="${AGENT_ROLE:-development}"
SOURCE_FILES="${SOURCE_FILES:-}"
BOSS_URL="${BOSS_URL:-http://localhost:8899}"
TMUX_SESSION="${TMUX_SESSION:-agentdeck_${AGENT_NAME}_$(date +%s)}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

# Export session name for coordination client
export TMUX_SESSION

# Wait for Agent Boss to be available
wait_for_boss() {
    log_info "Waiting for Agent Boss server at ${BOSS_URL}..."
    
    for i in {1..30}; do
        if curl -s "${BOSS_URL}/spaces" > /dev/null 2>&1; then
            log_success "Agent Boss server is available"
            return 0
        fi
        
        if [[ $i -eq 30 ]]; then
            log_error "Agent Boss server not available after 30 attempts"
            return 1
        fi
        
        sleep 2
    done
}

# Register agent with Agent Boss
register_agent() {
    log_info "Registering agent ${AGENT_NAME} with Agent Boss..."
    
    if /usr/local/bin/coordination-client.py register; then
        log_success "Agent registered successfully"
        return 0
    else
        log_error "Failed to register agent"
        return 1
    fi
}

# Create tmux session for Claude Code
create_session() {
    log_info "Creating tmux session: ${TMUX_SESSION}"
    
    # Kill existing session if it exists
    tmux kill-session -t "${TMUX_SESSION}" 2>/dev/null || true
    
    # Create new session in detached mode
    tmux new-session -d -s "${TMUX_SESSION}" -c /workspace
    
    # Configure session
    tmux set-option -t "${TMUX_SESSION}" history-limit 10000
    tmux set-option -t "${TMUX_SESSION}" mouse on
    
    log_success "Tmux session created: ${TMUX_SESSION}"
}

# Start Claude Code in tmux session
start_claude() {
    log_info "Starting Claude Code in tmux session..."
    
    # Send claude command to tmux session
    tmux send-keys -t "${TMUX_SESSION}" "claude" C-m
    
    # Wait a moment for Claude to start
    sleep 3
    
    log_success "Claude Code started in tmux session"
}

# Send ignition command to Claude
send_ignition() {
    log_info "Sending ignition command to Claude..."
    
    # Wait a bit more for Claude to fully initialize
    sleep 2
    
    # Send boss ignition command
    ignition_cmd="/boss.ignite ${AGENT_NAME} ${WORKSPACE_NAME}"
    tmux send-keys -t "${TMUX_SESSION}" "${ignition_cmd}" C-m
    
    log_success "Ignition command sent: ${ignition_cmd}"
}

# Setup git hooks for commit notifications
setup_git_hooks() {
    if [[ -d "/workspace/.git" ]]; then
        log_info "Setting up git commit hooks..."
        
        cat > /workspace/.git/hooks/post-commit << 'EOF'
#!/bin/bash
# Git post-commit hook for Agent Boss coordination

if [[ -n "${COORDINATION_ENABLED:-}" ]]; then
    COMMIT_HASH=$(git rev-parse HEAD)
    COMMIT_MSG=$(git log -1 --pretty=%s)
    
    /usr/local/bin/coordination-client.py git-commit "$COMMIT_HASH" "$COMMIT_MSG" || true
fi
EOF
        
        chmod +x /workspace/.git/hooks/post-commit
        log_success "Git hooks configured"
    else
        log_warn "No git repository found in /workspace"
    fi
}

# Monitor and maintain session
maintain_session() {
    log_info "Monitoring agent session..."
    
    # Post status update that we're active
    /usr/local/bin/coordination-client.py status "active" "${AGENT_NAME}: monitoring session (role: ${AGENT_ROLE})" || true
    
    # Monitor loop
    while true; do
        # Check if tmux session still exists
        if ! tmux has-session -t "${TMUX_SESSION}" 2>/dev/null; then
            log_warn "Tmux session ${TMUX_SESSION} no longer exists"
            break
        fi
        
        # Check if Claude process is running
        if ! tmux list-panes -t "${TMUX_SESSION}" -F "#{pane_current_command}" | grep -q "claude\|node"; then
            log_warn "Claude Code process not found in tmux session"
            # Could restart Claude here if desired
        fi
        
        # Check for incoming messages (every 30 seconds)
        messages=$(/usr/local/bin/coordination-client.py check-messages 2>/dev/null || echo "")
        if [[ -n "$messages" && "$messages" != "📬 No new messages" ]]; then
            log_info "📧 New messages detected - notifying Claude"
            # Send a notification to Claude session about new messages
            tmux send-keys -t "${TMUX_SESSION}" C-c || true
            sleep 1
            tmux send-keys -t "${TMUX_SESSION}" "echo '📧 NEW MESSAGE from Boss - check with: /usr/local/bin/coordination-client.py check-messages'" C-m || true
            # You can reply with: /usr/local/bin/coordination-client.py boss-reply <msg_id> <your_reply>
        fi
        
        # Periodic status update (every 5 minutes)
        if [[ $(($(date +%s) % 300)) -eq 0 ]]; then
            /usr/local/bin/coordination-client.py status "active" "${AGENT_NAME}: session active (role: ${AGENT_ROLE})" || true
        fi
        
        sleep 30
    done
}

# Cleanup function
cleanup() {
    log_info "Cleaning up agent session..."
    
    # Post final status
    /usr/local/bin/coordination-client.py status "idle" "${AGENT_NAME}: session ended" || true
    
    # Clean up tmux session
    tmux kill-session -t "${TMUX_SESSION}" 2>/dev/null || true
    
    log_info "Cleanup complete"
}

# Trap cleanup on exit
trap cleanup EXIT INT TERM

# Main execution flow
main() {
    log_info "Starting agent ignition for ${AGENT_NAME} (${AGENT_ROLE})"
    log_info "Workspace: ${WORKSPACE_NAME}"
    log_info "Source focus: ${SOURCE_FILES}"
    log_info "Session: ${TMUX_SESSION}"

    # Initialize agent
    wait_for_boss || exit 1
    register_agent || exit 1
    create_session || exit 1
    setup_git_hooks
    start_claude || exit 1
    send_ignition || exit 1
    
    # Maintain session
    maintain_session
}

# Run main function
main "$@"