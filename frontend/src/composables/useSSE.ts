import { ref, type Ref } from 'vue'
import type {
  SSEAgentUpdated,
  SSEAgentRemoved,
  SSEAgentMessage,
  SSETmuxLiveness,
  SSEBroadcastProgress,
  SSETaskUpdated,
} from '@/types'

// All SSE event types emitted by the Go backend
export type SSEEventType =
  | 'agent_updated'
  | 'agent_removed'
  | 'space_deleted'
  | 'agent_message'
  | 'tmux_liveness'
  | 'broadcast_complete'
  | 'broadcast_progress'
  | 'task_updated'

export type SSEEventMap = {
  agent_updated: SSEAgentUpdated
  agent_removed: SSEAgentRemoved
  space_deleted: string // space name
  agent_message: SSEAgentMessage
  tmux_liveness: SSETmuxLiveness[]
  broadcast_complete: unknown
  broadcast_progress: SSEBroadcastProgress
  task_updated: SSETaskUpdated
}

type SSECallback<T extends SSEEventType> = (data: SSEEventMap[T]) => void

interface SSECallbackEntry {
  type: SSEEventType
  callback: SSECallback<SSEEventType>
}

const INITIAL_RETRY_MS = 1000
const MAX_RETRY_MS = 30000

// Module-level singleton — all callers share one EventSource connection.
const connected: Ref<boolean> = ref(false)
const error: Ref<string | null> = ref(null)
let eventSource: EventSource | null = null
let retryMs = INITIAL_RETRY_MS
let retryTimer: ReturnType<typeof setTimeout> | null = null
let currentSpace: string | undefined
let intentionalClose = false
const callbacks: SSECallbackEntry[] = []

export function useSSE() {

  function on<T extends SSEEventType>(type: T, cb: SSECallback<T>): () => void {
    const entry: SSECallbackEntry = {
      type,
      callback: cb as SSECallback<SSEEventType>,
    }
    callbacks.push(entry)
    // Return unsubscribe function
    return () => {
      const idx = callbacks.indexOf(entry)
      if (idx !== -1) callbacks.splice(idx, 1)
    }
  }

  function emit(type: SSEEventType, data: unknown) {
    for (const entry of callbacks) {
      if (entry.type === type) {
        try {
          entry.callback(data as SSEEventMap[typeof type])
        } catch (err) {
          console.error(`[SSE] callback error for ${type}:`, err)
        }
      }
    }
  }

  function buildUrl(space?: string): string {
    if (space) {
      return `/spaces/${encodeURIComponent(space)}/events`
    }
    return '/events'
  }

  function parseSSEData(raw: string): unknown {
    try {
      return JSON.parse(raw)
    } catch {
      return raw
    }
  }

  function attachListeners(es: EventSource) {
    const eventTypes: SSEEventType[] = [
      'agent_updated',
      'agent_removed',
      'space_deleted',
      'agent_message',
      'tmux_liveness',
      'broadcast_complete',
      'broadcast_progress',
      'task_updated',
    ]

    for (const type of eventTypes) {
      es.addEventListener(type, (ev: MessageEvent) => {
        const data = parseSSEData(ev.data)
        emit(type, data)
      })
    }

    es.onopen = () => {
      connected.value = true
      error.value = null
      retryMs = INITIAL_RETRY_MS
    }

    es.onerror = () => {
      connected.value = false
      if (intentionalClose) return
      error.value = 'SSE connection lost, reconnecting...'
      es.close()
      eventSource = null
      scheduleReconnect()
    }
  }

  function scheduleReconnect() {
    if (retryTimer !== null) return
    retryTimer = setTimeout(() => {
      retryTimer = null
      if (!intentionalClose) {
        connect(currentSpace)
      }
    }, retryMs)
    // Exponential backoff with cap
    retryMs = Math.min(retryMs * 2, MAX_RETRY_MS)
  }

  function connect(space?: string) {
    disconnect()
    intentionalClose = false
    currentSpace = space

    const url = buildUrl(space)
    const es = new EventSource(url)
    eventSource = es
    attachListeners(es)
  }

  function disconnect() {
    intentionalClose = true
    if (retryTimer !== null) {
      clearTimeout(retryTimer)
      retryTimer = null
    }
    if (eventSource) {
      eventSource.close()
      eventSource = null
    }
    connected.value = false
    error.value = null
  }

  return {
    /** Whether the SSE connection is currently open */
    connected,
    /** Current error message, if any */
    error,
    /** Connect to global /events or space-scoped /spaces/{space}/events */
    connect,
    /** Close the connection and stop reconnecting */
    disconnect,
    /**
     * Register a callback for a specific SSE event type.
     * Returns an unsubscribe function.
     */
    on,
  }
}
