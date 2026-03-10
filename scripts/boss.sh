#!/bin/bash
#
# Paude Workspace Bootstrap Script
# 
# Purpose: Creates isolated Paude containers for all agents in a workspace
# - Mounts project repo with full access
# - Enables --yolo mode for minimal interrupts  
# - Preserves Agent Boss coordination via tmux sessions
# - Recoverable: agents re-ignite from blackboard context on restart
#
# Usage: ./boot-paude-workspace.sh [workspace-name]
# Example: ./boot-paude-workspace.sh sdk-backend-replacement
#

set -euo pipefail

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DATA_DIR="${PROJECT_ROOT}/data"
BOSS_URL="http://localhost:8899"
PAUDE_IMAGE="localhost/paude-claude:latest"

# Default workspace
WORKSPACE_NAME="${1:-sdk-backend-replacement}"

# Agent definitions for this workspace
# Format: "NAME:ROLE:SOURCE_FILES"
AGENTS=(
    "API:api-server-development:internal/coordinator/"
    "SDK:sdk-generation:cmd/boss/,internal/"  
    "CLI:command-line-interface:cmd/boss/"
    "CP:control-plane-operator:internal/coordinator/"
    "FE:frontend-development:internal/coordinator/static/"
    "BE:backend-integration:internal/"
    "Cluster:infrastructure-ops:data/,scripts/"
    "Overlord:coordination-management:data/,internal/"
    "Reviewer:code-review:internal/,cmd/"
    "Paude:containerization:scripts/,docs/"
)

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

# Cleanup function for graceful shutdown
cleanup() {
    log_info "Cleaning up..."
    if [[ "${CLEANUP_ON_EXIT:-}" == "true" ]]; then
        stop_all_agents
    fi
}
trap cleanup EXIT

# Check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check podman
    if ! command -v podman &> /dev/null; then
        log_error "podman is required but not installed"
        exit 1
    fi
    
    # Check Agent Boss server
    if ! curl -s "${BOSS_URL}/spaces" > /dev/null; then
        log_error "Agent Boss server not reachable at ${BOSS_URL}"
        log_info "Start with: DATA_DIR=./data ./boss serve"
        exit 1
    fi
    
    # Check workspace exists
    if [[ ! -f "${DATA_DIR}/${WORKSPACE_NAME}.json" ]]; then
        log_warn "Workspace ${WORKSPACE_NAME} doesn't exist - will be created on first agent post"
    fi
    
    # Check for local Paude + Claude image
    log_info "Checking for Paude + Claude integration image..."
    if ! podman image exists "${PAUDE_IMAGE}"; then
        log_error "Paude + Claude image not found: ${PAUDE_IMAGE}"
        log_info "Build the integrated image first with: ./scripts/build-paude-claude.sh"
        exit 1
    fi
    log_success "Paude + Claude image found: ${PAUDE_IMAGE}"
    
    log_success "Prerequisites check complete"
}

# Generate container name for agent
get_container_name() {
    local agent_name="$1"
    echo "paude-${WORKSPACE_NAME,,}-${agent_name,,}"
}

# Generate session name for agent
get_session_id() {
    local agent_name="$1"
    echo "agentdeck_${agent_name}_$(date +%s)"
}

# Note: Ignition is now handled by the integrated container image

