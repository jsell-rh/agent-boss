# Collaboration Protocol Overhaul — Overview

**Status:** Draft
**Owner:** ProtocolMgr

## Problem Statement

The current Agent Boss system has good primitives (messaging API, task board, SSE events, ignition), but collaboration is not enforced. Agents tend to:
- Read `/raw` instead of subscribing to their message stream
- Work solo on tasks that warrant a team
- Create tasks informally or not at all
- Not keep tasks in sync with actual work
- Receive minimal org-theory guidance at ignition time

The result is coordination that works for simple tasks but degrades at scale.

## Vision

Transform Agent Boss from a status dashboard into a **collaboration platform** where:
- Every inter-agent communication flows through the messaging API
- Teams form automatically for non-trivial work
- Hierarchical delegation is natural and enforced by convention
- Task tracking is the source of truth for what work is happening
- Agents arrive pre-loaded with the collaboration norms they need

## Scope of This Spec

This spec covers:

1. **Messaging Protocol** — how agents must communicate
2. **Team Formation** — when and how to form teams
3. **Organizational Model** — leadership, delegation, escalation
4. **Task Management** — task discipline rules
5. **Ignition Prompts** — collaboration norms embedded at startup
6. **Gap Analysis** — current API vs. needed capabilities

This spec does **not** cover implementation. It is a design document for boss review before any code changes.

## Success Criteria

- An agent given a non-trivial task knows exactly how to form a team
- Agents never need to read `/raw` to coordinate — messages suffice
- The task board accurately reflects who is doing what at all times
- A new agent spawned from ignition alone can navigate the collaboration model
- Boss can observe and redirect work through tasks and messages alone

## Stakeholders

- **Boss** — ultimate authority; approves spec; sets priorities
- **CTO** — delegated authority for tech decisions
- **Manager agents** (ProtocolMgr, DataMgr, etc.) — lead teams, delegate tasks
- **Developer/SME agents** — execute delegated work

## Related Documents

- `docs/AGENT_PROTOCOL.md` — existing HTTP agent protocol reference
- `internal/coordinator/protocol.md` — ignition template (embedded)
- TASK-058 — parent task
