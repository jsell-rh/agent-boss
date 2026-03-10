/**
 * 04 — API: Messaging System
 *
 * Covers: send message, list messages, cursor pagination, message ack,
 * priority levels, sender attribution.
 */
import { test, expect } from '../fixtures/index.ts'

test.describe('API: Messages', () => {
  test('send message delivers to agent', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/MsgBot`,
      { status: 'active', summary: 'MsgBot: listening' },
      'MsgBot',
    )
    const r = await api.post(
      `/spaces/${space}/agent/MsgBot/message`,
      { message: 'Hello from test!' },
      'Sender',
    )
    expect(r.status).toBe(200)
    const body = await r.json() as { status: string; recipient: string; messageId: string }
    expect(body.status).toBe('delivered')
    expect(body.recipient).toBe('MsgBot')
    expect(body.messageId).toBeTruthy()
  })

  test('GET /messages returns messages array with cursor', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/CursorBot`,
      { status: 'active', summary: 'CursorBot: ready' },
      'CursorBot',
    )
    await api.post(
      `/spaces/${space}/agent/CursorBot/message`,
      { message: 'Message 1' },
      'Boss',
    )
    const result = await api.getJSON<{
      agent: string
      cursor: string
      messages: { id: string; message: string; sender: string }[]
    }>(`/spaces/${space}/agent/CursorBot/messages`)
    expect(result.agent).toBe('CursorBot')
    expect(result.cursor).toBeTruthy()
    expect(result.messages.length).toBeGreaterThanOrEqual(1)
    expect(result.messages[0].message).toBeTruthy()
    expect(result.messages[0].sender).toBeTruthy()
  })

  test('cursor-based pagination only returns new messages', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/PaginBot`,
      { status: 'active', summary: 'PaginBot: ready' },
      'PaginBot',
    )

    // Send first message
    await api.post(`/spaces/${space}/agent/PaginBot/message`, { message: 'First' }, 'Boss')

    // Get cursor after first message
    const first = await api.getJSON<{ cursor: string; messages: unknown[] }>(
      `/spaces/${space}/agent/PaginBot/messages`,
    )
    expect(first.messages.length).toBeGreaterThanOrEqual(1)
    const cursor = first.cursor

    // Send second message
    await api.post(`/spaces/${space}/agent/PaginBot/message`, { message: 'Second' }, 'Boss')

    // Fetch with cursor — should only return Second
    const second = await api.getJSON<{ messages: { message: string }[] }>(
      `/spaces/${space}/agent/PaginBot/messages?since=${encodeURIComponent(cursor)}`,
    )
    expect(second.messages.length).toBe(1)
    expect(second.messages[0].message).toBe('Second')
  })

  test('message with priority field is preserved', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/PrioBot`,
      { status: 'active', summary: 'PrioBot: ready' },
      'PrioBot',
    )
    await api.post(
      `/spaces/${space}/agent/PrioBot/message`,
      { message: 'Urgent!', priority: 'directive' },
      'Boss',
    )
    const msgs = await api.getJSON<{ messages: { priority: string }[] }>(
      `/spaces/${space}/agent/PrioBot/messages`,
    )
    expect(msgs.messages.some(m => m.priority === 'directive')).toBe(true)
  })

  test('message ack endpoint works', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/AckBot`,
      { status: 'active', summary: 'AckBot: ready' },
      'AckBot',
    )
    const sentResp = await api.post(
      `/spaces/${space}/agent/AckBot/message`,
      { message: 'Ack this' },
      'Boss',
    )
    const sent = await sentResp.json() as { messageId: string }
    const msgId = sent.messageId

    // Ack the message
    const ackR = await fetch(
      `http://localhost:18899/spaces/${space}/agent/AckBot/message/${msgId}/ack`,
      { method: 'POST', headers: { 'X-Agent-Name': 'AckBot' } },
    )
    expect([200, 204]).toContain(ackR.status)
  })

  test('multiple messages from different senders are stored', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/MultiBot`,
      { status: 'active', summary: 'MultiBot: ready' },
      'MultiBot',
    )
    await api.post(`/spaces/${space}/agent/MultiBot/message`, { message: 'From Alpha' }, 'Alpha')
    await api.post(`/spaces/${space}/agent/MultiBot/message`, { message: 'From Beta' }, 'Beta')
    await api.post(`/spaces/${space}/agent/MultiBot/message`, { message: 'From Gamma' }, 'Gamma')

    const msgs = await api.getJSON<{ messages: { sender: string }[] }>(
      `/spaces/${space}/agent/MultiBot/messages`,
    )
    expect(msgs.messages.length).toBeGreaterThanOrEqual(3)
    const senders = msgs.messages.map(m => m.sender)
    expect(senders).toContain('Alpha')
    expect(senders).toContain('Beta')
    expect(senders).toContain('Gamma')
  })

  test('empty messages returns empty array with cursor', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/EmptyBot`,
      { status: 'active', summary: 'EmptyBot: no messages' },
      'EmptyBot',
    )
    const result = await api.getJSON<{ messages: unknown[]; cursor: string }>(
      `/spaces/${space}/agent/EmptyBot/messages`,
    )
    expect(Array.isArray(result.messages)).toBe(true)
    expect(result.cursor).toBeTruthy() // cursor exists even when empty
  })
})
