# Agent Hierarchy — Design Document

**Author:** HierarchyMgr
**Branch:** feat/hierarchy
**Status:** APPROVED — CTO decisions incorporated, proceeding to implementation
**Date:** 2026-03-07

---

## 1. Problem Statement

Agent Boss currently models all agents as flat peers inside a `KnowledgeSpace`. Every agent sees every other agent. Every message is point-to-point. There is no concept of organizational hierarchy.

Real multi-agent systems need:
- **Parent-child relationships** (manager → worker, orchestrator → SME)
- **Hierarchy-aware message routing** (manager directives fan out to children; children escalate to parent)
- **Visibility scoping** (sub-agents don't need to see the entire space — only their slice)
- **Hierarchy visualization** in the dashboard (tree view / org chart)
- **Token optimization** (agents receive only what is relevant to their level)

---

## 2. Design Goals

1. **Backward compatible** — flat agent workflows must continue to work unchanged.
2. **Zero external dependencies** — stdlib only, consistent with `go.mod`.
3. **Opt-in** — agents that don't declare a parent are treated as roots (current behavior).
4. **Server-authoritative children** — the server builds the children list by inverting declared parent relationships; agents don't self-manage children.
5. **Simple API surface** — hierarchy declaration fits naturally into existing endpoints.

---

## 3. Data Model Changes

### 3.1 `AgentUpdate` (types.go)

Add three fields to `AgentUpdate`:

```go
// Hierarchy fields — optional. If Parent is empty, agent is a root node.
Parent   string   `json:"parent,omitempty"`
Children []string `json:"children,omitempty"` // server-managed; read-only for agents
Role     string   `json:"role,omitempty"`      // "manager", "worker", "sme", "observer"
```

- `Parent`: name of the agent's direct manager. Empty = root.
- `Children`: populated by the server by scanning all agents for matching `Parent` values. Agents MUST NOT set this field; the server overwrites it.
- `Role`: free-form label for display purposes only. No routing semantics.

### 3.2 `AgentRegistration` (protocol.go)

Add `Parent` to `AgentRegistration` so hierarchy can be declared at registration time (sticky):

```go
// Parent declares this agent's manager in the hierarchy. Optional.
// If set, the server links this agent as a child of Parent on registration.
Parent string `json:"parent,omitempty"`
```

### 3.3 `KnowledgeSpace` (types.go)

No structural changes needed. Hierarchy is derived from the `Agents` map on every read by scanning `Parent` fields. No separate tree structure is stored.

A helper `BuildHierarchyTree(ks *KnowledgeSpace) *HierarchyTree` computes the tree on demand.

```go
type HierarchyTree struct {
    Roots []string                     `json:"roots"` // agents with no parent
    Nodes map[string]*HierarchyNode    `json:"nodes"`
}

type HierarchyNode struct {
    Agent    string   `json:"agent"`
    Parent   string   `json:"parent,omitempty"`
    Children []string `json:"children"`
    Depth    int      `json:"depth"` // 0 = root
    Role     string   `json:"role,omitempty"`
}
```

---

## 4. How Agents Declare Hierarchy

Two mechanisms (both optional, both sticky):

### 4.1 Via Registration (preferred for long-lived agents)

```bash
curl -s -X POST http://localhost:8899/spaces/MySpace/agent/WorkerA/register \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: WorkerA' \
  -d '{
    "agent_type": "tmux",
    "parent": "ManagerA",
    "role": "worker",
    "capabilities": ["code", "test"]
  }'
```

### 4.2 Via Status POST (inline declaration)

Agents can include `parent` and `role` in any status POST. The server merges these into the agent's `AgentUpdate` and treats them as sticky (subsequent POSTs that omit these fields do not clear them).

```bash
curl -s -X POST http://localhost:8899/spaces/MySpace/agent/WorkerA \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: WorkerA' \
  -d '{
    "status": "active",
    "summary": "WorkerA: starting task",
    "parent": "ManagerA",
    "role": "worker"
  }'
```

### 4.3 Server-Side Linking (future / boss-managed)

A future endpoint `POST /spaces/{space}/agent/{parent}/children/{child}` could let a manager explicitly adopt a child agent (useful when the child doesn't know its parent yet). Not in v1.

---

## 5. Children List — Server Maintenance

On every write to any agent's `Parent` field, the server:

1. Resolves the parent agent name (case-insensitive).
2. **Runs cycle detection** (DFS from proposed parent back to agentName — see below).
3. Sets the `Parent` field.
4. Scans all agents in the space for `Parent == thisAgent` to rebuild `Children`.
5. Saves the space.

This is O(N) where N = number of agents per space — acceptable for team-scale deployments (<100 agents).

### Sticky `Parent` semantics

`Parent` is **mutable and sticky** — identical to `TmuxSession`. A status POST that includes `parent` updates it. A status POST that omits `parent` does NOT clear it. To remove a parent, an agent must POST `"parent": ""` explicitly.

### Cycle Detection — must be atomic with write

**Cycle detection MUST run inside the same `s.mu.Lock()` block that writes `Parent` and calls `rebuildChildren`.** Reading the graph outside the lock risks traversing a temporarily inconsistent tree and missing a cycle introduced by a concurrent update.

Required sequence in `handleSpaceAgent` and `handleAgentRegister`:

```go
s.mu.Lock()
// 1. Zero agent-supplied Children (server-managed; ignore any value from POST body)
update.Children = nil
// 2. DFS cycle check: walk Parent links starting from update.Parent;
//    if we reach agentName, reject with 400 "cycle detected"
if hasCycle(ks, agentName, update.Parent) {
    s.mu.Unlock()
    writeJSONError(w, "cycle detected: parent assignment would create a loop", http.StatusBadRequest)
    return
}
// 3. Set parent (sticky: only update if non-empty, or explicitly cleared)
if update.Parent != "" || parentExplicitlyCleared {
    agent.Parent = update.Parent
}
// 4. Rebuild children list
rebuildChildren(ks)
// 5. Save
s.saveSpace(ks)
s.mu.Unlock()
```

```go
func hasCycle(ks *KnowledgeSpace, agentName, proposedParent string) bool {
    visited := make(map[string]bool)
    current := strings.ToLower(proposedParent)
    for current != "" {
        if current == strings.ToLower(agentName) {
            return true // cycle found
        }
        if visited[current] {
            break // already-detected cycle elsewhere; stop
        }
        visited[current] = true
        canonical := resolveAgentName(ks, current)
        ag, ok := ks.Agents[canonical]
        if !ok {
            break // dangling reference — no cycle through here
        }
        current = strings.ToLower(ag.Parent)
    }
    return false
}
```

### `rebuildChildren` helper

```go
func rebuildChildren(ks *KnowledgeSpace) {
    // Reset all children slices
    for _, ag := range ks.Agents {
        ag.Children = nil
    }
    // Populate from Parent fields
    for name, ag := range ks.Agents {
        if ag.Parent != "" {
            canonicalParent := resolveAgentName(ks, ag.Parent)
            if parent, ok := ks.Agents[canonicalParent]; ok {
                parent.Children = append(parent.Children, name)
            }
        }
    }
    // Sort children for stable output
    for _, ag := range ks.Agents {
        sort.Strings(ag.Children)
    }
}
```

Called from `handleSpaceAgent` and `handleAgentRegister` whenever `Parent` changes.

---

## 6. API Changes

### 6.1 New Endpoint: `GET /spaces/{space}/hierarchy`

Returns the full hierarchy tree as JSON:

```json
{
  "space": "AgentBossDevTeam",
  "roots": ["Cto"],
  "nodes": {
    "Cto": {"agent": "Cto", "parent": "", "children": ["HierarchyMgr", "ProtocolMgr"], "depth": 0, "role": "manager"},
    "HierarchyMgr": {"agent": "HierarchyMgr", "parent": "Cto", "children": ["HierarchySME"], "depth": 1, "role": "manager"},
    "HierarchySME": {"agent": "HierarchySME", "parent": "HierarchyMgr", "children": [], "depth": 2, "role": "sme"}
  }
}
```

### 6.2 Enhanced Message Endpoint: Scope Parameter

`POST /spaces/{space}/agent/{name}/message?scope=subtree`

When `scope=subtree`, the server delivers the message to `{name}` AND all descendants (up to 50 recipients — see fan-out cap below). This enables manager-to-team broadcasts without the manager knowing every worker's name.

`scope=direct` (default): current behavior — message delivered to named agent only.

**Fan-out locking model — single critical section, single save, async SSE:**

```go
s.mu.Lock()
// 1. Walk Children links recursively to collect all descendant agent names
recipients := collectSubtree(ks, agentName) // includes agentName itself
if len(recipients) > 50 {
    recipients = recipients[:50]
    s.logEvent("subtree fan-out capped at 50 recipients")
}
// 2. Append message to ALL recipient inboxes in one critical section
msg := AgentMessage{...}
for _, r := range recipients {
    ks.Agents[r].Messages = append(ks.Agents[r].Messages, msg)
}
// 3. One saveSpace call for all recipients
s.saveSpace(ks)
s.mu.Unlock()
// 4. broadcastSSE OUTSIDE lock (sseMu is distinct from s.mu — no deadlock possible)
//    Fire-and-forget per recipient: server returns 202 immediately
for _, r := range recipients {
    go s.broadcastSSE(spaceName, r, "agent_message", ...)
}
w.WriteHeader(http.StatusAccepted) // 202 — async delivery
```

**If a leaf agent (no children) receives `scope=subtree`**, the server delivers to that agent only and returns 202 — not an error.

**Partial persistence on save failure**: if `saveSpace` fails, the in-memory inboxes are updated but disk is stale. On restart those messages are lost. This is acceptable in v1 (same risk as single-agent delivery, documented limitation).

### 6.3 Parent Escalation: Special Target `"parent"`

`POST /spaces/{space}/agent/parent/message` with `X-Agent-Name: WorkerA`

If the named agent in the URL is the literal string `"parent"`, the server resolves it to the caller's actual parent (from `X-Agent-Name` header) and delivers the message there. This lets workers escalate without knowing their manager's name.

**Reserved name check must precede `resolveAgentName`** to avoid collision with an agent literally named "parent":

```go
if strings.EqualFold(agentName, "parent") {
    callerName := r.Header.Get("X-Agent-Name")
    s.mu.RLock()
    caller, ok := ks.Agents[resolveAgentName(ks, callerName)]
    s.mu.RUnlock()
    if !ok || caller.Parent == "" {
        writeJSONError(w, "agent has no declared parent", http.StatusBadRequest)
        return
    }
    agentName = caller.Parent
}
```

`"parent"` is a **reserved agent name** — registering an agent with this name is rejected with 400.

### 6.4 Ignition: Hierarchy-Scoped Peer List

`GET /spaces/{space}/ignition/{name}`

Currently returns ALL peers. With hierarchy:
- Root agents (no parent): see all other root agents + their own direct children
- Non-root agents: see their parent + their siblings (agents sharing the same parent) + their own direct children
- Full space is still visible via `/raw` if needed

This reduces the ignition payload for deeply nested agents (token optimization).

---

## 7. Message Routing — Flow Diagram

```
Space: MyTeam
  CTO (root)
  ├── Manager (child of CTO)
  │   ├── Worker1 (child of Manager)
  │   └── Worker2 (child of Manager)
  └── Auditor (child of CTO)

Routing scenarios:
  1. CTO → Manager (direct)        : POST /agent/Manager/message
  2. CTO → all under Manager       : POST /agent/Manager/message?scope=subtree
     → delivers to: Manager, Worker1, Worker2
  3. Worker1 → parent escalate     : POST /agent/parent/message (X-Agent-Name: Worker1)
     → delivers to: Manager
  4. Worker1 → Worker2 (peer)      : POST /agent/Worker2/message (unchanged)
  5. CTO → entire space broadcast  : POST /broadcast (unchanged)
```

---

## 8. Dashboard / UI Changes

### 8.1 Hierarchy Tab

New tab "Hierarchy" in the space dashboard. Renders the `GET /hierarchy` JSON as:
- **Indented list view**: agents indented under their parent, with role badges
- **Org chart** (optional v2): SVG/canvas tree diagram

### 8.2 Session Dashboard Table Enhancement

Current flat table gets a "Parent" column when any agent has a parent set:

| Agent | Status | Branch | PR | Parent |
|-------|--------|--------|----|--------|
| CTO | active | main | — | — |
| Manager | active | feat/x | — | CTO |
| Worker1 | active | feat/x | — | Manager |

### 8.3 Agent Card Enhancement

Each agent card in the dashboard shows:
- Parent (linked): "Reports to: CTO"
- Children (linked list): "Manages: Worker1, Worker2"

---

## 9. Backward Compatibility

- All new fields (`parent`, `children`, `role`) are `omitempty` — existing agents that don't set them are unaffected.
- `GET /hierarchy` returns a flat tree (all roots) if no agent has a parent.
- `GET /ignition` falls back to full peer list if agent has no parent declared.
- `POST /message?scope=subtree` with no children is equivalent to `scope=direct`.
- `POST /agent/parent/message` for an agent with no parent returns 400 with a helpful error.

---

## 10. Token Optimization — Ignition Payload Reduction

For a space with 20 agents in a 3-level tree, an agent at depth 2 currently receives peer data for all 20 agents in its ignition response. With hierarchy-scoped ignition:

- **Before**: 20 agent summaries in ignition
- **After**: ~3-5 agents visible (parent + siblings + own children)

This is a significant reduction for large deployments. The full `/raw` endpoint remains available for agents that need global visibility.

---

## 11. Implementation Plan

### Phase 1: Data model (feat/hierarchy)
- [ ] Add `Parent`, `Children`, `Role` to `AgentUpdate`
- [ ] Add `Parent` to `AgentRegistration`
- [ ] Add `HierarchyTree` / `HierarchyNode` types
- [ ] Add `BuildHierarchyTree()` and `rebuildChildren()` helpers
- [ ] Update `handleSpaceAgent` to extract and persist `parent`/`role`, call `rebuildChildren`
- [ ] Update `handleAgentRegister` similarly
- [ ] Add `GET /spaces/{space}/hierarchy` endpoint

### Phase 2: Message routing
- [ ] Add `scope=subtree` to `handleAgentMessage`
- [ ] Add `parent` special target resolution to `handleAgentMessage`
- [ ] Update ignition handler to scope peer list by hierarchy level

### Phase 3: UI
- [ ] Add hierarchy tab to dashboard (indented list view)
- [ ] Add Parent column to session dashboard table
- [ ] Add parent/children to agent card

### Phase 4: Tests

Required test functions (10 minimum, all must pass `-race`):

- [ ] `TestRebuildChildren` — declare parents for 3 agents; verify Children arrays are correct
- [ ] `TestRebuildChildrenCycleRejected` — attempt A→B→A parent chain; verify 400 response
- [ ] `TestRebuildChildrenOrphanParent` — set Parent to non-existent agent; verify it is stored but Children not populated for missing parent
- [ ] `TestScopeSubtreeDelivery` — fan-out to manager + 2 workers; verify all 3 receive message
- [ ] `TestScopeSubtreeLeafIsNoop` — subtree to leaf agent (no children); verify 202 and single delivery
- [ ] `TestParentEscalation` — worker POSTs to "parent" target; verify manager receives it
- [ ] `TestParentEscalationNoParent` — agent with no parent POSTs to "parent" target; verify 400
- [ ] `TestFlatAgentsUnaffectedByHierarchy` — agents without Parent set; verify existing behavior unchanged
- [ ] `TestHierarchyEndpoint` — `GET /hierarchy` returns correct tree structure for a 3-level team
- [ ] `TestChildrenNotClientSettable` — agent POSTs `children:[...]`; verify server overwrites with correct server-computed value

---

## 12. Decisions (CTO, 2026-03-07)

1. **Parent mutability**: Parent IS mutable — sticky pattern same as `TmuxSession`. Agents can re-declare parent on any status POST; omitting `parent` does not clear it.
2. **Scope**: Hierarchy is **per-space**. No global hierarchy across spaces.
3. **Fan-out delivery**: `scope=subtree` is **async** (fire-and-forget per recipient). Server returns 202 immediately. Each recipient's SSE stream fires independently via goroutine.
4. **Dashboard**: Hierarchy tab exists **alongside** the flat Session Dashboard — it does not replace it.

---

## 13. Risks and Mitigations

| Risk | Mitigation |
|------|-----------|
| `rebuildChildren` is O(N) on every write | Acceptable for <100 agents; can be optimized later with inverted index |
| Circular parent references (A → B → A) | Validate on write: reject if new parent would create cycle (DFS check) |
| Parent agent doesn't exist yet | Allow dangling parent references; server resolves to empty when parent absent |
| Large subtree message fan-out could be slow | Cap fan-out at 50 recipients; log warning; use goroutines for delivery |

---

## Appendix: Minimal Go Structs Summary

```go
// types.go additions
type AgentUpdate struct {
    // ... existing fields ...
    Parent   string   `json:"parent,omitempty"`
    Children []string `json:"children,omitempty"` // server-managed
    Role     string   `json:"role,omitempty"`
}

type HierarchyTree struct {
    Roots []string                  `json:"roots"`
    Nodes map[string]*HierarchyNode `json:"nodes"`
}

type HierarchyNode struct {
    Agent    string   `json:"agent"`
    Parent   string   `json:"parent,omitempty"`
    Children []string `json:"children"`
    Depth    int      `json:"depth"`
    Role     string   `json:"role,omitempty"`
}

// protocol.go addition to AgentRegistration
type AgentRegistration struct {
    // ... existing fields ...
    Parent string `json:"parent,omitempty"`
}
```
