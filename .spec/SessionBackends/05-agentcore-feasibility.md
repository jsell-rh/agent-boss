# AWS Bedrock AgentCore — SessionBackend Feasibility Analysis

Can the `SessionBackend` interface (13 methods) support an AgentCore backend?

**Short answer:** Yes, but with significant client-side state management.
AgentCore is a lower-level hosting primitive than Ambient — it provides
"invoke" and "stop", not managed session lifecycle. The interface design
is sound and requires no new methods, but an AgentCore implementation
would be substantially more complex than the Ambient one.

---

## What is AgentCore?

AgentCore is a **"bring your own agent code" serverless hosting platform**.
You package your agent as a Docker container exposing `/invocations` (request
handler) and `/ping` (health check) on port 8080. AgentCore runs it in
isolated microVMs with per-session compute, memory, and filesystem isolation.

This is fundamentally different from Ambient, which is a **managed Claude
session service** — you say "create a session with this task" and the platform
runs Claude Code for you.

### Two-Tier API

| Tier | Purpose | Key Operations |
|------|---------|----------------|
| **Control Plane** | Manage runtime *definitions* (like a Deployment template) | `CreateAgentRuntime`, `GetAgentRuntime`, `UpdateAgentRuntime`, `DeleteAgentRuntime`, `ListAgentRuntimes` |
| **Data Plane** | Interact with individual sessions | `InvokeAgentRuntime`, `StopRuntimeSession` |

The control plane manages the *deployment* of your agent code. The data plane
manages *sessions* within that deployment. For our use case, the coordinator
would pre-deploy a Claude Code agent as an AgentCore Runtime, then manage
individual sessions via the data plane.

### Session Model

- Sessions are **implicit** — created automatically on first `InvokeAgentRuntime`
  call with a new `runtimeSessionId`.
- Each session gets a dedicated **microVM** with isolated resources.
- Sessions auto-terminate after **15 minutes of inactivity** (configurable).
- Maximum session lifetime: **8 hours**.
- Session state is **ephemeral** — no persistence after termination.
- There is **no "list sessions" API** — you must track session IDs yourself.

### Authentication

AWS IAM or OAuth 2.0 (via AgentCore Identity). Go SDK:
`github.com/aws/aws-sdk-go-v2/service/bedrockagentcore`

---

## Interface Mapping

### Methods that map cleanly (5 of 13)

| Method | AgentCore Mapping | Notes |
|--------|------------------|-------|
| `Name()` | Returns `"agentcore"` | Trivial |
| `Available()` | `GetAgentRuntime(runtimeARN)` returns 200 | Checks if the pre-deployed runtime exists and is active |
| `KillSession(ctx, id)` | `StopRuntimeSession(runtimeARN, sessionID)` | Direct mapping. Terminates the microVM. |
| `SendInput(id, text)` | `InvokeAgentRuntime(runtimeARN, sessionID, payload)` | Sends payload to agent. Returns streaming response. |
| `Approve(id)` | No-op (return nil) | Agent code handles its own tool permissions |

### Methods that work but with semantic differences (2 of 13)

| Method | AgentCore Mapping | Semantic Difference |
|--------|------------------|-------------------|
| `CreateSession(ctx, opts)` | `InvokeAgentRuntime` with a new UUID as `runtimeSessionId` | No explicit "create" — session springs into existence on first invoke. The initial `opts.Command` becomes the first invocation payload. Backend must wait for streaming response to confirm the session started. |
| `CheckApproval(id)` | Returns `ApprovalInfo{NeedsApproval: false}` | Same as Ambient — agent code manages its own permissions. No terminal to parse. |

### Methods with significant gaps (6 of 13)

