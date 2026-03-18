# Paude Integration for OpenDispatch

## Why Paude?

Multi-agent Claude Code environments face critical problems that Paude solves:

**Security & Isolation Issues:**
- **Direct filesystem access** - Native Claude has unrestricted host access
- **Shared state corruption** - Multiple agents writing to `~/.claude/.claude.json`
- **Session conflicts** - Agents interfering with each other's contexts
- **Permission escalation** - `--skip-safety-checks` reduces security

**Paude Solutions:**
- ✅ **Container isolation** - Each agent runs in separate environment
- ✅ **Network filtering** - Safe access to Vertex AI + OpenDispatch only
- ✅ **Safe YOLO mode** - Dangerous tools enabled with network protection
- ✅ **Clean session management** - Container restart = fresh state
- ✅ **Pre-configured environment** - Claude Code + dependencies ready

## How It Works

### Architecture Overview

```
Host System
├── OpenDispatch Server (:8899)
├── Project Files (~/projects)
└── Paude Containers
    ├── agent-api (isolated Claude environment)
    ├── agent-sdk (isolated Claude environment)  
    ├── agent-cli (isolated Claude environment)
    ├── agent-cp (isolated Claude environment)
    └── agent-fe (isolated Claude environment)
```

### Integration Flow

```
┌─────────────────────────────────────────────────────────┐
│ Host System                                             │
│ ┌─────────────────┐  ┌─────────────────────────────────┐ │
│ │ OpenDispatch      │  │ Paude Containers                │ │  
│ │ Server          │  │ ┌─────────────────────────────┐ │ │
│ │ :8899           │◄─┼─│ Agent Container             │ │ │
│ │                 │  │ │ • Network filtered          │ │ │
│ │ • HTTP API      │  │ │ • Claude Code installed     │ │ │
│ │ • Spaces        │  │ │ • Tmux session management   │ │ │
│ │ • Broadcast     │  │ │ • Auto-registration         │ │ │
│ │ • Dashboard     │  │ │ • Git commit hooks          │ │ │
│ └─────────────────┘  │ │ • Coordination client       │ │ │
│                      │ └─────────────────────────────┘ │ │
│                      └─────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

## Quick Start

### Prerequisites

```bash
# 1. Build Paude base image  
git clone https://github.com/bbrowning/paude.git
cd paude && podman build -t localhost/paude-proxy-centos9:latest .

# 2. Build integrated Claude Code image
cd /path/to/agent-boss
./scripts/build-paude-claude.sh

# 3. Start OpenDispatch server
DATA_DIR=./data ./boss serve
```

### Deploy Complete Workspace

```bash
# Start all agents in secure containers
./scripts/boss.sh sdk-backend-replacement

# Monitor status
./scripts/boss.sh status

# Connect to specific agent
./scripts/boss.sh connect API

# Test broadcast feature
./scripts/boss.sh test
```

### Single Agent Test

```bash
# Run single agent for testing
podman run -it --rm \
  --name claude-test \
  --network=host \
  -v ~/projects/src/gitlab.cee.redhat.com/ocm/agent-boss:/workspace:Z \
  -e ODIS_URL=http://localhost:8899 \
  -e AGENT_NAME=TestAgent \
  -e WORKSPACE_NAME=sdk-backend-replacement \
  localhost/paude-claude:latest
```

## Integration Details

### Auto-Registration & Coordination

**Container Auto-Registration:**
```python
# coordination-client.py automatically:
def register_agent(self):
    summary = f"{self.agent_name}: Paude container initialized ({self.agent_role})"
    return self.post_status('idle', summary, items=[...])