# Start single agent container
start_agent() {
    local agent_spec="$1"
    IFS=':' read -r agent_name role source_files <<< "${agent_spec}"
    
    local container_name=$(get_container_name "${agent_name}")
    local session_id=$(get_session_id "${agent_name}")
    
    log_info "Starting agent: ${agent_name} (${role})"
    
    # Check if container already exists
    if podman ps -a --format "{{.Names}}" | grep -q "^${container_name}$"; then
        log_warn "Container ${container_name} already exists - removing"
        podman rm -f "${container_name}" || true
    fi
    
    # Start container with full YOLO permissions and integrated ignition
    podman run -d \
        --name "${container_name}" \
        --network=host \
        --privileged \
        --security-opt=seccomp=unconfined \
        --security-opt=apparmor=unconfined \
        --cap-add=ALL \
        -v "${PROJECT_ROOT}:/workspace:Z" \
        -e BOSS_URL="${BOSS_URL}" \
        -e AGENT_NAME="${agent_name}" \
        -e WORKSPACE_NAME="${WORKSPACE_NAME}" \
        -e AGENT_ROLE="${role}" \
        -e SOURCE_FILES="${source_files}" \
        -e TMUX_SESSION="${session_id}" \
        -e CLAUDE_ALLOW_DANGEROUS_TOOLS=1 \
        -e CLAUDE_AUTO_APPROVE=1 \
        -e COORDINATION_ENABLED=1 \
        "${PAUDE_IMAGE}"
        
    # Wait for container to stabilize
    sleep 2
    
    # Verify container is running
    if podman ps --format "{{.Names}}" | grep -q "^${container_name}$"; then
        log_success "Agent ${agent_name} started successfully"
        log_info "  Container: ${container_name}"
        log_info "  Session: ${session_id}"
        log_info "  Role: ${role}"
        log_info "  Source focus: ${source_files}"
    else
        log_error "Failed to start agent ${agent_name}"
        podman logs "${container_name}" | tail -10
        return 1
    fi
}

# Start all agents
start_all_agents() {
    log_info "Starting all agents for workspace: ${WORKSPACE_NAME}"
    
    local started=0
    local failed=0
    
    for agent_spec in "${AGENTS[@]}"; do
        if start_agent "${agent_spec}"; then
            ((started++))
        else
            ((failed++))
        fi
        
        # Stagger starts to prevent resource contention
        sleep 3
    done
    
    log_success "Agent startup complete: ${started} started, ${failed} failed"
}

# Stop single agent
stop_agent() {
    local agent_name="$1"
    local container_name=$(get_container_name "${agent_name}")
    
    if podman ps --format "{{.Names}}" | grep -q "^${container_name}$"; then
        log_info "Stopping agent: ${agent_name}"
        podman stop "${container_name}" || true
        podman rm "${container_name}" || true
    fi
}

# Stop all agents
stop_all_agents() {
    log_info "Stopping all agents for workspace: ${WORKSPACE_NAME}"
    
    for agent_spec in "${AGENTS[@]}"; do
        IFS=':' read -r agent_name _ _ <<< "${agent_spec}"
        stop_agent "${agent_name}"
    done
    
    # Clean up temporary ignition scripts
    rm -f /tmp/ignite-*.sh
    
    log_success "All agents stopped"
}

