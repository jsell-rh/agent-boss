import type {
  SpaceSummary,
  KnowledgeSpace,
  AgentUpdate,
  TmuxAgentStatus,
  InterruptMetrics,
  Interrupt,
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
}

export const api = new ApiClient()
export default api