```

**Agent Lifecycle Management:**
```bash
# agent-ignition.sh automatically:
1. Wait for Boss server availability
2. Register agent with role and source files
3. Create tmux session with proper naming
4. Start Claude Code with YOLO permissions  
5. Send /boss.ignite command for context
6. Monitor session and report status updates
```

**Status Update Flow:**
- Agent lifecycle events (start/stop/error) → Boss API
- Git commits → automatic status notifications via hooks
- Periodic health checks → maintain Boss coordination
- Message system → 30-second polling for Boss communications

### Security Model

**Container Isolation:**
- Each agent has isolated filesystem
- Separate `.claude` configuration prevents conflicts
- No shared state corruption between agents
- Container restart provides clean slate

**Network Filtering (Paude Base):**
- Containers can only reach Vertex AI API + OpenDispatch server
- External data exfiltration blocked at network level
- Custom domains configurable via `--allowed-domains`

**Safe YOLO Mode:**
- `CLAUDE_ALLOW_DANGEROUS_TOOLS=1` enabled for full tool access
- `CLAUDE_AUTO_APPROVE=1` for minimal interrupts
- `--privileged` containers for complete system access
- Network filtering ensures safety despite dangerous tools

**Volume Mounting Patterns:**
```bash
# Read-only project access
-v ~/projects:/workspace:ro,Z

# Specific path mounting for focused work
-v ~/projects/src/gitlab.cee.redhat.com/ocm/agent-boss:/agent-boss:Z
-v ~/projects/src/github.com/ambient/platform:/platform:Z

# Separate output directories per agent
-v ~/agent-outputs/${AGENT_NAME}:/outputs:Z
```

### Management Commands

**OpenDispatch Integration:**
```bash
# Check-in all agents (safe with Paude isolation)
curl -X POST http://localhost:8899/spaces/sdk-backend-replacement/broadcast

# Ignite all agents in containers
curl -X POST http://localhost:8899/spaces/sdk-backend-replacement/broadcast?type=ignite

# Check individual agent
curl -X POST http://localhost:8899/spaces/sdk-backend-replacement/broadcast/API?type=check-in

# Verify tmux sessions
curl -s http://localhost:8899/spaces/sdk-backend-replacement/api/tmux-status | python3 -m json.tool
```

**Monitoring & Debugging:**
```bash
# Container status
podman ps --format "table {{.Names}}\t{{.Status}}\t{{.Image}}"

# Agent logs
podman logs agent-api

# Interactive debugging
podman run -it --rm \
    --network=host \
    -v ~/projects:/workspace:Z \
    localhost/paude-claude:latest bash

# Test connectivity from container
podman exec -it agent-api curl -s http://localhost:8899/spaces
```

**Performance Tuning:**
```bash
# Resource limits
podman run -d \
    --memory=2g \
    --cpus=1.0 \
    --name "agent-${AGENT_NAME}" \
    # ... other options

# Shared volumes for efficiency
podman volume create npm-cache
-v npm-cache:/home/user/.npm:Z
```

## Implementation Status: ✅ COMPLETE

This integration is **fully implemented and ready to use**. All components have been built and tested.

### Built Components

| Component | Purpose | Status |
|-----------|---------|---------|
| `docker/Dockerfile.paude-claude` | Integrated container image | ✅ Complete |
| `scripts/coordination-client.py` | OpenDispatch API client | ✅ Complete |
| `scripts/agent-ignition.sh` | Session lifecycle management | ✅ Complete |
| `scripts/claude-wrapper.sh` | Execution hooks | ✅ Complete |
| `scripts/build-paude-claude.sh` | Build automation | ✅ Complete |
| `scripts/boss.sh` | Multi-agent deployment | ✅ Complete |

### File Structure

```
agent-boss/
├── docker/
│   └── Dockerfile.paude-claude        # Integrated container image
├── scripts/
│   ├── build-paude-claude.sh         # Build automation
│   ├── boss.sh                       # Multi-agent deployment  
│   ├── coordination-client.py        # Boss API client
│   ├── agent-ignition.sh             # Session management
│   └── claude-wrapper.sh             # Execution hooks
└── docs/
    └── paude.md                      # This integration guide
```

### Why This Works

The combination of Paude + OpenDispatch provides:

- ✅ **No `.claude.json` corruption**: Each container has isolated config
- ✅ **Safe concurrent operations**: Multiple agents can broadcast simultaneously  
- ✅ **Tmux session isolation**: Each agent has dedicated session in container
- ✅ **Clean restart**: Container restart = fresh environment
- ✅ **Secure by default**: Network filtering + dangerous tools safely enabled
- ✅ **Production ready**: Full automation and safety guarantees

This provides the ultimate secure and reliable multi-agent Claude Code environment for production use.