# Show status of all agents
show_status() {
    log_info "Agent status for workspace: ${WORKSPACE_NAME}"
    echo
    
    echo "=== Container Status ==="
    echo "NAME                    STATUS              IMAGE"
    echo "----------------------------------------"
    for agent_spec in "${AGENTS[@]}"; do
        IFS=':' read -r agent_name _ _ <<< "${agent_spec}"
        local container_name=$(get_container_name "${agent_name}")
        
        if podman ps -a --format "{{.Names}} {{.Status}} {{.Image}}" | grep -q "^${container_name}"; then
            podman ps -a --format "{{.Names}} {{.Status}} {{.Image}}" | grep "^${container_name}" | head -1
        else
            printf "%-22s %-18s %s\n" "${container_name}" "NOT_CREATED" "n/a"
        fi
    done
    
    echo
    echo "=== Agent Boss Status ==="
    if curl -s "${BOSS_URL}/spaces/${WORKSPACE_NAME}/api/agents" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    print(f'{"AGENT":<12} {"STATUS":<10} {"SUMMARY"}')
    print('-' * 60)
    for name, agent in data.items():
        print(f'{name:<12} {agent[\"status\"]:<10} {agent[\"summary\"][:40]}...' if len(agent[\"summary\"]) > 40 else f'{name:<12} {agent[\"status\"]:<10} {agent[\"summary\"]}')
except:
    print('No agents registered in Agent Boss yet')
    " 2>/dev/null; then
        :
    else
        echo "Could not fetch Agent Boss status"
    fi
}

# Connect to agent session
connect_agent() {
    local agent_name="$1"
    local container_name=$(get_container_name "${agent_name}")
    
    if ! podman ps --format "{{.Names}}" | grep -q "^${container_name}$"; then
        log_error "Agent ${agent_name} container is not running"
        return 1
    fi
    
    log_info "Connecting to agent ${agent_name}..."
    log_info "Use 'tmux list-sessions' to see sessions"
    log_info "Use 'tmux attach -t SESSION_NAME' to connect to Claude"
    
    podman exec -it "${container_name}" /bin/bash
}

# Restart specific agent
restart_agent() {
    local agent_name="$1"
    log_info "Restarting agent: ${agent_name}"
    
    # Find agent spec
    local agent_spec=""
    for spec in "${AGENTS[@]}"; do
        if [[ "${spec}" =~ ^${agent_name}: ]]; then
            agent_spec="${spec}"
            break
        fi
    done
    
    if [[ -z "${agent_spec}" ]]; then
        log_error "Unknown agent: ${agent_name}"
        return 1
    fi
    
    stop_agent "${agent_name}"
    sleep 2
    start_agent "${agent_spec}"
}

# Test broadcast feature with Paude agents
test_broadcast() {
    log_info "Testing broadcast feature with Paude agents..."
    
    echo "=== Testing check-in broadcast ==="
    curl -X POST "${BOSS_URL}/spaces/${WORKSPACE_NAME}/broadcast"
    echo
    
    sleep 5
    
    echo "=== Agent responses ==="
    curl -s "${BOSS_URL}/spaces/${WORKSPACE_NAME}/raw" | head -20
}

# Usage help
usage() {
    cat << EOF
Paude Workspace Bootstrap Script

USAGE:
    $0 [OPTIONS] [WORKSPACE]

OPTIONS:
    start           Start all agents (default)
    stop            Stop all agents  
    restart         Restart all agents
    status          Show agent status
    connect AGENT   Connect to specific agent
    restart AGENT   Restart specific agent
    test            Test broadcast feature
    help            Show this help

AGENTS:
$(printf "    %s\n" "${AGENTS[@]}" | sed 's/:/ - Role: /; s/:/, Sources: /')

EXAMPLES:
    $0 sdk-backend-replacement              # Start all agents
    $0 stop                                 # Stop all agents
    $0 connect API                          # Connect to API agent
    $0 restart CP                           # Restart CP agent
    $0 test                                 # Test broadcast feature

WORKSPACE DATA:
    Location: ${DATA_DIR}/
    Agent Boss: ${BOSS_URL}
    
EOF
}

# Main execution
main() {
    local command="${1:-start}"
    
    case "${command}" in
        "start")
            check_prerequisites
            start_all_agents
            echo
            show_status
            ;;
        "stop")
            stop_all_agents
            ;;
        "restart")
            if [[ -n "${2:-}" ]]; then
                restart_agent "$2"
            else
                stop_all_agents
                sleep 3
                check_prerequisites  
                start_all_agents
            fi
            ;;
        "status")
            show_status
            ;;
        "connect")
            if [[ -z "${2:-}" ]]; then
                log_error "Usage: $0 connect AGENT_NAME"
                exit 1
            fi
            connect_agent "$2"
            ;;
        "test")
            test_broadcast
            ;;
        "help"|"-h"|"--help")
            usage
            ;;
        *)
            # If first arg doesn't match a command, treat it as workspace name
            if [[ "$command" =~ ^[a-zA-Z0-9_-]+$ ]]; then
                WORKSPACE_NAME="$command"
                check_prerequisites
                start_all_agents
                echo
                show_status
            else
                log_error "Unknown command: $command"
                usage
                exit 1
            fi
            ;;
    esac
}

# Run main function with all arguments
main "$@"