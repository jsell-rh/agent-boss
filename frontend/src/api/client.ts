import type {
  SpaceSummary,
  KnowledgeSpace,
  AgentUpdate,
  AgentConfig,
  SessionAgentStatus,
  InterruptMetrics,
  Interrupt,
  StatusSnapshot,
  IntrospectResponse,
  HierarchyTree,
  Task,
  TaskStatus,
  TaskPriority,
  Persona,
  PersonaVersion,
  PersonaAgentInfo,
} from '@/types'

/**
 * PR #86 changed `KnowledgeSpace.agents` from `map[string]*AgentUpdate` to
 * `map[string]*AgentRecord{status, config}`. Normalize the response so
 * all frontend components can keep accessing agents as plain AgentUpdate objects.
 */
function normalizeSpace(space: KnowledgeSpace): KnowledgeSpace {
  if (!space.agents) return space
  const normalized: Record<string, import('@/types').AgentUpdate> = {}
  for (const [name, record] of Object.entries(space.agents)) {
    const r = record as unknown as { status: import('@/types').AgentUpdate }
    normalized[name] = r.status ?? (record as unknown as import('@/types').AgentUpdate)
  }
  return { ...space, agents: normalized }
}

const TOKEN_KEY = 'boss_api_token'

export function getStoredToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

export function setStoredToken(token: string): void {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token)
  } else {
    localStorage.removeItem(TOKEN_KEY)
  }
}

export class AuthRequiredError extends Error {
  constructor() {
    super('Authentication required — please enter your API token in Settings.')
    this.name = 'AuthRequiredError'
  }
}

// authRequired is set to true when any API call receives a 401.
// Components can watch this to show a login prompt.
import { ref } from 'vue'
export const authRequired = ref(false)

class ApiClient {
  private baseUrl: string

  constructor(baseUrl = '') {
    this.baseUrl = baseUrl
  }

