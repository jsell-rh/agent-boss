// Types matching the Go backend (internal/coordinator/types.go)

export type AgentStatus = 'active' | 'blocked' | 'done' | 'idle' | 'error'

export interface Table {
  headers: string[]
  rows: string[][]
}

export interface Section {
  title: string
  items?: string[]
  table?: Table
}

export interface AgentDocument {
  slug: string
  title: string
  content: string
}

export type MessagePriority = 'info' | 'directive' | 'urgent'

export type MessageType = 'text' | 'decision'

export interface AgentMessage {
  id: string
  message: string
  sender: string
  priority?: MessagePriority
  type?: MessageType
  resolved?: boolean
  resolution?: string
  timestamp: string
  read?: boolean
  read_at?: string
}

export interface AgentUpdate {
  status: AgentStatus
  summary: string
  branch?: string
  worktree?: string
  pr?: string
  phase?: string
  mood?: string
  test_count?: number
  items?: string[]
  sections?: Section[]
  questions?: string[]
  blockers?: string[]
  next_steps?: string
  free_text?: string
  documents?: AgentDocument[]
  session_id?: string
  backend_type?: string
  jira?: string
  repo_url?: string
  messages?: AgentMessage[]
  updated_at: string
  stale?: boolean
  inferred_status?: string
  // Hierarchy fields (optional — omitted for flat agents)
  parent?: string
  children?: string[]
  role?: string
}

// GET /personas and GET /personas/{id}
export interface Persona {
  id: string
  name: string
  description: string
  prompt: string
  version: number
  created_at: string
  updated_at: string
}

export interface PersonaVersion {
  version: number
  prompt: string
  updated_at: string
}

export interface PersonaAgentInfo {
  space: string
  agent: string
  pinned_version: number
  current_version: number
  outdated: boolean
  session_id?: string
}

export interface PersonaRef {
  id: string
  pinned_version?: number
}

// GET/PATCH /spaces/{space}/agent/{name}/config
export interface AgentConfig {
  work_dir?: string
  initial_prompt?: string
  personas?: PersonaRef[]
  backend?: string
  command?: string
  repo_url?: string
  repos?: { url: string; branch?: string }[]
  model?: string
}

// GET /spaces/{space}/hierarchy response
export interface HierarchyNode {
  agent: string
  parent?: string
  children: string[]
  depth: number
  role?: string
}

export interface HierarchyTree {
  space: string
  roots: string[]
  nodes: Record<string, HierarchyNode>
}

export interface KnowledgeSpace {
  name: string
  agents: Record<string, AgentUpdate>
  shared_contracts?: string
  archive?: string
  created_at: string
  updated_at: string
}

// GET /spaces response item
export interface SpaceSummary {
  name: string
  agent_count: number
  attention_count: number
  archive?: string
  created_at: string
  updated_at: string
}

// GET /spaces/{space}/api/session-status response values
export interface SessionAgentStatus {
  exists: boolean
  idle: boolean
  needs_approval: boolean
  tool_name: string
  prompt_text: string
}

// GET /spaces/{space}/history response items
export interface StatusSnapshot {
  agent_name: string
  space: string
  status: AgentStatus
  inferred_status?: string
  stale?: boolean
  timestamp: string
}

// GET /spaces/{space}/agent/{name}/introspect response
export interface IntrospectResponse {
  agent: string
  session_id?: string
  session_exists: boolean
  idle: boolean
  needs_approval: boolean
  tool_name?: string
  prompt_text?: string
  lines: string[]
  captured_at: string
}

// GET /spaces/{space}/factory/interrupts response items
// Field names match Go struct json tags in interrupts.go
export interface InterruptResolution {
  resolved_by: string
  answer: string
  resolved_at: string
  wait_seconds: number
}

export interface Interrupt {
  id: string
  space: string
  agent: string
  type: 'decision' | 'approval' | 'staleness' | 'review' | 'sequencing'
  question: string
  context?: Record<string, string>
  resolution?: InterruptResolution
  created_at: string
}

// GET /spaces/{space}/factory/metrics response
// Field names match Go struct json tags in interrupts.go
export interface InterruptMetrics {
  total_interrupts: number
  human_interrupts: number
  auto_resolved: number
  pending_interrupts: number
  by_type: Record<string, number>
  by_agent: Record<string, number>
  avg_wait_seconds: number
}

// ── Task Management ────────────────────────────────────────────────

export type TaskStatus = 'backlog' | 'in_progress' | 'review' | 'done' | 'blocked'
export type TaskPriority = 'low' | 'medium' | 'high' | 'urgent'

