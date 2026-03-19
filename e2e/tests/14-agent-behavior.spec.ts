/**
 * 14 — Agent Behavior: Full Lifecycle Tests
 *
 * Covers the complete autonomous agent workflow:
 * - Ignition → status posting → message exchange → cursor pagination
 * - Questions and blockers triggering attention
 * - Hierarchy registration via ignition
 * - Agent-to-agent messaging
 * - Agent status progression (idle → active → done)
 * - Concurrent agents in same space
 * - Agent reconnection after restart (session stickiness)
 */
import { test, expect } from '../fixtures/index.ts'

test.describe('Agent Behavior: Full Lifecycle', () => {
  test('complete agent lifecycle: register → post → message → done', async ({ space, api }) => {
    // Step 1: Post initial idle status
    let r = await api.post(
      `/spaces/${space}/agent/LifecycleBot`,
      { status: 'idle', summary: 'LifecycleBot: pending assignment' },
      'LifecycleBot',
    )
    expect(r.status).toBe(202)

    // Step 2: Register with capabilities
    const regR = await api.post(
      `/spaces/${space}/agent/LifecycleBot/register`,
      { agent_type: 'http', capabilities: ['code', 'test', 'review'], heartbeat_interval_sec: 60 },
      'LifecycleBot',
    )
    expect(regR.status).toBe(200)
    const reg = await regR.json() as { status: string; agent_type: string }
    expect(reg.status).toBe('registered')

    // Step 3: Ignite (registers session)
    const ignR = await api.get(
      `/spaces/${space}/ignition/LifecycleBot?session_id=lifecycle-session-1`,
    )
    expect(ignR.status).toBe(200)
    const ignText = await ignR.text()
    expect(ignText).toContain('LifecycleBot')

    // Step 4: Boss sends task assignment message
    const msgR = await api.post(
      `/spaces/${space}/agent/LifecycleBot/message`,
      { message: 'LifecycleBot: your task is TASK-999: Write tests', priority: 'directive' },
      'operator',
    )
    const msgBody = await msgR.json() as { status: string; messageId: string }
    expect(msgBody.status).toBe('delivered')
    expect(msgBody.messageId).toBeTruthy()

    // Step 5: Agent checks messages with cursor
    const msgs1 = await api.getJSON<{ cursor: string; messages: { message: string; priority: string }[] }>(
      `/spaces/${space}/agent/LifecycleBot/messages`,
    )
    expect(msgs1.messages.length).toBeGreaterThanOrEqual(1)
    expect(msgs1.messages.some(m => m.priority === 'directive')).toBe(true)
    const cursor1 = msgs1.cursor

    // Step 6: Agent posts active status (working on task)
    r = await api.post(
      `/spaces/${space}/agent/LifecycleBot`,
      {
        status: 'active',
        summary: 'LifecycleBot: working on TASK-999 — writing tests',
        branch: 'feat/tests',
        phase: 'implementation',
        test_count: 0,
        items: ['ACK task directive', 'started test suite'],
        next_steps: 'complete all test files',
      },
      'LifecycleBot',
    )
    expect(r.status).toBe(202)

    // Step 7: Agent sends a question
    r = await api.post(
      `/spaces/${space}/agent/LifecycleBot`,
      {
        status: 'active',
        summary: 'LifecycleBot: paused — has question',
        questions: ['[?BOSS] Should I include performance tests?'],
      },
      'LifecycleBot',
    )
    expect(r.status).toBe(202)

    // Verify attention count increased
    const spaces = await api.getJSON<{ name: string; attention_count: number }[]>('/spaces')
    const s = spaces.find(x => x.name === space)
    expect(s!.attention_count).toBeGreaterThan(0)

    // Step 8: Boss answers question
    await api.post(
      `/spaces/${space}/agent/LifecycleBot/message`,
      { message: 'Yes, include performance tests too.' },
      'operator',
    )

    // Step 9: Agent fetches only new messages via cursor
    const msgs2 = await api.getJSON<{ messages: { message: string }[] }>(
      `/spaces/${space}/agent/LifecycleBot/messages?since=${encodeURIComponent(cursor1)}`,
    )
    expect(msgs2.messages.length).toBe(1)
    expect(msgs2.messages[0].message).toContain('performance tests')

    // Step 10: Agent posts done status
    r = await api.post(
      `/spaces/${space}/agent/LifecycleBot`,
      {
        status: 'done',
        summary: 'LifecycleBot: TASK-999 complete — 42 tests passing',
        test_count: 42,
        items: ['E2E test suite complete', '42 tests passing'],
      },
      'LifecycleBot',
    )
    expect(r.status).toBe(202)

    // Verify final state
    const final = await api.getJSON<{ status: string; test_count: number }>(
      `/spaces/${space}/agent/LifecycleBot`,
    )
    expect(final.status).toBe('done')
    expect(final.test_count).toBe(42)
  })

  test('agent-to-agent messaging', async ({ space, api }) => {
    // Create two agents
    await api.post(
      `/spaces/${space}/agent/SenderAgent`,
      { status: 'active', summary: 'SenderAgent: will send messages' },
      'SenderAgent',
    )
    await api.post(
      `/spaces/${space}/agent/ReceiverAgent`,
      { status: 'active', summary: 'ReceiverAgent: awaiting messages' },
      'ReceiverAgent',
    )

    // SenderAgent sends message to ReceiverAgent
    const r = await api.post(
      `/spaces/${space}/agent/ReceiverAgent/message`,
      { message: 'SenderAgent → ReceiverAgent: coordination message' },
      'SenderAgent',
    )
    expect(r.status).toBe(200)

    // ReceiverAgent reads messages
    const msgs = await api.getJSON<{ messages: { message: string; sender: string }[] }>(
      `/spaces/${space}/agent/ReceiverAgent/messages`,
    )
    const found = msgs.messages.find(m => m.sender === 'SenderAgent')
    expect(found).toBeDefined()
    expect(found!.message).toContain('coordination message')
  })

  test('hierarchy: manager spawning child agents', async ({ space, api }) => {
    // Manager posts status
    await api.post(
      `/spaces/${space}/agent/OrchestratorMgr`,
      { status: 'active', summary: 'OrchestratorMgr: spinning up team' },
      'OrchestratorMgr',
    )

    // Child agents register with parent
    for (const child of ['OrchestratorDev1', 'OrchestratorDev2', 'OrchestratorSME']) {
      const ignR = await api.get(
        `/spaces/${space}/ignition/${child}?session_id=${child}-session&parent=OrchestratorMgr&role=Developer`,
      )
      expect(ignR.status).toBe(200)

      await api.post(
        `/spaces/${space}/agent/${child}`,
        {
          status: 'active',
          summary: `${child}: assigned and working`,
          parent: 'OrchestratorMgr',
        },
        child,
      )
    }

    // Manager sends directives to each child
    for (const child of ['OrchestratorDev1', 'OrchestratorDev2']) {
      const r = await api.post(
        `/spaces/${space}/agent/${child}/message`,
        { message: `${child}: implement feature module ${child}` },
        'OrchestratorMgr',
      )
      expect(r.status).toBe(200)
    }

    // Verify hierarchy - response has roots+nodes, not agents array
    const hierarchy = await api.getJSON<{ roots: string[]; nodes: Record<string, unknown> }>(`/spaces/${space}/hierarchy`)
    expect(Array.isArray(hierarchy.roots)).toBe(true)

    // All agents visible in space — agents is a map, not array
    const spaceData = await api.getJSON<{ agents: Record<string, unknown> }>(`/spaces/${space}/`)
    expect(spaceData.agents['OrchestratorMgr']).toBeDefined()
    expect(spaceData.agents['OrchestratorDev1']).toBeDefined()
  })

  test('agent heartbeat keeps liveness without status post', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/HeartbeatBot`,
      { status: 'active', summary: 'HeartbeatBot: using heartbeat' },
      'HeartbeatBot',
    )
    await api.post(
      `/spaces/${space}/agent/HeartbeatBot/register`,
      { agent_type: 'http', heartbeat_interval_sec: 5 },
      'HeartbeatBot',
    )

    // Send multiple heartbeats
    for (let i = 0; i < 3; i++) {
      const r = await api.post(`/spaces/${space}/agent/HeartbeatBot/heartbeat`, {}, 'HeartbeatBot')
      expect(r.status).toBe(200)
    }

    // Agent should still be active
    const data = await api.getJSON<{ status: string }>(`/spaces/${space}/agent/HeartbeatBot`)
    expect(data.status).toBe('active')
  })

  test('concurrent agents do not interfere with each other', async ({ space, api }) => {
    // Simulate multiple agents posting concurrently
    const agents = Array.from({ length: 5 }, (_, i) => `ConcurrentBot${i}`)

    await Promise.all(agents.map(name =>
      api.post(
        `/spaces/${space}/agent/${name}`,
        { status: 'active', summary: `${name}: concurrent operation`, test_count: Math.floor(Math.random() * 100) },
        name,
      )
    ))

    // All agents should be present with correct data
    await Promise.all(agents.map(async name => {
      const data = await api.getJSON<{ status: string }>(`/spaces/${space}/agent/${name}`)
      // AgentUpdate has no 'name' field — verify status only
      expect(data.status).toBe('active')
    }))
  })

  test('agent session stickiness: repo_url and tmux_session sent once', async ({ space, api }) => {
    // First post includes session info
    await api.post(
      `/spaces/${space}/agent/StickyBot`,
      {
        status: 'active',
        summary: 'StickyBot: sticky fields test',
        repo_url: 'https://github.com/test/sticky',
        tmux_session: 'sticky-session',
        branch: 'main',
      },
      'StickyBot',
    )

    // Second post does NOT include session fields — server should retain them
    await api.post(
      `/spaces/${space}/agent/StickyBot`,
      {
        status: 'active',
        summary: 'StickyBot: updated without sticky fields',
      },
      'StickyBot',
    )

    const data = await api.getJSON<{ repo_url?: string; tmux_session?: string }>(
      `/spaces/${space}/agent/StickyBot`,
    )
    // repo_url should still be set from first post
    expect(data.repo_url).toBe('https://github.com/test/sticky')
  })

  test('agent error status is handled correctly', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ErrorBot`,
      {
        status: 'error',
        summary: 'ErrorBot: encountered fatal error',
        blockers: ['Panic in main goroutine'],
      },
      'ErrorBot',
    )

    const data = await api.getJSON<{ status: string; blockers: string[] }>(
      `/spaces/${space}/agent/ErrorBot`,
    )
    expect(data.status).toBe('error')
    expect(data.blockers).toContain('Panic in main goroutine')
  })

  test('bulk agent updates: manager reports after team completes', async ({ space, api }) => {
    const SPACE = space
    const team = ['TeamDev1', 'TeamDev2', 'TeamDev3']
    const mgr = 'TeamMgr'

    // Setup team
    await api.post(
      `/spaces/${SPACE}/agent/${mgr}`,
      { status: 'active', summary: `${mgr}: monitoring team` },
      mgr,
    )
    for (const dev of team) {
      await api.post(
        `/spaces/${SPACE}/agent/${dev}`,
        { status: 'active', summary: `${dev}: working`, parent: mgr },
        dev,
      )
    }

    // Team completes work
    let completedCount = 0
    for (const dev of team) {
      await api.post(
        `/spaces/${SPACE}/agent/${dev}`,
        { status: 'done', summary: `${dev}: done` },
        dev,
      )
      // Notify manager
      await api.post(
        `/spaces/${SPACE}/agent/${mgr}/message`,
        { message: `${dev}: work complete` },
        dev,
      )
      completedCount++
    }

    // Manager checks messages
    const msgs = await api.getJSON<{ messages: { message: string }[] }>(
      `/spaces/${SPACE}/agent/${mgr}/messages`,
    )
    expect(msgs.messages.length).toBe(team.length)

    // Manager posts final report
    await api.post(
      `/spaces/${SPACE}/agent/${mgr}`,
      {
        status: 'done',
        summary: `${mgr}: team complete — ${completedCount} developers done`,
        items: team.map(d => `${d}: ✓`),
      },
      mgr,
    )

    const mgrData = await api.getJSON<{ status: string }>(`/spaces/${SPACE}/agent/${mgr}`)
    expect(mgrData.status).toBe('done')
  })
})