  private authHeaders(method: string): HeadersInit {
    const token = getStoredToken()
    if (!token) return {}
    const upper = method.toUpperCase()
    if (upper === 'GET' || upper === 'HEAD' || upper === 'OPTIONS') return {}
    return { Authorization: `Bearer ${token}` }
  }

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const method = init?.method ?? 'GET'
    const headers = new Headers(init?.headers)
    for (const [k, v] of Object.entries(this.authHeaders(method))) {
      headers.set(k, v as string)
    }
    const res = await fetch(`${this.baseUrl}${path}`, { ...init, headers })
    if (res.status === 401) {
      setStoredToken('')
      authRequired.value = true
      throw new AuthRequiredError()
    }
    if (!res.ok) {
      const text = await res.text().catch(() => res.statusText)
      throw new Error(`${res.status} ${res.statusText}: ${text}`)
    }
    return res.json() as Promise<T>
  }

  private async requestVoid(path: string, init?: RequestInit): Promise<void> {
    const method = init?.method ?? 'GET'
    const headers = new Headers(init?.headers)
    for (const [k, v] of Object.entries(this.authHeaders(method))) {
      headers.set(k, v as string)
    }
    const res = await fetch(`${this.baseUrl}${path}`, { ...init, headers })
    if (res.status === 401) {
      setStoredToken('')
      authRequired.value = true
      throw new AuthRequiredError()
    }
    if (!res.ok) {
      const text = await res.text().catch(() => res.statusText)
      throw new Error(`${res.status} ${res.statusText}: ${text}`)
    }
  }

  // --------------- Spaces ---------------

  fetchSpaces(): Promise<SpaceSummary[]> {
    return this.request<SpaceSummary[]>('/spaces')
  }

  fetchSpace(space: string): Promise<KnowledgeSpace> {
    return this.request<KnowledgeSpace>(`/spaces/${encodeURIComponent(space)}/`, {
      headers: { Accept: 'application/json' },
    }).then(normalizeSpace)
  }

  deleteSpace(space: string): Promise<void> {
    return this.requestVoid(`/spaces/${encodeURIComponent(space)}/`, {
      method: 'DELETE',
    })
  }

  createSpace(space: string): Promise<void> {
    return this.requestVoid(`/spaces/${encodeURIComponent(space)}/contracts`, {
      method: 'POST',
      body: '',
    })
  }

  archiveSpace(space: string, body?: string): Promise<void> {
    return this.requestVoid(`/spaces/${encodeURIComponent(space)}/archive`, {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain' },
      body: body !== undefined ? body : `Archived at ${new Date().toISOString()}`,
    })
  }

  fetchAgentMessages(space: string, agent: string, since?: string): Promise<{ agent: string; cursor: string; messages: import('@/types').AgentMessage[] }> {
    const url = since
      ? `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/messages?since=${encodeURIComponent(since)}`
      : `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/messages`
    return this.request(url)
  }

  ackMessage(space: string, agent: string, messageId: string, agentName: string): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/message/${encodeURIComponent(messageId)}/ack`,
      { method: 'POST', headers: { 'X-Agent-Name': agentName } },
    )
  }

  fetchAgentHistory(space: string, agent: string): Promise<import('@/types').StatusSnapshot[]> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/history`,
    )
  }

  // --------------- Agents ---------------

  fetchAgent(space: string, agent: string): Promise<AgentUpdate> {
    return this.request<AgentUpdate>(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}`,
    )
  }

  deleteAgent(space: string, agent: string): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}`,
      { method: 'DELETE' },
    )
  }

  // --------------- Session Status ---------------

  fetchSessionStatus(space: string): Promise<Record<string, SessionAgentStatus>> {
    return this.request<Record<string, SessionAgentStatus>>(
      `/spaces/${encodeURIComponent(space)}/api/session-status`,
    )
  }

  // --------------- Interrupts / Metrics ---------------

  fetchInterrupts(space: string): Promise<Interrupt[]> {
    return this.request<Interrupt[]>(
      `/spaces/${encodeURIComponent(space)}/factory/interrupts`,
    )
  }

  fetchMetrics(space: string): Promise<InterruptMetrics> {
    return this.request<InterruptMetrics>(
      `/spaces/${encodeURIComponent(space)}/factory/metrics`,
    )
  }

  // --------------- Hierarchy ---------------

  fetchHierarchy(space: string): Promise<HierarchyTree> {
    return this.request<HierarchyTree>(
      `/spaces/${encodeURIComponent(space)}/hierarchy`,
    )
  }

  // --------------- History ---------------

  fetchHistory(space: string, sinceMs?: number, agent?: string): Promise<StatusSnapshot[]> {
    const params = new URLSearchParams()
    if (sinceMs !== undefined) {
      params.set('since', new Date(Date.now() - sinceMs).toISOString())
    }
    if (agent) params.set('agent', agent)
    const qs = params.toString()
    return this.request<StatusSnapshot[]>(
      `/spaces/${encodeURIComponent(space)}/history${qs ? '?' + qs : ''}`,
    )
  }

  // --------------- Events ---------------

  fetchEvents(space: string): Promise<string[]> {
    return this.request<string[]>(
      `/spaces/${encodeURIComponent(space)}/api/events`,
    )
  }

  // --------------- Actions ---------------

  approveAgent(space: string, agent: string, always = false): Promise<void> {
    const qs = always ? '?always=true' : ''
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/approve/${encodeURIComponent(agent)}${qs}`,
      { method: 'POST' },
    )
  }

  replyToAgent(space: string, agent: string, text: string): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/reply/${encodeURIComponent(agent)}`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: text }),
      },
    )
  }

  broadcastSpace(space: string): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/broadcast`,
      { method: 'POST' },
    )
  }

  broadcastAgent(space: string, agent: string): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/broadcast/${encodeURIComponent(agent)}`,
      { method: 'POST' },
    )
  }

  sendMessage(
    space: string,
    agent: string,
    message: string,
    sender: string,
    replyToDecisionId?: string,
  ): Promise<void> {
    const body: Record<string, string> = { message }
    if (replyToDecisionId) body.reply_to_decision_id = replyToDecisionId
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/message`,
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Agent-Name': sender,
        },
        body: JSON.stringify(body),
      },
    )
  }

  resolveDecision(space: string, agent: string, messageId: string, resolution: string): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/message/${encodeURIComponent(messageId)}/resolve`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ resolution }),
      },
    )
  }

  resolveInterrupt(space: string, id: string, answer = 'dismissed'): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/factory/interrupts`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, answer, resolved_by: 'human' }),
      },
    )
  }

  dismissItem(space: string, agent: string, index: number, type: 'question' | 'blocker' = 'question'): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/dismiss/${encodeURIComponent(agent)}`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type, index }),
      },
    )
  }

  // --------------- Lifecycle ---------------

  createAgent(
    space: string,
    spec: {
      name: string
      work_dir?: string
      command?: string
      backend?: 'tmux' | 'ambient'
      width?: number
      height?: number
      parent?: string
      role?: string
      repos?: { url: string; branch?: string }[]
      task?: string
      initial_message?: string
    },
  ): Promise<{ ok: boolean; agent: string; backend: string; session: string; space: string }> {
    return this.request(`/spaces/${encodeURIComponent(space)}/agents`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(spec),
    })
  }

  spawnAgent(space: string, agent: string, command?: string): Promise<{ ok: boolean; session_id: string }> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/spawn`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Agent-Name': agent },
        body: JSON.stringify(command ? { command } : {}),
      },
    )
  }

  stopAgent(space: string, agent: string): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/stop`,
      { method: 'POST', headers: { 'X-Agent-Name': agent } },
    )
  }

  interruptAgent(space: string, agent: string): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/interrupt`,
      { method: 'POST', headers: { 'X-Agent-Name': agent } },
    )
  }

  restartAgent(space: string, agent: string): Promise<{ ok: boolean; session_id: string }> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/restart`,
      { method: 'POST', headers: { 'X-Agent-Name': agent } },
    )
  }

  restartAll(space: string): Promise<{ ok: boolean; agents: string[]; count: number }> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/restart-all`,
      { method: 'POST' },
    )
  }

  introspectAgent(space: string, agent: string): Promise<IntrospectResponse> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/introspect`,
    )
  }

  getAgentConfig(space: string, agent: string): Promise<AgentConfig> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/config`,
    )
  }

  updateAgentConfig(space: string, agent: string, config: Partial<AgentConfig>): Promise<AgentConfig> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/config`,
      {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json', 'X-Agent-Name': agent },
        body: JSON.stringify(config),
      },
    )
  }

  duplicateAgent(space: string, agent: string, newName: string): Promise<{ ok: boolean; agent: string }> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/duplicate`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ new_name: newName }),
      },
    )
  }

  // --------------- Tasks ---------------

  fetchTasks(space: string, filters?: { status?: TaskStatus; assigned_to?: string; label?: string; search?: string }, signal?: AbortSignal): Promise<Task[]> {
    const params = new URLSearchParams()
    if (filters?.status) params.set('status', filters.status)
    if (filters?.assigned_to) params.set('assigned_to', filters.assigned_to)
    if (filters?.label) params.set('label', filters.label)
    if (filters?.search) params.set('search', filters.search)
    const qs = params.toString()
    return this.request<{ tasks: Task[]; total: number }>(
      `/spaces/${encodeURIComponent(space)}/tasks${qs ? '?' + qs : ''}`,
      signal ? { signal } : undefined,
    ).then(r => r.tasks ?? [])
  }

  createTask(space: string, task: {
    title: string
    description?: string
    status?: TaskStatus
    priority?: TaskPriority
    assigned_to?: string
    labels?: string[]
    parent_task?: string
    due_at?: string
  }, actor = 'boss'): Promise<Task> {
    return this.request<Task>(
      `/spaces/${encodeURIComponent(space)}/tasks`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Agent-Name': actor },
        body: JSON.stringify(task),
      },
    )
  }

  fetchTask(space: string, id: string): Promise<Task> {
    return this.request<Task>(
      `/spaces/${encodeURIComponent(space)}/tasks/${encodeURIComponent(id)}`,
    )
  }

  updateTask(space: string, id: string, patch: Partial<Pick<Task, 'title' | 'description' | 'priority' | 'assigned_to' | 'labels' | 'linked_branch' | 'linked_pr'>> & { due_at?: string | null }, actor = 'boss'): Promise<Task> {
    return this.request<Task>(
      `/spaces/${encodeURIComponent(space)}/tasks/${encodeURIComponent(id)}`,
      {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', 'X-Agent-Name': actor },
        body: JSON.stringify(patch),
      },
    )
  }

  deleteTask(space: string, id: string, actor = 'boss'): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/tasks/${encodeURIComponent(id)}`,
      { method: 'DELETE', headers: { 'X-Agent-Name': actor } },
    )
  }

  moveTask(space: string, id: string, status: TaskStatus, actor = 'boss', reason?: string): Promise<Task> {
    return this.request<Task>(
      `/spaces/${encodeURIComponent(space)}/tasks/${encodeURIComponent(id)}/move`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Agent-Name': actor },
        body: JSON.stringify(reason ? { status, reason } : { status }),
      },
    )
  }

  assignTask(space: string, id: string, assignedTo: string, actor = 'boss', reason?: string): Promise<Task> {
    return this.request<Task>(
      `/spaces/${encodeURIComponent(space)}/tasks/${encodeURIComponent(id)}/assign`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Agent-Name': actor },
        body: JSON.stringify(reason ? { assigned_to: assignedTo, reason } : { assigned_to: assignedTo }),
      },
    )
  }

  addTaskComment(space: string, id: string, body: string, actor = 'boss'): Promise<Task> {
    return this.request<Task>(
      `/spaces/${encodeURIComponent(space)}/tasks/${encodeURIComponent(id)}/comment`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Agent-Name': actor },
        body: JSON.stringify({ body }),
      },
    )
  }

  createSubtask(space: string, parentId: string, task: {
    title: string
    description?: string
    priority?: TaskPriority
    assigned_to?: string
    labels?: string[]
  }, actor = 'boss'): Promise<Task> {
    return this.request<Task>(
      `/spaces/${encodeURIComponent(space)}/tasks/${encodeURIComponent(parentId)}/subtasks`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Agent-Name': actor },
        body: JSON.stringify(task),
      },
    )
  }



  // --------------- Contracts ---------------

  fetchContracts(space: string): Promise<string> {
    return fetch(`${this.baseUrl}/spaces/${encodeURIComponent(space)}/contracts`)
      .then(r => r.text())
  }

  saveContracts(space: string, content: string): Promise<void> {
    return this.requestVoid(`/spaces/${encodeURIComponent(space)}/contracts`, {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain' },
      body: content,
    })
  }

  fetchDefaultContracts(space: string): Promise<string> {
    return fetch(`${this.baseUrl}/spaces/${encodeURIComponent(space)}/contracts/default`)
      .then(r => r.text())
  }

  // --------------- Personas ---------------

  fetchPersonas(): Promise<Persona[]> {
    return this.request<Persona[]>('/personas')
  }

  createPersona(persona: { name: string; description: string; prompt: string }): Promise<Persona> {
    return this.request<Persona>('/personas', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(persona),
    })
  }

  getPersona(id: string): Promise<Persona> {
    return this.request<Persona>(`/personas/${encodeURIComponent(id)}`)
  }

  updatePersona(id: string, patch: Partial<{ name: string; description: string; prompt: string }>): Promise<Persona> {
    return this.request<Persona>(`/personas/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch),
    })
  }

  deletePersona(id: string): Promise<void> {
    return this.requestVoid(`/personas/${encodeURIComponent(id)}`, { method: 'DELETE' })
  }

  fetchPersonaHistory(id: string): Promise<PersonaVersion[]> {
    return this.request<PersonaVersion[]>(`/personas/${encodeURIComponent(id)}/history`)
  }

  revertPersona(id: string, version: number): Promise<Persona> {
    return this.request<Persona>(`/personas/${encodeURIComponent(id)}/revert`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ version }),
    })
  }

  fetchPersonaAgents(id: string): Promise<PersonaAgentInfo[]> {
    return this.request<PersonaAgentInfo[]>(`/personas/${encodeURIComponent(id)}/agents`)
  }

  restartOutdatedPersonaAgents(id: string): Promise<{ restarted: string[]; errors: string[]; total: number }> {
    return this.request<{ restarted: string[]; errors: string[]; total: number }>(
      `/personas/${encodeURIComponent(id)}/restart-outdated`,
      { method: 'POST' },
    )
  }

  // --------------- Settings ---------------

  getSettings(): Promise<{ allow_skip_permissions: boolean }> {
    return this.request<{ allow_skip_permissions: boolean }>('/settings')
  }

  updateSettings(patch: { allow_skip_permissions: boolean }): Promise<{ allow_skip_permissions: boolean }> {
    return this.request<{ allow_skip_permissions: boolean }>('/settings', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch),
    })
  }
}

export const api = new ApiClient()
export default api
