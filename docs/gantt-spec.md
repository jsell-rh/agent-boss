# Gantt / Timeline Visualization Spec

## Overview

A Gantt-style timeline panel added to the OpenDispatch dashboard showing each agent's state over time as horizontal bars. Default view: last 2 hours. Auto-refreshes via SSE or polling.

## Data Model

### StatusSnapshot

Persisted to `data/{space}-history.json` as a JSON array (append-only).

```json
{
  "agent_name": "UXDev",
  "space": "AgentBossDevTeam",
  "status": "active",
  "inferred_status": "",
  "stale": false,
  "summary": "UXDev: implementing history endpoint",
  "timestamp": "2026-03-07T01:25:00Z"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `agent_name` | string | Agent identifier (matches URL path key, case-normalized) |
| `space` | string | Knowledge space name |
| `status` | string | Agent-reported status: `active`, `blocked`, `done`, `idle`, `error` |
| `inferred_status` | string | Server-inferred override (e.g., `stale`), empty if not applicable |
| `stale` | bool | True when agent has not updated within StalenessThreshold (15 min) |
| `summary` | string | Agent summary at snapshot time (for tooltip) |
| `timestamp` | string | UTC RFC3339 timestamp of when snapshot was taken |

### Persistence Rules

- Append a snapshot on every successful agent status POST.
- Append a snapshot on every liveness loop tick for any agent whose `Stale` or `InferredStatus` changes.
- Keep all history in `data/{space}-history.json`. No rotation — the frontend filters client-side by time window.
- File format: JSON array, re-written atomically on each append.

## API Endpoints

### GET /spaces/{space}/history

Returns all snapshots for the space, newest first.

Query parameters:
- `?since=<RFC3339>` — only return snapshots at or after this time
- `?agent=<name>` — filter to a single agent (case-insensitive)

Response: `200 OK`, `Content-Type: application/json`

```json
[
  {
    "agent_name": "UXDev",
    "space": "AgentBossDevTeam",
    "status": "active",
    "inferred_status": "",
    "stale": false,
    "summary": "UXDev: implementing history endpoint",
    "timestamp": "2026-03-07T01:25:00Z"
  }
]
```

### GET /spaces/{space}/agent/{name}/history

Per-agent history. Equivalent to `/history?agent={name}`. Same query params (`?since=`).

## Rendering Approach

### Layout

A new collapsible panel `panel-gantt` inserted between the Session Overview and Agent Cards panels. Rendered with pure HTML/CSS using CSS grid — no canvas, no SVG, no external libraries.

```
+----------------------------------------------------------+
| Timeline  [last 2h]  [last 6h]  [last 24h]  [Auto]      |
+------------------------------------------+---------------+
| UXDev     |####active###|##done##|        |              |
| UXSME     |  ##active################    |              |
| DataMgr   |#####blocked##|##active######|              |
+------------------------------------------+---------------+
            ^                              ^
         2h ago                          now
```

### Time Axis

- Default window: last 2 hours.
- User-selectable buttons: `2h`, `6h`, `24h`.
- X-axis: rendered as a pixel offset within the row container using percentage of window width.
- Grid lines: every 30 minutes (dashed vertical lines via CSS `background` gradient).

### Bar Rendering

Each agent gets one row. Each snapshot defines a bar segment starting at `snapshot.timestamp` and ending at the next snapshot's timestamp (or "now" for the final snapshot).

Formula:
```
left%  = (snapshot.timestamp - windowStart) / windowDuration * 100
width% = (segmentEnd - snapshot.timestamp) / windowDuration * 100
```

Bars are absolutely positioned within a relative container. Each bar is a `<div>` with inline `left` and `width` style.

### Color Coding

| Status | Color variable | Hex approx |
|--------|---------------|------------|
| `active` | `--green` | `#3fb950` |
| `blocked` | `--red` | `#f85149` |
| `done` | `--blue` | `#4d9eff` |
| `idle` | `--text3` | `#5c6578` |
| `error` | `--red` | `#f85149` (darker border) |
| `stale` | `--orange` | `#db6d28` |

Stale overrides the reported status color when `stale === true` or `inferred_status === "stale"`.

### Interaction Design

- **Hover tooltip**: hovering a bar segment shows a floating tooltip with:
  - Agent name
  - Status (and inferred status if set)
  - Timestamp range (start → end)
  - Summary (truncated to 120 chars)
- **Click to filter**: clicking an agent row label filters the agent cards panel to highlight that agent.
- **Row label**: left-aligned, fixed 90px wide, same agent name styling as cards panel.
- **Time label axis**: bottom row showing timestamps at 30-min intervals.

### Auto-refresh

- On SSE `agent_updated` event: re-fetch `/history?since=<windowStart>` and re-render.
- Fallback (if SSE unavailable): poll every 30 seconds.
- Window end is always "now" — the timeline shifts forward automatically.

## Implementation Notes

### HTML Structure

```html
<div class="panel" id="panel-gantt">
  <div class="panel-header">
    <span class="panel-title">Timeline</span>
    <div class="gantt-controls">
      <button class="gantt-btn active" onclick="setGanttWindow(2)">2h</button>
      <button class="gantt-btn" onclick="setGanttWindow(6)">6h</button>
      <button class="gantt-btn" onclick="setGanttWindow(24)">24h</button>
    </div>
  </div>
  <div id="gantt-body"></div>
</div>
```

### CSS Requirements

- `.gantt-row`: `display: grid; grid-template-columns: 90px 1fr; align-items: center; height: 28px;`
- `.gantt-track`: `position: relative; height: 18px; background: var(--bg3); border-radius: 3px;`
- `.gantt-bar`: `position: absolute; height: 100%; border-radius: 3px; cursor: pointer; transition: opacity .15s;`
- `.gantt-bar:hover`: `opacity: .8;`
- `.gantt-tooltip`: fixed position, `z-index: 1000`, dark background, appears on mouseover via JS.

### JavaScript

All logic inline in `mission-control.html`:
- `ganttWindowHours = 2` — current window state
- `ganttData = []` — cached snapshot array
- `loadGantt(spaceName)` — fetches `/spaces/{space}/history?since=<2h ago>`, stores in `ganttData`, calls `renderGantt()`
- `renderGantt()` — builds HTML string, sets `document.getElementById('gantt-body').innerHTML`
- `setGanttWindow(hours)` — updates `ganttWindowHours`, re-fetches, updates button active state
- Hook into existing SSE `agent_updated` listener to call `loadGantt(SPACE)`

## Acceptance Criteria

1. Gantt panel renders within the existing dashboard without layout breakage.
2. Each known agent appears as a row.
3. Status bars accurately reflect history from `/history` endpoint.
4. Default window is 2 hours; buttons switch to 6h and 24h correctly.
5. Hover tooltip shows status, time range, and summary.
6. Panel auto-updates on SSE `agent_updated` events.
7. Zero external dependencies — pure inline HTML/CSS/JS.
8. Works when history is empty (shows empty rows with placeholder text).