| Method | Gap | Workaround |
|--------|-----|-----------|
| `SessionExists(id)` | **No API to query session existence.** Sessions are either active (accepting invocations) or terminated (gone). No status endpoint. | Backend must maintain a **local session registry** — track created sessions and mark them terminated on stop/timeout. Alternatively, attempt a lightweight invoke and interpret errors, but this is fragile. |
| `ListSessions()` | **No `ListRuntimeSessions` API exists.** You can list *runtimes* but not *sessions within a runtime*. | Backend must maintain a **local session registry** of all session IDs it has created. |
| `GetStatus(ctx, id)` | **No external session status API.** The `/ping` endpoint is internal to the agent container — it's how AgentCore infrastructure monitors the agent, not how external callers query status. | Backend must **infer status** from local state: just created → `pending`/`running`, last invoke returned output → `running`, invoke failed with session-not-found → `missing`, locally marked stopped → `completed`. Very approximate. |
| `IsIdle(id)` | **`/ping` is internal.** Reports `Healthy` (idle) or `HealthyBusy` (working) but only to AgentCore infrastructure, not to external API callers. | Backend could embed a custom status endpoint in the agent code that the coordinator calls directly. Or track idle state based on whether the last streaming invoke response has completed. Both require custom agent code changes. |
| `CaptureOutput(id, lines)` | **No transcript/output API.** Output comes back as a streaming response from `InvokeAgentRuntime`. There's no after-the-fact "get output" endpoint. | Backend must **capture and buffer** streaming output from every `InvokeAgentRuntime` call. Store the last N lines in memory or a local store. This means the coordinator must be the one invoking (or subscribing to) the agent to capture its output. |
| `DiscoverSessions()` | **No list sessions API.** | Backend returns only sessions it has locally registered. Cannot discover sessions created by other coordinators or external callers. |

### Methods with partial gaps (1 of 13)

| Method | Gap | Workaround |
|--------|-----|-----------|
| `Interrupt(ctx, id)` | `StopRuntimeSession` is **destructive** — it terminates the microVM entirely. There's no "cancel current work but keep session alive" equivalent. Unlike Ambient's `POST /interrupt` which cancels the current run while preserving the session. | If the agent code supports it, send a special "interrupt" message via `InvokeAgentRuntime`. But this requires custom agent code and won't work if the agent is busy (AgentCore won't accept new invocations while status is `HealthyBusy`). In practice, interrupt = kill + recreate for AgentCore. |

---

## Conceptual Comparison

| Concept | Tmux | Ambient | AgentCore |
|---------|------|---------|-----------|
| What runs the agent | Local tmux + Claude CLI | Platform-managed Claude pod | Your Docker container in a microVM |
| Session creation | Explicit (`tmux new-session`) | Explicit (`POST /sessions`) | Implicit (first invoke) |
| Session listing | `tmux list-sessions` | `GET /sessions` | **Not available** |
| Session status | Inferred from terminal | `GET /sessions/{id}` status field | **Not available externally** |
| Idle detection | Parse terminal output | Check run status via API | `/ping` internal only |
| Output capture | Read terminal pane | `GET /sessions/{id}/output` | Streaming during invoke only |
| Send input | `tmux send-keys` | `POST /sessions/{id}/message` | `InvokeAgentRuntime` |
| Kill session | `tmux kill-session` | `POST /sessions/{id}/stop` | `StopRuntimeSession` |
| Interrupt (non-destructive) | `tmux send-keys C-c` | `POST /sessions/{id}/interrupt` | **Not available** |
| Session persistence | Ephemeral (lost on reboot) | Persistent (K8s resource) | Ephemeral (lost on terminate) |
| Discovery | Parse session names | List + match display_name | **Not available** |
| Model flexibility | Any (runs locally) | Configured at creation | Any (you deploy the model call) |

---

## Architecture: What an AgentCore Backend Would Require

### Pre-requisite: Deploy Claude Code as an AgentCore Runtime

Before the backend can create sessions, a Claude Code agent must be
deployed as an AgentCore Runtime. This is a one-time setup:

```
1. Package Claude Code CLI in a container
2. Implement /invocations handler (receives prompts, runs Claude, streams output)
3. Implement /ping handler (reports Healthy/HealthyBusy)
4. CreateAgentRuntime with the container image
5. Store the Runtime ARN in coordinator config
```

