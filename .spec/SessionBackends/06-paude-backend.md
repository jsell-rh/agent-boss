# PaudeSessionBackend — Feasibility & Design

Implementation of `SessionBackend` backed by the [paude](https://github.com/bbrowning/paude) CLI,
which runs Claude Code autonomously in isolated containers (Podman locally, OpenShift remotely).

---

## What is Paude?

Paude is a Python CLI tool that runs Claude Code in sandboxed containers. Claude makes
commits inside the container; the user pulls changes via git. It provides:

- **Two backends**: Local Podman containers or remote OpenShift/Kubernetes pods
- **Network isolation**: Squid proxy filtering restricts outbound traffic to approved domains
- **Container isolation**: Claude runs in an ephemeral container with no access to the host
- **Git-based sync**: Changes flow via `git push`/`git pull` through a paude-managed remote
- **tmux inside the container**: Claude Code runs in a tmux session inside each container/pod

**Key architectural fact:** Paude is "tmux-in-a-container, managed by a CLI." Every
interaction with the Claude session requires executing commands inside the container.

### Two-Phase Lifecycle

```
paude create [name]   ->  Resources created (container/pod stopped)
paude start [name]    ->  Container/pod running, Claude active in tmux
paude connect [name]  ->  Attach to running tmux session (interactive)
paude stop [name]     ->  Container/pod stopped (data preserved)
paude delete [name]   ->  All resources permanently removed
```

### Session State Inside the Container

```
Container/Pod
  |
  +-- tmux session "claude"
  |     +-- Claude Code CLI (running autonomously or interactively)
  |
  +-- /pvc/workspace/        (git repo with Claude's changes)
  +-- /credentials/          (tmpfs, auto-wiped on inactivity)
  +-- squid proxy             (optional, for network filtering)
```

---

## Conceptual Mapping: Tmux vs Ambient vs Paude

| Concept | Tmux | Ambient | Paude |
|---------|------|---------|-------|
| Session identity | tmux session name (local) | K8s resource ID (remote) | Container/pod name (local or remote) |
| What runs Claude | Local tmux + Claude CLI | Platform-managed pod | Container with tmux + Claude CLI |
| "Exists" | `tmux list-sessions` | `GET /sessions/{id}` 200 | `paude list` contains name |
| "Idle" | Terminal prompt heuristics | Run status via API | tmux window activity (2min threshold) |
| "Busy" | Active terminal output | Latest run `running` | tmux window activity < 2min ago |
| "Capture output" | Read local tmux pane | Fetch transcript via API | Exec into container, read tmux pane |
| "Send input" | `tmux send-keys` | `POST /message` | Exec into container, `tmux send-keys` |
| "Approval check" | Parse local terminal | N/A (no prompts) | Exec into container, parse tmux |
| "Approve" | `tmux send-keys Enter` | No-op | Exec into container, `tmux send-keys Enter` |
| "Kill" (permanent) | `tmux kill-session` | `DELETE /sessions/{id}` | `paude delete --confirm` |
| "Stop" (preserve) | Not available | `POST /stop` | `paude stop` (data preserved) |
| "Create" | `tmux new-session` (sync) | `POST /sessions` (async) | `paude create` + `paude start` (async) |
| "Discovery" | Parse `agentdeck_*` names | Match `display_name` | `paude list` + name matching |
| "Interrupt" | `tmux send-keys Escape` | `POST /interrupt` | Exec + `tmux send-keys Escape`, or `paude stop` |
| "Status" | Inferred from terminal | API status field | Container state + tmux activity |
| "Resume" | Not possible | `POST /start` | `paude start` (resumes stopped session) |
| "Output" | Terminal pane capture | Transcript API | Git commits + tmux pane (via exec) |
| "Network isolation" | None | Cluster networking | Squid proxy + NetworkPolicy |
| "Container isolation" | None (runs on host) | K8s pod sandbox | Podman/K8s container sandbox |

---

## Interface Mapping

### Interaction Model: The Exec Tax

Every observation and interaction method requires executing a command inside the
container. This is the fundamental cost of the paude model:

- **Podman**: `podman exec paude-{name} tmux ...` — fast, local IPC
- **OpenShift**: `oc exec paude-{name}-0 -c claude -- tmux ...` — network round-trip

The liveness loop polls every 5 seconds. At 10 agents, that's 10 exec calls per
tick for `SessionExists` + `IsIdle` + `CheckApproval` = **30 exec calls every 5
seconds**. This is acceptable for Podman (local) but problematic for OpenShift
(each `oc exec` is a Kubernetes API call + WebSocket connection).

### Methods that map cleanly (7 of 13)

| Method | Paude Mapping | Notes |
|--------|--------------|-------|
| `Name()` | Returns `"paude"` | Trivial |
| `Available()` | `paude --version` succeeds + backend available | Checks CLI + Podman/oc |
| `KillSession(ctx, id)` | `paude delete {id} --confirm` | Permanent removal |
| `SessionExists(id)` | `paude list --json`, check for name | Or cache from periodic list |
| `ListSessions()` | `paude list --json` | Returns all paude sessions |
| `Approve(id)` | Exec: `tmux send-keys -t claude Enter` | Same as tmux but inside container |
| `DiscoverSessions()` | `paude list --json`, match by naming convention | Filter by `ab-*` prefix |

### Methods that work but with performance concerns (4 of 13)

| Method | Paude Mapping | Performance Concern |
|--------|--------------|-------------------|
| `IsIdle(id)` | Exec: `tmux list-windows -t claude -F '#{window_activity}'` | One exec per call. Paude uses a 2-minute activity threshold vs tmux backend's prompt-heuristic approach. Different idle semantics. |
| `CaptureOutput(id, lines)` | Exec: `tmux capture-pane -t claude -p` | One exec per call. Output is raw terminal, same as tmux backend. |
| `CheckApproval(id)` | Exec: capture pane + parse for approval patterns | One exec per call. Same heuristics as tmux backend. |
| `SendInput(id, text)` | Exec: `tmux send-keys -t claude '{text}' C-m` | One exec per call. Latency acceptable for infrequent sends. |

### Methods with significant complexity (2 of 13)

| Method | Paude Mapping | Complexity |
|--------|--------------|-----------|
| `CreateSession(ctx, opts)` | `paude create {name} [--yolo] [--backend=X]` then `paude start {name}` | Two CLI calls. Async — container/pod startup takes seconds (Podman) to minutes (OpenShift image build). Must poll for readiness. Workspace directory must exist on the host for git remote setup. |
| `GetStatus(ctx, id)` | Composite: `paude list` for container state + exec for tmux activity | Requires mapping paude's 5 statuses (`running`/`stopped`/`pending`/`error`/`degraded`) to `SessionStatus` enum, then enriching with idle detection. Two operations. |

### Methods with semantic mismatch (1 of 13)

| Method | Paude Mapping | Mismatch |
|--------|--------------|----------|
| `Interrupt(ctx, id)` | Two options: (a) Exec `tmux send-keys Escape` for soft interrupt, or (b) `paude stop` for hard stop. No native "interrupt current task, keep session" like Ambient's `POST /interrupt`. Soft interrupt via Escape is the closest match, but requires the session to be running and reachable. |

---

## Struct and Configuration

```go
type PaudeSessionBackend struct {
    backendType string // "podman" or "openshift"
    workspace   string // base workspace directory for session repos

    // Paude CLI options
    yolo           bool   // pass --yolo (skip permission prompts)
    allowedDomains string // "default", "all", or comma-separated domains
    pvcSize        string // OpenShift PVC size (default "10Gi")
    image          string // custom container image (optional)

    // Caching
    mu            sync.RWMutex
    sessionCache  map[string]*paudeSession // refreshed periodically
    lastListAt    time.Time
    listCacheTTL  time.Duration // default 10s
}

type paudeSession struct {
    name      string
    status    string // paude status: running/stopped/pending/error/degraded
    workspace string
    backend   string // podman or openshift
}

type PaudeCreateOpts struct {
    Workspace      string // local workspace path (required)
    Yolo           bool   // skip Claude permission prompts
    AllowedDomains string // network domain filter
    ClaudeArgs     string // extra args for Claude Code (e.g., '-p "do X"')
    PVCSize        string // OpenShift only
}
```

### Configuration

```go
type PaudeBackendConfig struct {
    BackendType    string `json:"backend_type"`     // "podman" or "openshift"
    Workspace      string `json:"workspace"`        // base directory for session workspaces
    Yolo           bool   `json:"yolo"`             // default --yolo for all sessions
    AllowedDomains string `json:"allowed_domains"`  // default domain filter
    PVCSize        string `json:"pvc_size"`          // default PVC size (OpenShift)
    Image          string `json:"image"`             // custom container image
}
```

### Environment variables

```bash
PAUDE_BACKEND=podman           # or "openshift"
PAUDE_WORKSPACE=/path/to/base  # base workspace directory
PAUDE_YOLO=true                # default --yolo for all sessions
PAUDE_ALLOWED_DOMAINS=default  # network domain filter
PAUDE_PVC_SIZE=10Gi            # OpenShift PVC size
```

---

## Method Implementations

### `Name() string`

```go
func (b *PaudeSessionBackend) Name() string { return "paude" }
```

### `Available() bool`

Checks that both the `paude` CLI and the selected container backend are available.

```go
func (b *PaudeSessionBackend) Available() bool {
    // 1. Check paude CLI exists
    if _, err := exec.LookPath("paude"); err != nil {
        return false
    }
    // 2. Check container backend
    switch b.backendType {
    case "podman":
        _, err := exec.LookPath("podman")
        return err == nil
    case "openshift":
        _, err := exec.LookPath("oc")
        return err == nil
    default:
        return false
    }
}
```

### `CreateSession(ctx, opts) (string, error)`

Two-step: `paude create` then `paude start`. The session workspace must be a real
git repository on the host (paude sets up a git remote for sync).

```go
func (b *PaudeSessionBackend) CreateSession(ctx context.Context, opts SessionCreateOpts) (string, error) {
    name := opts.SessionID
    if name == "" {
        return "", fmt.Errorf("session ID is required")
    }

    var paudeOpts PaudeCreateOpts
    if opts.BackendOpts != nil {
        if po, ok := opts.BackendOpts.(PaudeCreateOpts); ok {
            paudeOpts = po
        }
    }

    workspace := paudeOpts.Workspace
    if workspace == "" {
        workspace = filepath.Join(b.workspace, name)
    }

    // Step 1: paude create
    args := []string{"create", name, "--backend=" + b.backendType}
    if paudeOpts.Yolo || b.yolo {
        args = append(args, "--yolo")
    }
    if paudeOpts.AllowedDomains != "" {
        args = append(args, "--allowed-domains", paudeOpts.AllowedDomains)
    } else if b.allowedDomains != "" {
        args = append(args, "--allowed-domains", b.allowedDomains)
    }
    if paudeOpts.ClaudeArgs != "" {
        args = append(args, "-a", paudeOpts.ClaudeArgs)
    }

    createCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
    defer cancel()
    cmd := exec.CommandContext(createCtx, "paude", args...)
    cmd.Dir = workspace
    if out, err := cmd.CombinedOutput(); err != nil {
        return "", fmt.Errorf("paude create: %s: %w", string(out), err)
    }

    // Step 2: paude start
    startCtx, cancel2 := context.WithTimeout(ctx, 300*time.Second)
    defer cancel2()
    // --no-attach: start without attaching to tmux (non-interactive)
    startCmd := exec.CommandContext(startCtx, "paude", "start", name, "--no-attach")
    startCmd.Dir = workspace
    if out, err := startCmd.CombinedOutput(); err != nil {
        return "", fmt.Errorf("paude start: %s: %w", string(out), err)
    }

    return name, nil
}
```

**Critical issue:** Paude's `start` command attaches to the tmux session interactively
by default (`podman attach` or `oc exec -it`). The coordinator needs a non-interactive
start. Paude does not currently expose a `--no-attach` flag — this would need to be
added upstream. **Without this, `CreateSession` would block forever waiting for the
interactive tmux session to detach.**

**Workaround:** Instead of `paude start`, the backend could directly invoke
`podman start paude-{name}` or `oc scale statefulset/paude-{name} --replicas=1`
to start the container/pod without attaching.

### `KillSession(ctx, id) error`

```go
func (b *PaudeSessionBackend) KillSession(ctx context.Context, sessionID string) error {
    killCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
    defer cancel()
    cmd := exec.CommandContext(killCtx, "paude", "delete", sessionID, "--confirm")
    out, err := cmd.CombinedOutput()
    if err != nil {
        // Accept "not found" as success (already deleted)
        if strings.Contains(string(out), "not found") {
            return nil
        }
        return fmt.Errorf("paude delete: %s: %w", string(out), err)
    }
    return nil
}
```

### `SessionExists(id) bool`

```go
func (b *PaudeSessionBackend) SessionExists(sessionID string) bool {
    b.refreshCacheIfStale()
    b.mu.RLock()
    defer b.mu.RUnlock()
    _, ok := b.sessionCache[sessionID]
    return ok
}
```

### `ListSessions() ([]string, error)`

```go
func (b *PaudeSessionBackend) ListSessions() ([]string, error) {
    sessions, err := b.listSessionsFromCLI()
    if err != nil {
        return nil, err
    }
    ids := make([]string, 0, len(sessions))
    for _, s := range sessions {
        ids = append(ids, s.name)
    }
    return ids, nil
}
```

### `GetStatus(ctx, id) (SessionStatus, error)`

Maps paude's status model to `SessionStatus`. Enriches with tmux activity for
running sessions.

```go
func (b *PaudeSessionBackend) GetStatus(ctx context.Context, sessionID string) (SessionStatus, error) {
    b.refreshCacheIfStale()
    b.mu.RLock()
    session, ok := b.sessionCache[sessionID]
    b.mu.RUnlock()

    if !ok {
        return SessionStatusMissing, nil
    }

    switch session.status {
    case "stopped":
        return SessionStatusCompleted, nil // closest mapping
    case "pending":
        return SessionStatusPending, nil
    case "error", "degraded":
        return SessionStatusFailed, nil
    case "running":
        if b.IsIdle(sessionID) {
            return SessionStatusIdle, nil
        }
        return SessionStatusRunning, nil
    default:
        return SessionStatusUnknown, nil
    }
}
```

### `IsIdle(id) bool`

Paude's activity detection uses tmux's `window_activity` timestamp and a 2-minute
threshold. This is different from the tmux backend's prompt-heuristic approach.

```go
func (b *PaudeSessionBackend) IsIdle(sessionID string) bool {
    // Exec into container: tmux list-windows -t claude -F '#{window_activity}'
    rc, stdout, _ := b.execInSession(sessionID,
        "tmux list-windows -t claude -F '#{window_activity}'")
    if rc != 0 || stdout == "" {
        return false // can't determine -> assume busy
    }

    // Parse unix timestamp, compare to now
    ts, err := strconv.ParseInt(strings.TrimSpace(stdout), 10, 64)
    if err != nil {
        return false
    }
    elapsed := time.Now().Unix() - ts
    return elapsed > 120 // idle if >2 minutes since last activity
}
```

**Semantic difference from tmux backend:** The tmux backend checks for shell prompt
characters (`$`, `>`, `#`) in the terminal output. The paude backend checks the tmux
window activity timestamp. These detect different things:

- Tmux backend: "Claude is at a prompt" (finished current task)
- Paude backend: "No terminal activity for 2 minutes" (could be thinking, could be idle)

The 2-minute threshold means paude will report agents as "busy" for up to 2 minutes
after they actually finish. This delays broadcast check-in and liveness updates.

### `CaptureOutput(id, lines) ([]string, error)`

```go
func (b *PaudeSessionBackend) CaptureOutput(sessionID string, lines int) ([]string, error) {
    rc, stdout, stderr := b.execInSession(sessionID,
        "tmux capture-pane -t claude -p")
    if rc != 0 {
        return nil, fmt.Errorf("capture output: %s", stderr)
    }

    allLines := strings.Split(stdout, "\n")
    // Filter empty lines and return last N
    var nonEmpty []string
    for _, l := range allLines {
        if strings.TrimSpace(l) != "" {
            nonEmpty = append(nonEmpty, l)
        }
    }
    if len(nonEmpty) > lines {
        nonEmpty = nonEmpty[len(nonEmpty)-lines:]
    }
    return nonEmpty, nil
}
```

### `CheckApproval(id) ApprovalInfo`

Same terminal-parsing logic as the tmux backend, but executed inside the container.

```go
func (b *PaudeSessionBackend) CheckApproval(sessionID string) ApprovalInfo {
    output, err := b.CaptureOutput(sessionID, 30)
    if err != nil || len(output) == 0 {
        return ApprovalInfo{NeedsApproval: false}
    }

    // Reuse the same approval-pattern parsing from tmux.go
    // Look for "Do you want to..." + numbered choices
    // This is identical to tmuxCheckApproval logic
    return parseApprovalFromLines(output)
}
```

### `SendInput(id, text) error`

```go
func (b *PaudeSessionBackend) SendInput(sessionID string, text string) error {
    escaped := shellQuote(text)
    rc, _, stderr := b.execInSession(sessionID,
        fmt.Sprintf("tmux send-keys -t claude %s C-m", escaped))
    if rc != 0 {
        return fmt.Errorf("send input: %s", stderr)
    }
    return nil
}
```

### `Approve(id) error`

```go
func (b *PaudeSessionBackend) Approve(sessionID string) error {
    rc, _, stderr := b.execInSession(sessionID,
        "tmux send-keys -t claude Enter")
    if rc != 0 {
        return fmt.Errorf("approve: %s", stderr)
    }
    return nil
}
```

### `Interrupt(ctx, id) error`

```go
func (b *PaudeSessionBackend) Interrupt(ctx context.Context, sessionID string) error {
    rc, _, stderr := b.execInSession(sessionID,
        "tmux send-keys -t claude Escape")
    if rc != 0 {
        return fmt.Errorf("interrupt: %s", stderr)
    }
    return nil
}
```

### `DiscoverSessions() (map[string]string, error)`

```go
func (b *PaudeSessionBackend) DiscoverSessions() (map[string]string, error) {
    sessions, err := b.listSessionsFromCLI()
    if err != nil {
        return nil, err
    }
    discovered := make(map[string]string)
    for _, s := range sessions {
        if s.status != "running" {
            continue
        }
        // Convention: paude sessions created by agent-boss use
        // name = "ab-{agent}" or "ab-{space}-{agent}"
        agentName := parsePaudeAgentName(s.name)
        if agentName != "" {
            discovered[agentName] = s.name
        }
    }
    return discovered, nil
}
```

### Helper: `execInSession`

The core primitive — runs a command inside the container/pod.

```go
func (b *PaudeSessionBackend) execInSession(sessionID, command string) (int, string, string) {
    var cmd *exec.Cmd
    containerName := "paude-" + sessionID

    switch b.backendType {
    case "podman":
        cmd = exec.Command("podman", "exec", containerName, "bash", "-c", command)
    case "openshift":
        podName := containerName + "-0"
        cmd = exec.Command("oc", "exec", podName, "-c", "claude", "--", "bash", "-c", command)
    default:
        return 1, "", "unknown backend type"
    }

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    err := cmd.Run()

    rc := 0
    if err != nil {
        if exitErr, ok := err.(*exec.ExitError); ok {
            rc = exitErr.ExitCode()
        } else {
            rc = 1
        }
    }
    return rc, stdout.String(), stderr.String()
}
```

### Helper: `listSessionsFromCLI`

```go
func (b *PaudeSessionBackend) listSessionsFromCLI() ([]paudeSession, error) {
    cmd := exec.Command("paude", "list", "--backend="+b.backendType)
    out, err := cmd.CombinedOutput()
    if err != nil {
        return nil, fmt.Errorf("paude list: %s: %w", string(out), err)
    }
    return parsePaudeListOutput(string(out)), nil
}
```

**Note on `paude list` output format:** Paude outputs a human-readable table, not JSON.
The backend would need to either:
1. Parse the table format (fragile)
2. Use `--json` flag if paude adds one (not currently available)
3. Bypass the CLI and call `podman ps` / `oc get statefulset` directly

Option 3 is the most reliable but couples the backend to the container runtime.

---

## Behavioral Differences from Tmux and Ambient

### 1. Container startup latency

Tmux `CreateSession` takes milliseconds. Paude `CreateSession` takes:
- **Podman**: 5-15 seconds (pull image, create container, start, Claude boot)
- **OpenShift**: 30-120 seconds (PVC provisioning, pod scheduling, image pull, Claude boot)
- **OpenShift with custom image**: 2-5 minutes (on-cluster build + above)

The coordinator must poll for readiness after creation, similar to Ambient but
potentially slower. On OpenShift, the first session creation in a new namespace
can take several minutes due to image pulls.

### 2. Exec-based interaction (the "exec tax")

Every observation and interaction goes through container exec. This adds latency
and creates operational concerns:

| Operation | Tmux Backend | Paude (Podman) | Paude (OpenShift) |
|-----------|-------------|----------------|-------------------|
| IsIdle | ~1ms (local tmux) | ~50ms (podman exec) | ~500ms-2s (oc exec) |
| CaptureOutput | ~1ms | ~50ms | ~500ms-2s |
| CheckApproval | ~1ms | ~50ms | ~500ms-2s |
| SendInput | ~1ms | ~50ms | ~500ms-2s |
| Liveness tick (3 ops x 10 agents) | ~30ms total | ~1.5s total | ~15-60s total |

**Real-world calibration:** The `sdk-backend-replacement` deployment
([proposal](https://github.com/markturansky/agent-boss/blob/main/docs/proposal-agent-boss-ambient.md))
runs **11 concurrent agents** with a 3-second liveness tick. At that scale:
- **Podman**: 11 agents x 3 ops x 50ms = **1.65s per tick** (55% of 3s budget — tight but viable)
- **OpenShift**: 11 agents x 3 ops x 1s = **33s per tick** (11x over the 3s budget — completely unworkable)

**`--yolo` optimization:** When sessions use `--yolo` (the common autonomous mode),
`CheckApproval` always returns `NeedsApproval: false` without needing to exec —
reducing the per-agent ops from 3 to 2 (exists + idle). This improves the Podman
budget to 11 x 2 x 50ms = **1.1s per tick** (37% of budget — comfortable). The
`sdk-backend-replacement` data shows that even with non-yolo sessions, 93% of
approval interrupts were eliminable via allowlist rules in `settings.json`, meaning
approval polling is rarely productive.

**For OpenShift, the liveness loop exceeds the polling interval even with `--yolo`.**
This makes the paude-openshift combination impractical for real-time liveness
monitoring of more than a few agents.

**Mitigation:** Batch exec calls. Instead of 3 separate exec calls per agent per
tick, run a single exec that captures all needed data:

```bash
echo "EXISTS:$(tmux has-session -t claude 2>&1 && echo yes || echo no)"
echo "IDLE:$(tmux list-windows -t claude -F '#{window_activity}')"
echo "OUTPUT:$(tmux capture-pane -t claude -p | tail -30)"
```

This reduces 3 exec calls to 1 per agent per tick. Still expensive for OpenShift
but viable for small agent counts (1-5).

### 3. Different idle detection semantics

The tmux backend uses prompt-heuristic idle detection (checks terminal output for
`$`, `>`, `#` characters). Paude's native idle detection uses a 2-minute tmux
window activity threshold.

For the `SessionBackend` implementation, we have two choices:

**Option A: Use paude's native 2-minute threshold**
- Pro: Consistent with paude's own status reporting
- Con: 2-minute delay before detecting idle; broadcast check-in waits longer

**Option B: Use prompt heuristics (same as tmux backend)**
- Pro: Faster idle detection; consistent with tmux backend behavior
- Con: Requires capturing pane output (exec call) and running the same heuristics

**Recommendation:** Option B. Use the same `lineIsIdleIndicator` / `isShellPrompt`
logic from `tmux.go`. The functions are pure and can be reused. This keeps idle
detection consistent across backends that use tmux internally.

### 4. No non-interactive start

Paude's `start` command attaches to the tmux session interactively. The coordinator
needs to start sessions without blocking on an interactive terminal. Solutions:

1. **Upstream contribution**: Add `--no-attach` / `--detach` flag to `paude start`
2. **Direct container control**: Use `podman start` / `oc scale` directly
3. **Background start**: Run `paude start` in a background goroutine and kill it
   after the container is confirmed running

Option 2 is the most pragmatic — the backend already needs direct access to
podman/oc for exec calls.

### 5. Workspace requirement

Paude requires a real git repository on the host for each session. The coordinator
must either:
- Maintain a workspace directory per agent (managed outside the coordinator)
- Create temporary workspaces at session creation time
- Pre-provision workspaces as part of agent configuration

This is a structural requirement that doesn't exist for tmux (no filesystem needed)
or Ambient (workspace is handled by the platform).

### 6. Network isolation (unique to paude)

Paude's squid proxy filtering is a capability that neither tmux nor Ambient provides.
For agent-boss, this means:

- Agents can be restricted to only reach approved domains
- **But**: The coordinator's own API (`:8899`) must be in the allowed domains list
  for blackboard access
- The proxy blocks unauthorized outbound traffic, which could interfere with
  agents that need internet access for their tasks

### 7. Git-based output model

Paude's primary output mechanism is git commits, not terminal output or API
transcripts. The coordinator's blackboard pattern expects agents to post updates
via HTTP to the boss API. For paude sessions:

- The boss API (`http://host:8899`) must be reachable from inside the container
- For Podman: host networking or explicit port forwarding required
- For OpenShift: requires a Route or Service pointing to the coordinator

---

## Functionality Not Available with Paude Backend

### Fully unavailable

| Feature | Why | Impact |
|---------|-----|--------|
| **Real-time SSE output streaming** | No WebSocket/SSE from container to coordinator. Output requires polling via exec. | Frontend agent detail view won't have live output updates. Must poll. |
| **Model switching during broadcast** | Paude sessions have Claude CLI args set at creation time. `/model` command works inside the session, but the coordinator can't observe or control it without exec. | Broadcast check-in model-switch flow is technically possible but adds more exec round-trips. Recommend skipping model switch for paude (same as Ambient). |
| **Fast liveness with OpenShift** | `oc exec` latency makes per-tick per-agent polling too slow for >5 agents. | Liveness loop must use longer intervals (30s+) or batch operations. Agent status may be stale. |

### Degraded / requires workaround

| Feature | Degradation | Workaround |
|---------|------------|------------|
| **Blackboard posting** | Claude in containers can't reach boss API by default. | Configure allowed domains to include boss host. For Podman: use host networking. For OpenShift: expose boss as a Route/Service. |
| **Ignition prompt** | Coordinator sends ignite command via `SendInput`. Works but requires container to be fully ready (Claude booted, tmux session active). | Poll for tmux session existence inside container before sending ignite. Add readiness check loop. |
| **Broadcast check-in** | Full broadcast flow works but each step requires exec. A 5-agent broadcast: 5 idle checks + 5 sends + 5 wait-for-post polls = 15+ exec calls minimum. | Batch where possible. Accept longer broadcast times. |
| **Non-interactive session creation** | `paude start` blocks on interactive tmux attach. | Use direct `podman start` / `oc scale` instead of `paude start`. |
| **Session output parsing** | `paude list` outputs human-readable table, not structured data. | Parse table or use direct container queries. Propose `--json` flag upstream. |
| **Approval detection accuracy** | Same heuristic as tmux backend, but exec latency means approval prompts may not be detected immediately. | Accept slightly delayed approval detection (seconds, not minutes). |

### Not applicable (by design)

| Feature | Why N/A |
|---------|---------|
| **Resume/restart distinction** | Paude supports `stop`/`start` natively. `KillSession` maps to `delete`. The coordinator can use `stop`/`start` for restarts instead of `kill`/`create`, avoiding the full container creation overhead. This is an advantage over tmux. |
| **Session persistence across restarts** | Paude sessions survive container stops (volume/PVC preserved). Unlike tmux, where sessions are lost on system reboot. This is an advantage. |

---

## Impact on Broadcast / Check-In

### Current tmux flow (per agent)

```
1. Switch model:     tmuxSendKeys("/model haiku")           ~1ms
2. Wait for idle:    poll tmuxIsIdle() every 3s              ~1ms per poll
3. Send check-in:    tmuxSendKeys("/boss.check Agent Space") ~1ms
4. Wait for post:    poll agentUpdatedAt() every 3s          ~0ms (boss-side)
5. Restore model:    tmuxSendKeys("/model sonnet")           ~1ms
6. Wait for idle:    poll tmuxIsIdle() every 3s              ~1ms per poll
```

### Paude adaptation (per agent)

```
1. Skip model switch: Same reasoning as Ambient (see 04-ambient-backend.md §4)
2. Check idle:        execInSession("tmux list-windows...")   ~50ms podman / ~1s openshift
3. Send check-in:     execInSession("tmux send-keys...")      ~50ms podman / ~1s openshift
4. Wait for post:     poll agentUpdatedAt() every 3s          ~0ms (boss-side, no exec)
5. Skip model restore
6. Check idle:        execInSession("tmux list-windows...")   ~50ms podman / ~1s openshift
```

**Estimated broadcast time for 5 agents:**
- Podman: ~5-10 seconds (acceptable)
- OpenShift: ~30-60 seconds (marginal)

The coordinator should skip model switching for paude backends (same as Ambient).

---

## Comparison with Other Backends

### Implementation Complexity

| Backend | Lines of code (est.) | External deps | Client-side state | Key challenge |
|---------|---------------------|---------------|-------------------|---------------|
| Tmux | ~150 | tmux binary | None | Prompt heuristics |
| Ambient | ~300 | HTTP client | None | Async lifecycle |
| Paude | ~400-500 | paude + podman/oc | Session cache | Exec latency, no `--no-attach` |
| AgentCore | ~500-700 | AWS SDK | Session registry, output buffer | Missing APIs |

### When Paude Makes Sense

Despite the exec tax, paude is the right choice when:

- **Container isolation is required** — agents must not have host filesystem access
- **Network filtering is required** — agents should only reach approved domains
- **Session persistence matters** — agents survive container restarts (unlike tmux)
- **OpenShift is the deployment target** — paude already handles StatefulSets, PVCs,
  NetworkPolicies, and credential sync
- **Small-to-medium agent count** — Podman handles up to ~10 agents (with `--yolo`
  and batched exec); OpenShift is limited to 1-5
- **Autonomous, long-running tasks** — not time-sensitive interactive coordination

Paude is a poor choice when:

- **Real-time liveness is critical** — exec latency prevents fast status updates
- **Large agent count on OpenShift** — exec overhead scales linearly; the
  `sdk-backend-replacement` scale of 11 agents is unworkable over `oc exec`
- **Tight coordination loop** — broadcast check-in adds significant latency
- **Interactive model switching** — not cleanly supported

---

## Prerequisite: Upstream Changes

The following changes to paude would significantly improve the backend implementation:

| Change | Priority | Reason |
|--------|----------|--------|
| `paude start --no-attach` / `--detach` | **High** | Required for non-interactive session creation. Without this, the backend must bypass the CLI for starting sessions. |
| `paude list --json` | **High** | Required for reliable session discovery. Current table format is fragile to parse. |
| `paude status --json` | Medium | Structured status output for enrichment. |
| `paude exec` as public API | Medium | Currently `exec_in_session` is a Python method, not a CLI command. Would simplify the Go backend. |
| Configurable idle threshold | Low | 2-minute threshold is too long for liveness. Allow customization or expose raw tmux activity timestamp. |

---

## Verdict

### The interface works — no changes needed

All 13 `SessionBackend` methods can be implemented against paude. No new interface
methods are required. The core technique — executing tmux commands inside the
container — mirrors the tmux backend's approach but adds a container exec layer.

### Practical for Podman, marginal for OpenShift

- **Podman backend**: Viable. Exec latency (~50ms) is acceptable. With `--yolo`
  and batched exec, supports up to ~10 agents within the 3-second liveness budget
  (validated against the `sdk-backend-replacement` 11-agent scale — 1.1s per tick
  at 2 ops/agent). Recommended for local development where container isolation
  and network filtering are desired.

- **OpenShift backend**: Marginal. Exec latency (~500ms-2s) makes real-time liveness
  impractical for more than a few agents. At 11-agent scale, a single liveness tick
  would take ~22-33s — far exceeding the 3s interval. Best suited for 1-3
  long-running autonomous agents where real-time coordination is not critical.

### Phase 3 recommendation

Paude support is feasible as a Phase 3 backend (after tmux and Ambient). It offers
unique value (container isolation, network filtering, session persistence) but
requires upstream CLI changes for production use. The implementation is moderately
complex — more than Ambient (due to exec overhead management) but less than
AgentCore (no missing APIs to compensate for).

**Recommended implementation order:**
1. Phase 1: Tmux (current — behavior-preserving refactor)
2. Phase 2: Ambient (1:1 API mapping, production remote backend)
3. Phase 3a: Paude-Podman (local isolated containers)
4. Phase 3b: Paude-OpenShift (remote isolated containers, if latency proves acceptable)

---

## Sources

- [paude repository](https://github.com/bbrowning/paude)
- `src/paude/backends/base.py` — Backend Protocol definition
- `src/paude/backends/podman.py` — Podman backend implementation
- `src/paude/backends/openshift/backend.py` — OpenShift backend implementation
- `src/paude/session_status.py` — Activity detection and work summary
- `src/paude/cli/commands.py` — CLI command definitions
- `src/paude/workflow.py` — Harvest/reset/status workflows
