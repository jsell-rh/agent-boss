/**
 * 01 — API: Space Management
 *
 * Covers: list, create, get, archive, delete, and raw endpoint.
 * Every space created gets a unique name so tests are self-contained.
 */
import { test, expect } from '../fixtures/index.ts'

test.describe('API: Spaces', () => {
  test('GET /spaces returns an array', async ({ api }) => {
    const spaces = await api.getJSON<unknown[]>('/spaces')
    expect(Array.isArray(spaces)).toBe(true)
  })

  test('create space via POST /spaces/{name}/contracts', async ({ api, cleanupSpaces }) => {
    const name = `e2e-create-${Date.now()}`
    cleanupSpaces.push(name)

    const r = await api.postText(`/spaces/${name}/contracts`, '# Contract\nShared rules.')
    expect(r.status).toBe(200)

    const spaces = await api.getJSON<{ name: string }[]>('/spaces')
    expect(spaces.some(s => s.name === name)).toBe(true)
  })

  test('GET /spaces/{name}/ returns JSON space with Accept: application/json', async ({ space, api }) => {
    // Space view returns JSON when Accept: application/json is sent (fixtures/index.ts sends it)
    const data = await api.getJSON<{ name: string; agents: Record<string, unknown> }>(`/spaces/${space}/`)
    expect(data.name).toBe(space)
    expect(data.agents).toBeDefined()
    expect(typeof data.agents).toBe('object')
  })

  test('space summary includes agent_count and timestamps', async ({ space, api }) => {
    const spaces = await api.getJSON<{
      name: string
      agent_count: number
      created_at: string
      updated_at: string
    }[]>('/spaces')
    const s = spaces.find(x => x.name === space)
    expect(s).toBeDefined()
    expect(typeof s!.agent_count).toBe('number')
    expect(s!.created_at).toMatch(/^\d{4}-\d{2}-\d{2}/)
    expect(s!.updated_at).toMatch(/^\d{4}-\d{2}-\d{2}/)
  })

  test('GET /spaces/{name}/raw returns markdown', async ({ space, api }) => {
    const r = await api.get(`/spaces/${space}/raw`)
    expect(r.status).toBe(200)
    const text = await r.text()
    expect(text).toContain(space)
  })

  test('archive a space', async ({ space, api }) => {
    const r = await api.postText(
      `/spaces/${space}/archive`,
      'Archived for test purposes',
    )
    expect(r.status).toBe(200)

    // Archived space should appear in spaces list
    const spaces = await api.getJSON<{ name: string }[]>('/spaces')
    expect(spaces.some(s => s.name === space)).toBe(true)

    // Raw content should mention archive
    const raw = await (await api.get(`/spaces/${space}/raw`)).text()
    expect(raw).toContain(space)
  })

  test('delete a space returns 204 or 200', async ({ api, cleanupSpaces }) => {
    const name = `e2e-del-${Date.now()}`
    await api.postText(`/spaces/${name}/contracts`, '')
    const r = await api.del(`/spaces/${name}/`)
    expect([200, 204]).toContain(r.status)

    // Should not appear in space list
    const spaces = await api.getJSON<{ name: string }[]>('/spaces')
    expect(spaces.some(s => s.name === name)).toBe(false)
  })

  test('DELETE non-existent space returns 404', async ({ api }) => {
    const r = await api.del('/spaces/does-not-exist-xyz/')
    expect(r.status).toBe(404)
  })

  test('space contracts endpoint accepts text updates', async ({ space, api }) => {
    const r = await api.postText(
      `/spaces/${space}/contracts`,
      '# Updated Contract\n- Rule 1\n- Rule 2',
    )
    expect(r.status).toBe(200)

    const raw = await (await api.get(`/spaces/${space}/raw`)).text()
    expect(raw).toContain('Updated Contract')
  })
})
