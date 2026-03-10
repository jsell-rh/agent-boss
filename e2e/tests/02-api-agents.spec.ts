/**
 * 02 — API: Agent Management
 *
 * Covers: post status (JSON + text), get agent, update, delete,
 * agent registration, heartbeat, X-Agent-Name enforcement, cross-channel rejection.
 *
 * Note: AgentUpdate response does NOT include a 'name' field (name is the map key).
 * Note: /heartbeat requires prior /register call.
 * Note: /spaces/{name}/ returns map { agentName: AgentUpdate }, not an array.
 */
import { test, expect } from '../fixtures/index.ts'

test.describe('API: Agents', () => {
  test('POST agent status (JSON) returns 202 accepted', async ({ space, api }) => {
    const r = await api.post(
      `/spaces/${space}/agent/Rover`,
      {
        status: 'active',
        summary: 'Rover: running tests',
        branch: 'feat/tests',
      },
      'Rover',
    )
    expect(r.status).toBe(202)
    const body = await r.text()
    expect(body).toContain('accepted')
  })

  test('POST agent status (text/plain) is accepted', async ({ space, api }) => {
    const r = await api.postText(
      `/spaces/${space}/agent/TextBot`,
      '# TextBot\n**status:** active\n\nDoing things.',
      'TextBot',
    )
    expect([200, 202]).toContain(r.status)
  })

  test('GET /spaces/{space}/agent/{name} returns agent data', async ({ space, api }) => {
    // Create the agent first
    await api.post(
      `/spaces/${space}/agent/Alpha`,
      { status: 'active', summary: 'Alpha: hello', branch: 'main' },
      'Alpha',
    )
    const data = await api.getJSON<{
      status: string
      summary: string
      branch: string
    }>(`/spaces/${space}/agent/Alpha`)
    // AgentUpdate does not include a 'name' field — it's the map key
    expect(data.status).toBe('active')
    expect(data.summary).toContain('Alpha')
    expect(data.branch).toBe('main')
  })

  test('agent appears in space agents map after posting', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/Listed`,
      { status: 'active', summary: 'Listed: visible' },
      'Listed',
    )
    // agents is a map: { [agentName: string]: AgentUpdate }
    const spaceData = await api.getJSON<{ agents: Record<string, { status: string }> }>(`/spaces/${space}/`)
    expect(spaceData.agents['Listed']).toBeDefined()
  })

  test('cross-channel POST (wrong X-Agent-Name) is rejected with 403', async ({ space, api }) => {
    // Create the agent first
    await api.post(
      `/spaces/${space}/agent/Owner`,
      { status: 'active', summary: 'Owner: live' },
      'Owner',
    )
    // Attempt to post to Owner's channel as Intruder
    const r = await api.post(
      `/spaces/${space}/agent/Owner`,
      { status: 'active', summary: 'Intruder: hijacking' },
      'Intruder',  // wrong name — should be Owner
    )
    expect(r.status).toBe(403)
  })

  test('POST without X-Agent-Name header is rejected', async ({ space }) => {
    // Post without agentName param (no X-Agent-Name header)
    const r = await fetch(`http://localhost:18899/spaces/${space}/agent/NoHeader`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status: 'active', summary: 'NoHeader: missing header' }),
    })
    // Should fail: 400 or 403
    expect([400, 403]).toContain(r.status)
  })

  test('DELETE agent removes it from space', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ToDelete`,
      { status: 'active', summary: 'ToDelete: temporary' },
      'ToDelete',
    )
    const r = await api.del(`/spaces/${space}/agent/ToDelete`, 'ToDelete')
    expect([200, 204]).toContain(r.status)

    const spaceData = await api.getJSON<{ agents: Record<string, unknown> }>(`/spaces/${space}/`)
    expect(spaceData.agents['ToDelete']).toBeUndefined()
  })

  test('agent status fields are persisted and retrievable', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/Detailed`,
      {
        status: 'active',
        summary: 'Detailed: full fields',
        branch: 'feat/detailed',
        pr: '#42',
        repo_url: 'https://github.com/test/repo',
        phase: 'coding',
        test_count: 99,
        items: ['did A', 'did B'],
        next_steps: 'do C',
      },
      'Detailed',
    )
    const data = await api.getJSON<{
      status: string
      branch: string
      pr: string
      phase: string
      test_count: number
      items: string[]
    }>(`/spaces/${space}/agent/Detailed`)
    expect(data.status).toBe('active')
    expect(data.branch).toBe('feat/detailed')
    expect(data.pr).toBe('#42')
    expect(data.phase).toBe('coding')
    expect(data.test_count).toBe(99)
    expect(data.items).toContain('did A')
  })

  test('agent register endpoint stores capabilities', async ({ space, api }) => {
    // First create the agent
    await api.post(
      `/spaces/${space}/agent/RegBot`,
      { status: 'idle', summary: 'RegBot: registering' },
      'RegBot',
    )
    const r = await api.post(
      `/spaces/${space}/agent/RegBot/register`,
      {
        agent_type: 'http',
        capabilities: ['code', 'review'],
        heartbeat_interval_sec: 30,
      },
      'RegBot',
    )
    expect(r.status).toBe(200)
    const body = await r.json() as { status: string; agent_type: string }
    expect(body.status).toBe('registered')
    expect(body.agent_type).toBe('http')
  })

  test('agent heartbeat endpoint returns ok (requires prior registration)', async ({ space, api }) => {
    // Create agent
    await api.post(
      `/spaces/${space}/agent/HeartBot`,
      { status: 'active', summary: 'HeartBot: alive' },
      'HeartBot',
    )
    // Register as http agent first — heartbeat requires registration
    await api.post(
      `/spaces/${space}/agent/HeartBot/register`,
      { agent_type: 'http', heartbeat_interval_sec: 30 },
      'HeartBot',
    )
    // Now heartbeat should work
    const r = await api.post(
      `/spaces/${space}/agent/HeartBot/heartbeat`,
      {},
      'HeartBot',
    )
    expect(r.status).toBe(200)
  })

  test('agent status done is reflected in space data', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/Finisher`,
      { status: 'active', summary: 'Finisher: working' },
      'Finisher',
    )
    await api.post(
      `/spaces/${space}/agent/Finisher`,
      { status: 'done', summary: 'Finisher: all done' },
      'Finisher',
    )
    const data = await api.getJSON<{ status: string }>(`/spaces/${space}/agent/Finisher`)
    expect(data.status).toBe('done')
  })

  test('multiple agents can coexist in one space', async ({ space, api }) => {
    const agents = ['AgentA', 'AgentB', 'AgentC']
    for (const name of agents) {
      await api.post(
        `/spaces/${space}/agent/${name}`,
        { status: 'active', summary: `${name}: coexisting` },
        name,
      )
    }
    const spaceData = await api.getJSON<{ agents: Record<string, unknown> }>(`/spaces/${space}/`)
    for (const name of agents) {
      expect(spaceData.agents[name]).toBeDefined()
    }
  })

  test('agent history endpoint returns array of snapshots', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/HistoryBot`,
      { status: 'active', summary: 'HistoryBot: v1' },
      'HistoryBot',
    )
    await api.post(
      `/spaces/${space}/agent/HistoryBot`,
      { status: 'active', summary: 'HistoryBot: v2' },
      'HistoryBot',
    )
    const history = await api.getJSON<unknown[]>(`/spaces/${space}/agent/HistoryBot/history`)
    expect(Array.isArray(history)).toBe(true)
    expect(history.length).toBeGreaterThanOrEqual(1)
  })

  test('agent questions array triggers attention count', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/QuestionBot`,
      {
        status: 'blocked',
        summary: 'QuestionBot: needs decision',
        questions: ['[?BOSS] Should we proceed?'],
      },
      'QuestionBot',
    )
    const spaces = await api.getJSON<{ name: string; attention_count: number }[]>('/spaces')
    const s = spaces.find(x => x.name === space)
    expect(s).toBeDefined()
    expect(s!.attention_count).toBeGreaterThan(0)
  })

  test('agent blockers array is stored', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/BlockedBot`,
      {
        status: 'blocked',
        summary: 'BlockedBot: stuck',
        blockers: ['Waiting for DB migration'],
      },
      'BlockedBot',
    )
    const data = await api.getJSON<{ blockers: string[] }>(`/spaces/${space}/agent/BlockedBot`)
    expect(data.blockers).toContain('Waiting for DB migration')
  })
})
