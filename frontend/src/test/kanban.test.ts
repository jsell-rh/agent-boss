import { describe, it, expect } from 'vitest'
import type { Task } from '@/types'

// Pure logic extracted from KanbanView for unit testing

function isTaskOverdue(task: Task, now = new Date()): boolean {
  if (!task.due_at || task.status === 'done') return false
  return new Date(task.due_at) < now
}

function dueSortKey(task: Task): number {
  if (!task.due_at) return Infinity
  return new Date(task.due_at).getTime()
}

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'TASK-1',
    title: 'Test task',
    status: 'backlog',
    space: 'TestSpace',
    created_by: 'boss',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('isTaskOverdue', () => {
  const now = new Date('2026-06-01T12:00:00Z')

  it('returns false when no due_at', () => {
    expect(isTaskOverdue(makeTask(), now)).toBe(false)
  })

  it('returns false when task is done', () => {
    expect(isTaskOverdue(makeTask({ status: 'done', due_at: '2026-01-01T00:00:00Z' }), now)).toBe(false)
  })

  it('returns true when past due and not done', () => {
    expect(isTaskOverdue(makeTask({ due_at: '2026-05-01T00:00:00Z' }), now)).toBe(true)
  })

  it('returns false when due in the future', () => {
    expect(isTaskOverdue(makeTask({ due_at: '2026-07-01T00:00:00Z' }), now)).toBe(false)
  })
})

describe('dueSortKey', () => {
  it('returns Infinity for tasks without due_at', () => {
    expect(dueSortKey(makeTask())).toBe(Infinity)
  })

  it('returns timestamp for tasks with due_at', () => {
    const ts = '2026-03-01T00:00:00Z'
    expect(dueSortKey(makeTask({ due_at: ts }))).toBe(new Date(ts).getTime())
  })

  it('tasks with earlier due dates sort before later ones', () => {
    const earlier = makeTask({ id: 'TASK-1', due_at: '2026-01-01T00:00:00Z' })
    const later = makeTask({ id: 'TASK-2', due_at: '2026-06-01T00:00:00Z' })
    expect(dueSortKey(earlier)).toBeLessThan(dueSortKey(later))
  })
})

describe('task filter logic', () => {
  const tasks = [
    makeTask({ id: 'TASK-1', title: 'Fix bug', assigned_to: 'Alice', labels: ['bug'] }),
    makeTask({ id: 'TASK-2', title: 'Add feature', assigned_to: 'Bob', labels: ['feature'] }),
    makeTask({ id: 'TASK-3', title: 'Write docs', assigned_to: 'Alice', labels: ['docs', 'bug'] }),
  ]

  function filterTasks(
    tasks: Task[],
    { assignee = '', label = '', search = '' } = {},
  ) {
    const q = search.trim().toLowerCase()
    return tasks.filter(t => {
      if (assignee && t.assigned_to !== assignee) return false
      if (label && !t.labels?.includes(label)) return false
      if (q) {
        const titleMatch = t.title.toLowerCase().includes(q)
        const idMatch = t.id.toLowerCase() === q
        if (!titleMatch && !idMatch) return false
      }
      return true
    })
  }

  it('returns all tasks with no filters', () => {
    expect(filterTasks(tasks)).toHaveLength(3)
  })

  it('filters by assignee', () => {
    const result = filterTasks(tasks, { assignee: 'Alice' })
    expect(result.map(t => t.id)).toEqual(['TASK-1', 'TASK-3'])
  })

  it('filters by label', () => {
    const result = filterTasks(tasks, { label: 'bug' })
    expect(result.map(t => t.id)).toEqual(['TASK-1', 'TASK-3'])
  })

  it('filters by search text in title', () => {
    const result = filterTasks(tasks, { search: 'feat' })
    expect(result.map(t => t.id)).toEqual(['TASK-2'])
  })

  it('filters by exact task ID', () => {
    const result = filterTasks(tasks, { search: 'task-2' })
    expect(result.map(t => t.id)).toEqual(['TASK-2'])
  })

  it('combines assignee + label filters', () => {
    // Alice has TASK-1 (bug) and TASK-3 (docs+bug) — both match label:bug
    const result = filterTasks(tasks, { assignee: 'Alice', label: 'bug' })
    expect(result.map(t => t.id)).toEqual(['TASK-1', 'TASK-3'])
  })

  it('combined filters exclude non-matching tasks', () => {
    // Only Alice's tasks with 'feature' label — Alice has none
    const result = filterTasks(tasks, { assignee: 'Alice', label: 'feature' })
    expect(result).toHaveLength(0)
  })
})
