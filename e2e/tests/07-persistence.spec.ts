/**
 * 07 — Persistence: Server Restart Tests
 *
 * Verifies that all data survives a complete server restart:
 * - Agent status updates
 * - Task definitions and state
 * - Messages
 * - Space contracts
 *
 * These tests RESTART THE SERVER. They must run serially (workers: 1).
 */
import { test, expect } from '../fixtures/index.ts'

// Use a fixed space name so we can find data after restart
const PERSIST_SPACE = 'persistence-test'

test.describe('Persistence: Data Survives Restart', () => {
  test.describe.configure({ mode: 'serial' })

  // Phase 1: Create data before restart
  test('create agents, tasks, and messages', async ({ api }) => {
    // Create space
    await api.postText(`/spaces/${PERSIST_SPACE}/contracts`, '# Persistence test space\n')

    // Create agents
    await api.post(
      `/spaces/${PERSIST_SPACE}/agent/PersistedAgent`,
      {
        status: 'active',
        summary: 'PersistedAgent: will survive restart',
        branch: 'feat/persistence',
        test_count: 42,
        items: ['item persisted'],
      },
      'PersistedAgent',
    )

    await api.post(
      `/spaces/${PERSIST_SPACE}/agent/AnotherAgent`,
      {
        status: 'done',
        summary: 'AnotherAgent: completed before restart',
      },
      'AnotherAgent',
    )

    // Create tasks
    const task1 = await api.postJSON<{ id: string }>(
      `/spaces/${PERSIST_SPACE}/tasks`,
      { title: 'Persistent Task', description: 'Must survive restart', priority: 'urgent' },
      'operator',
    )

    // Move task to in_progress
    await api.postJSON(
      `/spaces/${PERSIST_SPACE}/tasks/${task1.id}/move`,
      { status: 'in_progress', reason: 'pre-restart' },
      'operator',
    )

    // Add comment
    await api.postJSON(
      `/spaces/${PERSIST_SPACE}/tasks/${task1.id}/comment`,
      { body: 'Comment before restart' },
      'operator',
    )

    // Create a second task
    await api.postJSON(
      `/spaces/${PERSIST_SPACE}/tasks`,
      { title: 'Second Persistent Task', assigned_to: 'PersistedAgent' },
      'operator',
    )

    // Send a message
    await api.post(
      `/spaces/${PERSIST_SPACE}/agent/PersistedAgent/message`,
      { message: 'Message sent before restart' },
      'operator',
    )

    // Update contracts
    await api.postText(
      `/spaces/${PERSIST_SPACE}/contracts`,
      '# Updated contract\n- Rule persisted across restart',
    )

    // Verify data exists before restart
    const spaces = await api.getJSON<{ name: string }[]>('/spaces')
    expect(spaces.some(s => s.name === PERSIST_SPACE)).toBe(true)

    const agent = await api.getJSON<{ status: string; test_count: number }>(
      `/spaces/${PERSIST_SPACE}/agent/PersistedAgent`,
    )
    expect(agent.status).toBe('active')
    expect(agent.test_count).toBe(42)

    const tasks = await api.getJSON<{ tasks: { id: string; status: string }[] }>(
      `/spaces/${PERSIST_SPACE}/tasks`,
    )
    expect(tasks.tasks.length).toBeGreaterThanOrEqual(2)
  })

  // Phase 2: Restart server
  test('restart the server', async ({ server }) => {
    await server.restart()
    // Verify server is back up by checking spaces endpoint
    const r = await fetch('http://localhost:18899/spaces')
    expect(r.status).toBe(200)
  })

  // Phase 3: Verify everything survived
  test('spaces survive restart', async ({ api }) => {
    const spaces = await api.getJSON<{ name: string }[]>('/spaces')
    expect(spaces.some(s => s.name === PERSIST_SPACE)).toBe(true)
  })

  test('agent data survives restart', async ({ api }) => {
    const agent = await api.getJSON<{
      status: string
      summary: string
      branch: string
      test_count: number
      items: string[]
    }>(`/spaces/${PERSIST_SPACE}/agent/PersistedAgent`)

    // AgentUpdate does not have a 'name' field (it's the map key)
    expect(agent.status).toBe('active')
    expect(agent.summary).toContain('PersistedAgent')
    expect(agent.branch).toBe('feat/persistence')
    expect(agent.test_count).toBe(42)
    expect(agent.items).toContain('item persisted')
  })

  test('done agent status survives restart', async ({ api }) => {
    const agent = await api.getJSON<{ status: string }>(`/spaces/${PERSIST_SPACE}/agent/AnotherAgent`)
    expect(agent.status).toBe('done')
  })

  test('tasks survive restart with correct status', async ({ api }) => {
    const tasks = await api.getJSON<{ tasks: { title: string; status: string; priority: string }[] }>(
      `/spaces/${PERSIST_SPACE}/tasks`,
    )
    expect(tasks.tasks.length).toBeGreaterThanOrEqual(2)

    const persistent = tasks.tasks.find(t => t.title === 'Persistent Task')
    expect(persistent).toBeDefined()
    expect(persistent!.status).toBe('in_progress')
    expect(persistent!.priority).toBe('urgent')
  })

  test('task comments survive restart', async ({ api }) => {
    const tasks = await api.getJSON<{ tasks: { id: string; title: string }[] }>(
      `/spaces/${PERSIST_SPACE}/tasks`,
    )
    const persistent = tasks.tasks.find(t => t.title === 'Persistent Task')!
    const task = await api.getJSON<{ comments: { body: string }[] }>(
      `/spaces/${PERSIST_SPACE}/tasks/${persistent.id}`,
    )
    expect(task.comments.some(c => c.body === 'Comment before restart')).toBe(true)
  })

  test('task assignment survives restart', async ({ api }) => {
    const tasks = await api.getJSON<{ tasks: { title: string; assigned_to: string }[] }>(
      `/spaces/${PERSIST_SPACE}/tasks`,
    )
    const assigned = tasks.tasks.find(t => t.title === 'Second Persistent Task')
    expect(assigned).toBeDefined()
    expect(assigned!.assigned_to).toBe('PersistedAgent')
  })

  test('messages survive restart', async ({ api }) => {
    const msgs = await api.getJSON<{ messages: { message: string; sender: string }[] }>(
      `/spaces/${PERSIST_SPACE}/agent/PersistedAgent/messages`,
    )
    expect(msgs.messages.some(m => m.message === 'Message sent before restart')).toBe(true)
  })

  test('space raw output reflects persisted data after restart', async ({ api }) => {
    const raw = await (await api.get(`/spaces/${PERSIST_SPACE}/raw`)).text()
    expect(raw).toContain('PersistedAgent')
    expect(raw).toContain(PERSIST_SPACE)
  })

  test('space contracts survive restart', async ({ api }) => {
    const raw = await (await api.get(`/spaces/${PERSIST_SPACE}/raw`)).text()
    expect(raw).toContain('Rule persisted across restart')
  })

  // Cleanup
  test('cleanup persistence test space', async ({ api }) => {
    const r = await api.del(`/spaces/${PERSIST_SPACE}/`)
    expect([200, 204, 404]).toContain(r.status)
  })
})
