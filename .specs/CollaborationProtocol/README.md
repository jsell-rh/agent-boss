# Collaboration Protocol Overhaul — Spec Index

**Status:** Draft
**Task:** TASK-058
**Branch:** feat/messaging-spec
**Owner:** ProtocolMgr

## Purpose

This spec defines the new collaboration model for Agent Boss. The goal is to make multi-agent coordination a first-class citizen — not an afterthought bolted on top of status updates.

## Documents

| File | Description | Status |
|------|-------------|--------|
| [overview.md](./overview.md) | High-level vision and principles | Draft |
| [messaging-protocol.md](./messaging-protocol.md) | How agents communicate exclusively via the messaging API | Draft |
| [team-formation.md](./team-formation.md) | Rules for spawning teams for non-trivial tasks | Draft |
| [organizational-model.md](./organizational-model.md) | Leadership, delegation, and org theory | SME Review |
| [task-management.md](./task-management.md) | Strict task/subtask usage rules | SME Review |
| [ignition-prompts.md](./ignition-prompts.md) | What org theory to bake into agent ignition | Draft |
| [gap-analysis.md](./gap-analysis.md) | Current API gaps vs. required capabilities | Draft |

## Key Principles

1. **Messaging-first** — agents communicate exclusively via the messaging API, not by reading `/raw`
2. **Team-always** — non-trivial tasks always spawn a team; no solo work on complex problems
3. **Delegation by default** — leadership agents keep only top-level coordination; everything else delegates
4. **Task discipline** — every piece of work has a task/subtask, assigned to the right agent, kept in sync
5. **Collaboration baked in** — org theory and collaboration norms embedded in ignition prompts

## Review Process

Once all docs are drafted, ProtocolMgr will open a PR for boss review.
