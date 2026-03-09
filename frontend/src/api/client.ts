import type {
  SpaceSummary,
  KnowledgeSpace,
  AgentUpdate,
  TmuxAgentStatus,
  InterruptMetrics,
  Interrupt,
  StatusSnapshot,
  IntrospectResponse,
  HierarchyTree,
  Task,
  TaskStatus,
  TaskPriority,
} from '@/types'

class ApiClient {
  private baseUrl: string

  constructor(baseUrl = '') {
    this.baseUrl = baseUrl
  }

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, init)
    if (!res.ok) {
      const text = await res.text().catch(() => res.statusText)
      throw new Error(`${res.status} ${res.statusText}: ${text}`)
    }
    return res.json() as Promise<T>
  }

  private async requestVoid(path: string, init?: RequestInit): Promise<void> {
    const res = await fetch(`${this.baseUrl}${path}`, init)
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
    })
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

  // --------------- Tmux ---------------

  fetchTmuxStatus(space: string): Promise<Record<string, TmuxAgentStatus>> {
    return this.request<Record<string, TmuxAgentStatus>>(
      `/spaces/${encodeURIComponent(space)}/api/tmux-status`,
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

  approveAgent(space: string, agent: string): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/approve/${encodeURIComponent(agent)}`,
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
  ): Promise<void> {
    return this.requestVoid(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/message`,
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Agent-Name': sender,
        },
        body: JSON.stringify({ message }),
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
      backend?: 'tmux' | 'cloud'
      width?: number
      height?: number
      parent?: string
      role?: string
    },
  ): Promise<{ ok: boolean; agent: string; backend: string; session: string; space: string }> {
    return this.request(`/spaces/${encodeURIComponent(space)}/agents`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(spec),
    })
  }

  spawnAgent(space: string, agent: string, command?: string): Promise<{ ok: boolean; tmux_session: string }> {
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

  restartAgent(space: string, agent: string): Promise<{ ok: boolean; tmux_session: string }> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/restart`,
      { method: 'POST', headers: { 'X-Agent-Name': agent } },
    )
  }

  introspectAgent(space: string, agent: string): Promise<IntrospectResponse> {
    return this.request(
      `/spaces/${encodeURIComponent(space)}/agent/${encodeURIComponent(agent)}/introspect`,
    )
  }

  // --------------- Tasks ---------------

  fetchTasks(space: string, filters?: { status?: TaskStatus; assigned_to?: string; label?: string; search?: string }): Promise<Task[]> {
    const params = new URLSearchParams()
    if (filters?.status) params.set('status', filters.status)
    if (filters?.assigned_to) params.set('assigned_to', filters.assigned_to)
    if (filters?.label) params.set('label', filters.label)
    if (filters?.search) params.set('search', filters.search)
    const qs = params.toString()
    return this.request<{ tasks: Task[]; total: number }>(
      `/spaces/${encodeURIComponent(space)}/tasks${qs ? '?' + qs : ''}`,
    ).then(r => r.tasks ?? [])
  }

  createTask(space: string, task: {
    title: string
    description?: string
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
}

export const api = new ApiClient()
export default api
