# Hierarchy Design Review — SME Analysis

**Reviewer:** HierarchySME
**Design doc:** docs/hierarchy-design.md
**Branch:** feat/hierarchy
**Date:** 2026-03-06
**Verdict:** MOSTLY SOUND with 9 issues requiring attention before implementation

---

## 1. Codebase Verification

Reviewed against actual source: `internal/coordinator/types.go`, `server.go`, `protocol.go`.

Current state:
- `AgentUpdate` has no `Parent`, `Children`, or `Role` fields — additions are net-new, no conflicts.
- `AgentRegistration` has no `Parent` field — addition is safe.
- `resolveAgentName` (case-insensitive lookup) already exists and is used consistently — `rebuildChildren` should use it (design correctly calls for this).
- `broadcastSSE` uses `s.sseMu`; agent data writes use `s.mu` — these are **distinct** mutexes.
- `handleAgentMessage` pattern: lock `s.mu` → modify → save → unlock → call `broadcastSSE`. This pattern is correct and must be preserved in subtree fan-out.
- Current test suite: **50 test functions**, all passing `-race` clean.

---

## 2. Issues Found

### Issue 1 — CORRECTNESS: `Children` field not cleared from agent POST body

**Risk: Medium**

When an agent POSTs an `AgentUpdate` containing `"children": [...]`, the server unmarshals into the struct, then `rebuildChildren` overwrites `Children`. Because this all happens under `s.mu.Lock()`, no external reader sees the transient value. However, the implementation must **explicitly zero the `Children` field** from the decoded struct *before* calling `rebuildChildren`, to be defensive:

```go
// In handleSpaceAgent, after decoding:
update.Children = nil  // server-managed; ignore agent-supplied value
```

Without this, if `rebuildChildren` is somehow skipped (e.g., due to an early-return path), a stale agent-supplied value could persist.

---

### Issue 2 — CORRECTNESS: `"parent"` special target naming collision

**Risk: Medium**

The design proposes `POST /spaces/{space}/agent/parent/message` as a special "escalate to my parent" syntax. If any agent is literally named `"parent"`, the URL is ambiguous.

The router calls `resolveAgentName(ks, "parent")` which would find an agent named `Parent` first, bypassing the escalation logic.

**Recommendation:** Add an explicit check *before* `resolveAgentName`:

```go
if strings.EqualFold(agentName, "parent") {
    // resolve to caller's actual parent
    agentName = lookupParent(ks, senderName)
    if agentName == "" {
        http.Error(w, "agent has no declared parent", http.StatusBadRequest)
        return
    }
}
```

Also: document `"parent"` as a reserved agent name (reject registration with that name, or at minimum document the collision risk).

---

### Issue 3 — CORRECTNESS: Cycle detection must be atomic with write

**Risk: High**

The design mentions cycle detection (DFS to reject circular `Parent` assignments) but does not specify that it must happen **inside** `s.mu.Lock()`. If cycle detection reads `ks.Agents` outside the lock while another goroutine is updating a parent field, the DFS could traverse a temporarily inconsistent tree and miss a cycle.

**Requirement:** Cycle detection must run inside the same `s.mu.Lock()` block that writes the `Parent` field and calls `rebuildChildren`. Example sequence:

```go
s.mu.Lock()
// 1. decode update.Parent
// 2. DFS cycle check from update.Parent back to agentName — reject if cycle found
// 3. set agent.Parent = update.Parent
// 4. rebuildChildren(ks)
// 5. saveSpace(ks)
s.mu.Unlock()
```

---

### Issue 4 — LOCKING: `scope=subtree` fan-out must happen in one critical section

**Risk: High**

The design says "the server walks the hierarchy tree from `{name}` downward and enqueues the message for each descendant" but does not specify the locking model. The correct approach:

```go
s.mu.Lock()
// 1. Collect all descendant agent names by walking Children links
// 2. Append messageReq to ALL descendant inboxes (including named agent)
// 3. saveSpace(ks) — one save for all
s.mu.Unlock()
// 4. Call broadcastSSE once per recipient (uses sseMu, separate from s.mu — no deadlock)
for _, recipient := range recipients {
    s.broadcastSSE(spaceName, recipient, "agent_message", ...)
}
```

If fan-out appends and saves inside multiple separate lock acquisitions, two concurrent subtree fan-outs could interleave, producing duplicate or missing messages. **Single lock, single save.**

---

### Issue 5 — ATOMICITY: Partial fan-out on save failure

**Risk: Low (same as existing single-message risk, but amplified)**

If `saveSpace` fails after N messages are appended to in-memory agent inboxes, those agents have the message in RAM but not on disk. On restart, the messages are lost. This is the same risk as today's single-agent delivery, but for N recipients.

**Recommendation:** Document this as a known limitation. For v1, "best effort delivery" is acceptable. Do NOT attempt N separate saves (worse — partial persistence guaranteed).

---

### Issue 6 — API: `scope=subtree` with no descendants must not error

**Risk: Low**

The design states this is "equivalent to `scope=direct`." Verify the implementation does NOT return 4xx when a leaf agent (no children) receives a subtree-scoped message. It should silently deliver to only the named agent and return 200.

