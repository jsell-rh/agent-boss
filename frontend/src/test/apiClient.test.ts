import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { api } from '@/api/client'

describe('ApiClient', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function mockFetch(data: unknown, status = 200) {
    const mockFn = vi.mocked(fetch)
    mockFn.mockResolvedValueOnce({
      ok: status >= 200 && status < 300,
      status,
      statusText: status === 200 ? 'OK' : 'Error',
      json: async () => data,
      text: async () => JSON.stringify(data),
    } as Response)
  }

  it('fetchSpaces calls /spaces and returns array', async () => {
    const spaces = [{ name: 'TestSpace', agent_count: 2, attention_count: 0, created_at: '', updated_at: '' }]
    mockFetch(spaces)
    const result = await api.fetchSpaces()
    expect(result).toEqual(spaces)
    expect(fetch).toHaveBeenCalledWith('/spaces', undefined)
  })

  it('fetchSpace calls /spaces/{space}/ with json accept header', async () => {
    const space = { name: 'MySpace', agents: {}, created_at: '', updated_at: '' }
    mockFetch(space)
    await api.fetchSpace('MySpace')
    expect(fetch).toHaveBeenCalledWith(
      '/spaces/MySpace/',
      expect.objectContaining({ headers: { Accept: 'application/json' } }),
    )
  })

  it('fetchSpace URL-encodes space names with spaces', async () => {
    mockFetch({ name: 'My Space', agents: {}, created_at: '', updated_at: '' })
    await api.fetchSpace('My Space')
    expect(fetch).toHaveBeenCalledWith(
      '/spaces/My%20Space/',
      expect.anything(),
    )
  })

  it('deleteSpace sends DELETE to /spaces/{space}/', async () => {
    mockFetch('ok')
    await api.deleteSpace('TestSpace')
    expect(fetch).toHaveBeenCalledWith(
      '/spaces/TestSpace/',
      expect.objectContaining({ method: 'DELETE' }),
    )
  })

  it('fetchTasks returns tasks array from response', async () => {
    const tasks = [{ id: 'TASK-1', title: 'Do something', status: 'backlog', space: 'S', created_by: 'boss', created_at: '', updated_at: '' }]
    mockFetch({ tasks, total: 1 })
    const result = await api.fetchTasks('TestSpace')
    expect(result).toEqual(tasks)
  })

  it('fetchTasks passes status filter in query string', async () => {
    mockFetch({ tasks: [], total: 0 })
    await api.fetchTasks('TestSpace', { status: 'in_progress' })
    expect(fetch).toHaveBeenCalledWith(
      '/spaces/TestSpace/tasks?status=in_progress',
      undefined,
    )
  })

  it('moveTask sends POST to /tasks/{id}/move with status', async () => {
    const task = { id: 'TASK-1', title: 'T', status: 'done', space: 'S', created_by: 'boss', created_at: '', updated_at: '' }
    mockFetch(task)
    await api.moveTask('TestSpace', 'TASK-1', 'done')
    expect(fetch).toHaveBeenCalledWith(
      '/spaces/TestSpace/tasks/TASK-1/move',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ status: 'done' }),
      }),
    )
  })

  it('throws on non-2xx response', async () => {
    const mockFn = vi.mocked(fetch)
    mockFn.mockResolvedValueOnce({
      ok: false,
      status: 404,
      statusText: 'Not Found',
      text: async () => 'not found',
    } as Response)
    await expect(api.fetchSpace('ghost')).rejects.toThrow('404')
  })

  it('archiveSpace sends POST to /spaces/{space}/archive with timestamp body', async () => {
    mockFetch('ok')
    await api.archiveSpace('TestSpace')
    const call = vi.mocked(fetch).mock.calls[0]
    expect(call[0]).toBe('/spaces/TestSpace/archive')
    expect((call[1] as RequestInit).method).toBe('POST')
    const body = (call[1] as RequestInit).body as string
    expect(body).toMatch(/^Archived at \d{4}-/)
  })
})
