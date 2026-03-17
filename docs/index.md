# Agent Boss — Docs Index

Knowledge base for the Agent Boss coordination server. See [ARCHITECTURE.md](../ARCHITECTURE.md) for the system map.

---

## Design Documents

Core design decisions and specifications that shaped the system.

| File | Title | Status |
|------|-------|--------|
| [agents-design.md](agents-design.md) | Agent Definition System Design | **implemented** |
| [hierarchy-design.md](hierarchy-design.md) | Agent Hierarchy — Design Document | **implemented** |
| [hierarchy-sme-review.md](hierarchy-sme-review.md) | Hierarchy Design Review — SME Analysis | **implemented** |
| [sse-design.md](sse-design.md) | Per-Agent SSE Stream — Design Specification | **implemented** |
| [task-system-design.md](task-system-design.md) | Task Management System Design | **implemented** |
| [lifecycle-spec.md](lifecycle-spec.md) | Agent Lifecycle & Introspection Specification | **implemented** |
| [event-perf-spec.md](event-perf-spec.md) | Event Journal Performance Spec | **implemented** |
| [sse-scalability-spec.md](sse-scalability-spec.md) | SSE Agent-Polling Scalability Spec | **implemented** |
| [gantt-spec.md](gantt-spec.md) | Gantt / Timeline Visualization Spec | **implemented** |
| [design-spec-task-014.md](design-spec-task-014.md) | Frontend UX Overhaul (TASK-014) | **implemented** |
| [resolve-agent-name-audit.md](resolve-agent-name-audit.md) | resolveAgentName Lock Audit | **implemented** |
| [agent-names.md](agent-names.md) | Agent Names — Supported Characters | **implemented** |
| [paude.md](paude.md) | Paude Integration for Agent Boss | **proposed** |
| [design-docs/hexagonal-architecture.md](design-docs/hexagonal-architecture.md) | Hexagonal Architecture Migration Plan | **in-progress** |
| [design-docs/auth-model.md](design-docs/auth-model.md) | Auth Model ADR — Phase 1 static token + Phase 2 per-agent tokens | **proposed** |
| [design-docs/agent-compose.md](design-docs/agent-compose.md) | agent-compose.yaml — Fleet Blueprint Design Spec (TASK-098) | **implemented** |
| [design-docs/agent-experience-surface.md](design-docs/agent-experience-surface.md) | Agent Experience Surface — spawn contract, dev loop, MCP surface | **active** |
| [design-docs/audio-language-spec.md](design-docs/audio-language-spec.md) | Audio Language & Voice System Spec | **proposed** |

---

## Executive Plans & Proposals

Strategic direction, roadmaps, and factory-scale proposals.

| File | Title | Status |
|------|-------|--------|
| [proposal-agent-boss-ambient.md](proposal-agent-boss-ambient.md) | Agent Boss: Operational Proof at Scale (Ambient backend) | **implemented** |
| [agent-boss-factory-proposal.md](agent-boss-factory-proposal.md) | Agent Boss: TRex-Powered Software Factory | **proposed** |
| [software-factory.md](software-factory.md) | Software Factory: Autonomous Multi-Agent Production | **proposed** |
| [software-factory2.md](software-factory2.md) | Software Factory: Component Dependency Tree | **proposed** |

---

## Product Specs & Reference

End-user and operator documentation.

| File | Title | Status |
|------|-------|--------|
| [getting-started.md](getting-started.md) | Getting Started with Agent Boss | **active** |
| [api-reference.md](api-reference.md) | Agent Boss — API Reference | **active** |
| [AGENT_PROTOCOL.md](AGENT_PROTOCOL.md) | Agent Boss — HTTP Agent Protocol v1.0 | **active** |
| [agent-migration-guide.md](agent-migration-guide.md) | Migration Guide: /raw Polling → Message Polling | **active** |

---

## Quality & Tech Debt

| File | Title |
|------|-------|
| [QUALITY.md](QUALITY.md) | Quality Grades by Subsystem |
| [exec-plans/tech-debt-tracker.md](exec-plans/tech-debt-tracker.md) | Known Tech Debt Tracker |

---

## Status Legend

| Status | Meaning |
|--------|---------|
| **proposed** | Design or proposal not yet implemented; may be aspirational |
| **in-progress** | Partially implemented; work is actively ongoing |
| **active** | Living reference document, kept up to date |
| **implemented** | Feature built; doc is historical reference |
| **superseded** | Replaced by a newer document or implementation |
