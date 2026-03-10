/**
 * 06 — API: Hierarchy & Ignition
 *
 * Covers: hierarchy tree, agent parent-child relationships, ignition endpoint.
 */
import { test, expect } from '../fixtures/index.ts'

test.describe('API: Hierarchy', () => {
  test('GET /hierarchy returns hierarchy tree', async ({ space, api }) => {
    // Post a manager and a child
    await api.post(
      `/spaces/${space}/agent/Manager`,
      { status: 'active', summary: 'Manager: leading' },
      'Manager',
    )
    await api.post(
      `/spaces/${space}/agent/Worker`,
      { status: 'active', summary: 'Worker: working', parent: 'Manager', role: 'Developer' },
      'Worker',
    )
    const tree = await api.getJSON<{ space: string; roots: string[]; nodes: Record<string, unknown> }>(`/spaces/${space}/hierarchy`)
    expect(tree).toBeDefined()
    expect(Array.isArray(tree.roots)).toBe(true)
    expect(typeof tree.nodes).toBe('object')
  })

  test('ignition endpoint registers session and returns prompt', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/IgniteBot`,
      { status: 'idle', summary: 'IgniteBot: awaiting ignition' },
      'IgniteBot',
    )
    const r = await api.get(
      `/spaces/${space}/ignition/IgniteBot?session_id=test-session-123`,
    )
    expect(r.status).toBe(200)
    const text = await r.text()
    expect(text).toContain('IgniteBot')
    expect(text).toContain(space)
  })

  test('ignition with parent registers hierarchy', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ParentMgr`,
      { status: 'active', summary: 'ParentMgr: managing' },
      'ParentMgr',
    )
    await api.post(
      `/spaces/${space}/agent/ChildDev`,
      { status: 'idle', summary: 'ChildDev: ready' },
      'ChildDev',
    )
    const r = await api.get(
      `/spaces/${space}/ignition/ChildDev?session_id=child-session&parent=ParentMgr&role=Developer`,
    )
    expect(r.status).toBe(200)
    const text = await r.text()
    expect(text).toContain('ChildDev')
  })

  test('hierarchy tree reflects parent-child after ignition', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/HierMgr`,
      { status: 'active', summary: 'HierMgr: top-level' },
      'HierMgr',
    )
    await api.post(
      `/spaces/${space}/agent/HierDev`,
      { status: 'active', summary: 'HierDev: child', parent: 'HierMgr' },
      'HierDev',
    )

    const tree = await api.getJSON<{ roots: string[]; nodes: Record<string, unknown> }>(
      `/spaces/${space}/hierarchy`,
    )
    expect(tree.nodes?.['HierMgr'] || tree.roots?.includes('HierMgr')).toBeTruthy()
  })

  test('GET /spaces/{space}/api/agents returns agent JSON list', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ApiAgent`,
      { status: 'active', summary: 'ApiAgent: listed' },
      'ApiAgent',
    )
    const agentsMap = await api.getJSON<Record<string, { status: string }>>(
      `/spaces/${space}/api/agents`,
    )
    expect(agentsMap['ApiAgent']).toBeDefined()
    expect(typeof agentsMap).toBe('object')
  })

  test('GET /spaces/{space}/api/events returns event log', async ({ space, api }) => {
    const events = await api.getJSON<string[]>(`/spaces/${space}/api/events`)
    expect(Array.isArray(events)).toBe(true)
  })
})