This is entirely outside the `SessionBackend` interface — it's infrastructure
setup, similar to having tmux installed or Ambient deployed.

### Client-Side State Store

The AgentCore backend would need a local state store to compensate for
the missing APIs:

```go
type AgentCoreSessionBackend struct {
    runtimeARN string
    client     *bedrockagentcore.Client

    mu       sync.RWMutex
    sessions map[string]*agentCoreSession // sessionID -> state
}

type agentCoreSession struct {
    id        string
    createdAt time.Time
    status    SessionStatus           // locally tracked
    output    *ring.Buffer            // circular buffer of last N output lines
    lastInvoke time.Time
}
```

### Estimated Implementation Complexity

| Backend | Lines of code (est.) | External dependencies | Client-side state needed |
|---------|---------------------|----------------------|------------------------|
| Tmux | ~150 | tmux binary | None |
| Ambient | ~300 | HTTP client | None (API is stateful) |
| AgentCore | ~500-700 | AWS SDK Go v2 | Session registry, output buffer, status tracking |

---

## Verdict

### The interface works — no changes needed

All 13 methods can be implemented against AgentCore. No new methods are
required. The gaps are all solvable with client-side state management.

### But the implementation is substantially more complex

AgentCore is designed as a low-level hosting platform, not a managed
session service. It gives you two operations — invoke and stop — and
expects you to build session management on top. This means:

1. **Session tracking**: Must maintain a local registry of all sessions
2. **Output buffering**: Must capture and store streaming output
3. **Status inference**: Must derive status from local state rather than querying an API
4. **No interrupt**: Must accept kill-and-recreate as the "interrupt" pattern
5. **No discovery**: Can only find sessions the coordinator itself created
6. **Custom agent code**: Need to build and deploy a Claude Code container

### Comparison to Ambient

Ambient's API was practically designed for this interface — nearly 1:1 mapping
with rich session lifecycle, status, output, and interrupt APIs. AgentCore
requires the backend to replicate what Ambient provides natively.

### When AgentCore makes sense

Despite the complexity, AgentCore would be the right choice when:

- Running on AWS infrastructure (native IAM integration)
- Need model flexibility beyond Claude (AgentCore is model-agnostic)
- Want per-session compute isolation (dedicated microVMs)
- Already have agent code that isn't Claude Code (LangGraph, CrewAI, etc.)
- Need the AWS ecosystem (CloudWatch, X-Ray, IAM, VPC integration)

### Recommendation

AgentCore support is feasible as a Phase 3 backend (after tmux and Ambient),
but it's a larger effort. The interface design is validated — it accommodates
AgentCore's minimal API surface without requiring new methods. The complexity
lives entirely in the implementation, not the interface.

---

## Sources

- [Amazon Bedrock AgentCore Overview](https://aws.amazon.com/bedrock/agentcore/)
- [AgentCore Runtime — How it Works](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-how-it-works.html)
- [InvokeAgentRuntime API Reference](https://docs.aws.amazon.com/bedrock-agentcore/latest/APIReference/API_InvokeAgentRuntime.html)
- [StopRuntimeSession API Reference](https://docs.aws.amazon.com/bedrock-agentcore/latest/APIReference/API_StopRuntimeSession.html)
- [CreateAgentRuntime API Reference](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_CreateAgentRuntime.html)
- [GetAgentRuntime API Reference](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_GetAgentRuntime.html)
- [ListAgentRuntimes API Reference](https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_ListAgentRuntimes.html)
- [Session Isolation](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-sessions.html)
- [Lifecycle Configuration](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-lifecycle-settings.html)
- [Async and Long-Running Agents](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-long-run.html)
- [HTTP Protocol Contract](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-http-protocol-contract.html)
- [AgentCore Observability](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/observability.html)
- [Go SDK — bedrockagentcore package](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/bedrockagentcore)
- [AgentCore Python SDK (GitHub)](https://github.com/aws/bedrock-agentcore-sdk-python)
