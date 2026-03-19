/**
 * 03 — API: Task Management
 *
 * Covers: create, list, get, update, move, assign, comment, subtask, delete.
 */
import { test, expect } from '../fixtures/index.ts'

test.describe('API: Tasks', () => {
  test('create task returns task with ID', async ({ space, api }) => {
    const task = await api.postJSON<{ id: string; title: string; status: string }>(
      `/spaces/${space}/tasks`,
      { title: 'My E2E Task', description: 'Testing task creation', priority: 'medium' },
      'operator',
    )
    expect(task.id).toMatch(/^TASK-/)
    expect(task.title).toBe('My E2E Task')
    expect(task.status).toBe('backlog')
  })

  test('list tasks returns array with total', async ({ space, api }) => {
    await api.postJSON(`/spaces/${space}/tasks`, { title: 'Task 1' }, 'operator')
    await api.postJSON(`/spaces/${space}/tasks`, { title: 'Task 2' }, 'operator')
    const result = await api.getJSON<{ tasks: unknown[]; total: number }>(`/spaces/${space}/tasks`)
    expect(Array.isArray(result.tasks)).toBe(true)
    expect(result.total).toBeGreaterThanOrEqual(2)
  })

  test('filter tasks by status', async ({ space, api }) => {
    const t = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Backlog task' }, 'operator')

    // Move to in_progress
    await api.postJSON(`/spaces/${space}/tasks/${t.id}/move`, { status: 'in_progress' }, 'operator')

    const inProgress = await api.getJSON<{ tasks: { id: string; status: string }[] }>(
      `/spaces/${space}/tasks?status=in_progress`,
    )
    expect(inProgress.tasks.some(tk => tk.id === t.id)).toBe(true)

    const backlog = await api.getJSON<{ tasks: { id: string }[] }>(
      `/spaces/${space}/tasks?status=backlog`,
    )
    expect(backlog.tasks.some(tk => tk.id === t.id)).toBe(false)
  })

  test('filter tasks by assigned_to', async ({ space, api }) => {
    const t = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Assigned task', assigned_to: 'DevAgent' }, 'operator')
    const assigned = await api.getJSON<{ tasks: { id: string }[] }>(
      `/spaces/${space}/tasks?assigned_to=DevAgent`,
    )
    expect(assigned.tasks.some(tk => tk.id === t.id)).toBe(true)
  })

  test('GET /spaces/{space}/tasks/{id} returns task', async ({ space, api }) => {
    const t = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Fetch by ID' }, 'operator')
    const fetched = await api.getJSON<{ id: string; title: string }>(`/spaces/${space}/tasks/${t.id}`)
    expect(fetched.id).toBe(t.id)
    expect(fetched.title).toBe('Fetch by ID')
  })

  test('PUT /tasks/{id} updates task fields', async ({ space, api }) => {
    const t = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Old Title', priority: 'low' }, 'operator')
    const updated = await api.putJSON<{ title: string; priority: string }>(
      `/spaces/${space}/tasks/${t.id}`,
      { title: 'New Title', priority: 'urgent' },
      'operator',
    )
    expect(updated.title).toBe('New Title')
    expect(updated.priority).toBe('urgent')
  })

  test('move task through all statuses', async ({ space, api }) => {
    const t = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Lifecycle task' }, 'operator')
    const statuses = ['in_progress', 'review', 'done'] as const

    for (const status of statuses) {
      const moved = await api.postJSON<{ status: string }>(
        `/spaces/${space}/tasks/${t.id}/move`,
        { status },
        'operator',
      )
      expect(moved.status).toBe(status)
    }

    // Verify final state
    const final = await api.getJSON<{ status: string }>(`/spaces/${space}/tasks/${t.id}`)
    expect(final.status).toBe('done')
  })

  test('move task records event in history', async ({ space, api }) => {
    const t = await api.postJSON<{ id: string; events: unknown[] }>(
      `/spaces/${space}/tasks`,
      { title: 'Event task' },
      'operator',
    )
    await api.postJSON(`/spaces/${space}/tasks/${t.id}/move`, { status: 'in_progress', reason: 'starting work' }, 'operator')
    const task = await api.getJSON<{ events: { type: string; detail?: string }[] }>(`/spaces/${space}/tasks/${t.id}`)
    expect(task.events.some(e => e.type === 'moved')).toBe(true)
  })

  test('assign task to agent', async ({ space, api }) => {
    const t = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Unassigned' }, 'operator')
    const assigned = await api.postJSON<{ assigned_to: string }>(
      `/spaces/${space}/tasks/${t.id}/assign`,
      { assigned_to: 'DevBot' },
      'operator',
    )
    expect(assigned.assigned_to).toBe('DevBot')
  })

  test('add comment to task', async ({ space, api }) => {
    const t = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Commented task' }, 'operator')
    const commented = await api.postJSON<{ comments: { body: string; by: string }[] }>(
      `/spaces/${space}/tasks/${t.id}/comment`,
      { body: 'This is a test comment.' },
      'operator',
    )
    expect(commented.comments).toBeDefined()
    expect(commented.comments.some(c => c.body === 'This is a test comment.')).toBe(true)
  })

  test('create subtask with parent_task relationship', async ({ space, api }) => {
    const parent = await api.postJSON<{ id: string }>(
      `/spaces/${space}/tasks`,
      { title: 'Parent Epic' },
      'operator',
    )
    const subtask = await api.postJSON<{ id: string; parent_task: string }>(
      `/spaces/${space}/tasks/${parent.id}/subtasks`,
      { title: 'Subtask 1', description: 'child work' },
      'operator',
    )
    expect(subtask.parent_task).toBe(parent.id)

    // Subtask should appear in task list
    const list = await api.getJSON<{ tasks: { id: string }[] }>(`/spaces/${space}/tasks`)
    expect(list.tasks.some(t => t.id === subtask.id)).toBe(true)
  })

  test('delete task removes it from list', async ({ space, api }) => {
    const t = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Temporary' }, 'operator')
    const r = await api.del(`/spaces/${space}/tasks/${t.id}`, 'operator')
    expect([200, 204]).toContain(r.status)

    const list = await api.getJSON<{ tasks: { id: string }[] }>(`/spaces/${space}/tasks`)
    expect(list.tasks.some(tk => tk.id === t.id)).toBe(false)
  })

  test('task search filter returns matching tasks', async ({ space, api }) => {
    await api.postJSON(`/spaces/${space}/tasks`, { title: 'Unique Banana Task' }, 'operator')
    await api.postJSON(`/spaces/${space}/tasks`, { title: 'Apple Task' }, 'operator')

    const results = await api.getJSON<{ tasks: { title: string }[] }>(
      `/spaces/${space}/tasks?search=Banana`,
    )
    expect(results.tasks.every(t => t.title.toLowerCase().includes('banana'))).toBe(true)
    expect(results.tasks.length).toBeGreaterThanOrEqual(1)
  })

  test('task with labels can be filtered by label', async ({ space, api }) => {
    await api.postJSON(`/spaces/${space}/tasks`, { title: 'Labeled task', labels: ['frontend', 'urgent'] }, 'operator')
    const results = await api.getJSON<{ tasks: { id: string; labels: string[] }[] }>(
      `/spaces/${space}/tasks?label=frontend`,
    )
    expect(results.tasks.every(t => t.labels?.includes('frontend'))).toBe(true)
  })

  test('GET non-existent task returns 404', async ({ space, api }) => {
    const r = await api.get(`/spaces/${space}/tasks/TASK-NOTEXIST`)
    expect(r.status).toBe(404)
  })

  test('task assigned notification sent to agent on assignment', async ({ space, api }) => {
    // Create the agent first
    await api.post(
      `/spaces/${space}/agent/NotifyAgent`,
      { status: 'idle', summary: 'NotifyAgent: waiting' },
      'NotifyAgent',
    )
    const t = await api.postJSON<{ id: string }>(
      `/spaces/${space}/tasks`,
      { title: 'Task to assign', assigned_to: 'NotifyAgent' },
      'operator',
    )

    // Check messages for NotifyAgent
    const msgs = await api.getJSON<{ messages: { message: string }[] }>(
      `/spaces/${space}/agent/NotifyAgent/messages`,
    )
    // Should have a task_assigned notification
    expect(msgs.messages.length).toBeGreaterThan(0)
  })
})