export interface TaskComment {
  id: string
  author: string
  body: string
  created_at: string
}

export interface TaskEvent {
  id: string
  type: string   // "created" | "moved" | "assigned" | "commented" | "updated"
  by: string
  detail: string
  created_at: string
}

export interface Task {
  id: string
  space: string
  title: string
  description?: string
  status: TaskStatus
  priority?: TaskPriority
  assigned_to?: string
  created_by: string
  labels?: string[]
  parent_task?: string
  subtasks?: string[]
  linked_branch?: string
  linked_pr?: string
  created_at: string
  updated_at: string
  due_at?: string
  comments?: TaskComment[]
  events?: TaskEvent[]
}

export const TASK_STATUS_COLUMNS: TaskStatus[] = ['backlog', 'in_progress', 'review', 'blocked', 'done']

export const TASK_STATUS_LABELS: Record<TaskStatus, string> = {
  backlog: 'Backlog',
  in_progress: 'In Progress',
  review: 'Review',
  done: 'Done',
  blocked: 'Blocked',
}

export const TASK_PRIORITY_LABELS: Record<TaskPriority, string> = {
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  urgent: 'Urgent',
}

export const TASK_PRIORITY_COLOR: Record<TaskPriority, string> = {
  low: 'bg-muted text-muted-foreground',
  medium: 'bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300',
  high: 'bg-orange-100 text-orange-800 dark:bg-orange-900/30 dark:text-orange-300',
  urgent: 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300',
}

// SSE event data shapes
export interface SSEAgentUpdated {
  space: string
  agent: string
  status: string
  summary: string
}

export interface SSEAgentRemoved {
  space: string
  agent: string
}

export interface SSEAgentSpawned {
  space: string
  agent: string
}

export interface SSETaskUpdated {
  id: string
  space: string
  status?: string
  title?: string
  assigned_to?: string
  deleted?: boolean
}

export interface SSEAgentMessage {
  space: string
  agent: string
  sender: string
  message: string
}

export interface SSESessionLiveness {
  agent: string
  session: string
  exists: boolean
  idle: boolean
  needs_approval: boolean
  tool_name?: string
  prompt_text?: string
}

export interface SSEBroadcastProgress {
  space: string
  message: string
}

// Status color class mapping — Red Hat brand palette semantics:
//   active  -> green  (success)
//   blocked -> red    (attention/brand, not "negative")
//   done    -> teal   (info)
//   idle    -> gray   (muted)
//   error   -> orange (danger per Red Hat guidelines)
export const statusColorClass: Record<AgentStatus, string> = {
  active: 'status-green',
  blocked: 'status-red',
  done: 'status-teal',
  idle: 'status-gray',
  error: 'status-orange',
}

// Agent/Task Status display labels and tooltips.
// These describe the agent's self-reported work status.
export const STATUS_DISPLAY: Record<AgentStatus, { label: string; tooltip: string }> = {
  active:  { label: 'Working', tooltip: 'Agent reports it is actively working on its task' },
  blocked: { label: 'Blocked', tooltip: 'Agent reports it cannot proceed — check blockers' },
  done:    { label: 'Done',    tooltip: 'Agent reports its assigned task is complete' },
  idle:    { label: 'Idle',    tooltip: 'Agent is not currently assigned work' },
  error:   { label: 'Error',   tooltip: 'Agent encountered an unrecoverable error' },
}

// Session Status display labels and tooltips.
// These describe the observed state of the agent's session.
export type SessionDisplayState = 'ready' | 'running' | 'approval' | 'offline' | 'no-session'

export const SESSION_STATUS_DISPLAY: Record<SessionDisplayState, { label: string; tooltip: string }> = {
  ready:      { label: 'Ready',      tooltip: 'Terminal is at a prompt, ready for input' },
  running:    { label: 'Running',    tooltip: 'A process is actively running in the terminal' },
  approval:   { label: 'Approval',   tooltip: 'Agent is waiting for tool-use approval' },
  offline:    { label: 'Offline',    tooltip: 'No session found for this agent' },
  'no-session': { label: 'No Session', tooltip: 'No session registered for this agent' },
}

/** Derive the SessionDisplayState from a SessionAgentStatus (or null). */
export function getSessionDisplayState(tmux: SessionAgentStatus | null | undefined): SessionDisplayState {
  if (!tmux) return 'no-session'
  if (!tmux.exists) return 'offline'
  if (tmux.needs_approval) return 'approval'
  if (tmux.idle) return 'ready'
  return 'running'
}