---

### Issue 7 — API: Ignition "sibling roots" definition is unclear

**Risk: Low (doc clarity)**

Section 6.4 says root agents "see all direct children + sibling roots." But root agents (no parent) have no siblings by definition in a tree. The intended behavior appears to be: **root agents see all other root agents + their own children.** Update the wording.

Non-root agents: "see their parent + siblings (same parent) + their own children" — this is clear and correct.

---

### Issue 8 — TESTING: Zero hierarchy tests in current suite

**Risk: High (for implementation quality)**

The 50-test suite has no tests for any hierarchy functionality. Before merging hierarchy implementation, the following tests are required:

1. `TestRebuildChildren` — declare parents for 3 agents; verify Children arrays are correct
2. `TestRebuildChildrenCycleRejected` — attempt A→B→A parent chain; verify 400 response
3. `TestRebuildChildrenOrphanParent` — set Parent to non-existent agent; verify it's stored but Children not populated for missing parent
4. `TestScopeSubtreeDelivery` — fan-out to manager + 2 workers; verify all 3 receive message
5. `TestScopeSubtreeLeafIsNoop` — subtree to leaf agent; verify 200 and single delivery
6. `TestParentEscalation` — worker POSTs to "parent" target; verify manager receives it
7. `TestParentEscalationNoParent` — agent with no parent POSTs to "parent" target; verify 400
8. `TestFlatAgentsUnaffectedByHierarchy` — agents without Parent set; verify existing behavior unchanged
9. `TestHierarchyEndpoint` — GET /hierarchy returns correct tree structure
10. `TestChildrenNotClientSettable` — agent POSTs children:[...]; verify server overwrites with correct value

---

### Issue 9 — DESIGN: Sticky `Parent` field semantics need clarification

**Risk: Low (open question)**

This ties into [?BOSS] question 1 ("immutable or changeable parent?"). The implementation needs to decide before coding:

- **If mutable**: every status POST that includes `parent` updates it. Every status POST that *omits* `parent` must NOT clear it (sticky behavior). This is consistent with how `TmuxSession` and `Registration` are handled.
- **If immutable**: first write wins; subsequent `parent` fields in POST body are ignored after initial declaration.

The `sticky` pattern is already used for `TmuxSession` in `handleSpaceAgent`. The same pattern should apply to `Parent` regardless of which answer [?BOSS] gives — the only difference is whether re-declaration is allowed or silently ignored.

---

## 3. What Is Correct

- **O(N) `rebuildChildren`**: At current scale (<20 agents per space), this is O(microseconds). Even at 100 agents it's negligible. No performance issue.
- **No deadlock from `scope=subtree`**: `s.mu` and `s.sseMu` are distinct locks. As long as `broadcastSSE` is called after releasing `s.mu` (which the current `handleAgentMessage` does), subtree fan-out cannot deadlock.
- **`BuildHierarchyTree` on-demand**: Not storing a separate tree avoids sync issues. Correct approach.
- **`omitempty` on all new fields**: Backward compatible. Flat agents are unaffected.
- **`AgentRegistration.Parent` addition**: Consistent with existing registration pattern. No conflicts.
- **`sort.Strings(ag.Children)`**: Stable output. Correct.
- **Dashboard changes (Parent column, hierarchy tab)**: Purely additive to `RenderMarkdown`. Correct approach.
- **`POST /agent/parent/message` returning 400 when no parent is declared**: Correct error behavior.
- **Fan-out cap at 50 recipients**: Sensible safety valve. Document it clearly in the API reference.

---

## 4. Summary Table

| # | Issue | Severity | Phase |
|---|-------|----------|-------|
| 1 | `Children` not zeroed from POST body | Medium | Phase 1 |
| 2 | `"parent"` token naming collision | Medium | Phase 2 |
| 3 | Cycle detection not atomic with write | **High** | Phase 1 |
| 4 | Subtree fan-out locking not specified | **High** | Phase 2 |
| 5 | Partial fan-out on save failure | Low | Phase 2 |
| 6 | Subtree with no descendants must not error | Low | Phase 2 |
| 7 | "Sibling roots" definition unclear | Low (docs) | Design |
| 8 | Zero hierarchy tests in current suite | **High** | Phase 4 |
| 9 | Sticky vs immutable Parent semantics | Low (pending [?BOSS]) | Phase 1 |

**Blocking issues (must fix before implementation):** 3, 4, 8
**Fix before PR:** 1, 2
**Fix or document:** 5, 6, 7, 9

---

## 5. Recommendation

The design is architecturally sound and well-researched. The data model, API surface, and backward compatibility approach are all correct. The main gap is in implementation guidance for the locking model (Issues 3, 4) and test coverage (Issue 8).

**Proceed to Phase 1 implementation** after:
1. Adding cycle detection spec to section 5 with explicit locking requirement
2. Adding subtree fan-out locking spec to section 6.2
3. Noting the `Children` zeroing requirement in section 5

I'm available for a follow-up review pass once Phase 1 is committed.
