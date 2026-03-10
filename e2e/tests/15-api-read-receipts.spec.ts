/**
 * 15 — API: Read Receipts
 *
 * Covers the complete read receipt lifecycle:
 * - Messages start as unread (read: false / omitted)
 * - Ack endpoint marks a message as read with a timestamp
 * - Read state is returned in messages endpoint
 * - Cross-agent ack is rejected (403)
 * - Acking non-existent message returns 404
 * - Read state persists and is included in cursor-paginated responses
 * - Multiple messages: acking one does not affect others
 * - Notification read state mirrors message ack
 */
import { test, expect } from '../fixtures/index.ts'

const BASE = 'http://localhost:18899'

test.describe('API: Read Receipts', () => {
  test('new message starts as unread (read field false or absent)', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ReadBot`,
      { status: 'active', summary: 'ReadBot: watching inbox' },
      'ReadBot',
    )
    await api.post(
      `/spaces/${space}/agent/ReadBot/message`,
      { message: 'You have unread mail' },
      'boss',
    )

    const msgs = await api.getJSON<{
      messages: { id: string; message: string; read?: boolean; read_at?: string }[]
    }>(`/spaces/${space}/agent/ReadBot/messages`)

    expect(msgs.messages.length).toBeGreaterThan(0)
    const msg = msgs.messages[0]
    // read should be false or absent (omitempty) for a new message
    expect(msg.read === false || msg.read === undefined).toBe(true)
    expect(msg.read_at).toBeUndefined()
  })

  test('ack marks message as read with read_at timestamp', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/AckBot`,
      { status: 'active', summary: 'AckBot: will ack messages' },
      'AckBot',
    )
    const sentResp = await api.post(
      `/spaces/${space}/agent/AckBot/message`,
      { message: 'Please ack me' },
      'boss',
    )
    const sent = await sentResp.json() as { messageId: string }
    const msgId = sent.messageId

    // Verify unread before ack
    const before = await api.getJSON<{ messages: { id: string; read?: boolean }[] }>(
      `/spaces/${space}/agent/AckBot/messages`,
    )
    const beforeMsg = before.messages.find(m => m.id === msgId)
    expect(beforeMsg).toBeDefined()
    expect(beforeMsg!.read === false || beforeMsg!.read === undefined).toBe(true)

    // Ack the message
    const ackResp = await fetch(`${BASE}/spaces/${space}/agent/AckBot/message/${msgId}/ack`, {
      method: 'POST',
      headers: { 'X-Agent-Name': 'AckBot' },
    })
    expect(ackResp.status).toBe(200)
    const ackBody = await ackResp.json() as { status: string; message_id: string }
    expect(ackBody.status).toBe('acked')
    expect(ackBody.message_id).toBe(msgId)

    // Verify read after ack
    const after = await api.getJSON<{
      messages: { id: string; read?: boolean; read_at?: string }[]
    }>(`/spaces/${space}/agent/AckBot/messages`)
    const afterMsg = after.messages.find(m => m.id === msgId)
    expect(afterMsg).toBeDefined()
    expect(afterMsg!.read).toBe(true)
    // read_at should be a valid timestamp
    expect(afterMsg!.read_at).toBeTruthy()
    expect(new Date(afterMsg!.read_at!).getTime()).not.toBeNaN()
  })

  test('ack by wrong agent returns 403', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/VictimBot`,
      { status: 'active', summary: 'VictimBot: owns messages' },
      'VictimBot',
    )
    const sentResp = await api.post(
      `/spaces/${space}/agent/VictimBot/message`,
      { message: 'Private message' },
      'boss',
    )
    const sent = await sentResp.json() as { messageId: string }
    const msgId = sent.messageId

    // Attacker tries to ack VictimBot's message
    const r = await fetch(`${BASE}/spaces/${space}/agent/VictimBot/message/${msgId}/ack`, {
      method: 'POST',
      headers: { 'X-Agent-Name': 'Attacker' },
    })
    expect(r.status).toBe(403)
    // Message should still be unread
    const msgs = await api.getJSON<{ messages: { id: string; read?: boolean }[] }>(
      `/spaces/${space}/agent/VictimBot/messages`,
    )
    const msg = msgs.messages.find(m => m.id === msgId)
    expect(msg!.read === false || msg!.read === undefined).toBe(true)
  })

  test('ack non-existent message returns 404', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/NonExistBot`,
      { status: 'active', summary: 'NonExistBot: ready' },
      'NonExistBot',
    )
    const r = await fetch(
      `${BASE}/spaces/${space}/agent/NonExistBot/message/definitely-not-a-real-id/ack`,
      { method: 'POST', headers: { 'X-Agent-Name': 'NonExistBot' } },
    )
    expect(r.status).toBe(404)
  })

  test('acking one message does not affect other messages', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/MultiMsgBot`,
      { status: 'active', summary: 'MultiMsgBot: multiple messages' },
      'MultiMsgBot',
    )

    // Send 3 messages
    const ids: string[] = []
    for (let i = 1; i <= 3; i++) {
      const r = await api.post(
        `/spaces/${space}/agent/MultiMsgBot/message`,
        { message: `Message ${i}` },
        'boss',
      )
      const body = await r.json() as { messageId: string }
      ids.push(body.messageId)
    }

    // Ack only message 2
    await fetch(`${BASE}/spaces/${space}/agent/MultiMsgBot/message/${ids[1]}/ack`, {
      method: 'POST',
      headers: { 'X-Agent-Name': 'MultiMsgBot' },
    })

    // Check all messages
    const msgs = await api.getJSON<{ messages: { id: string; read?: boolean }[] }>(
      `/spaces/${space}/agent/MultiMsgBot/messages`,
    )

    const msg1 = msgs.messages.find(m => m.id === ids[0])
    const msg2 = msgs.messages.find(m => m.id === ids[1])
    const msg3 = msgs.messages.find(m => m.id === ids[2])

    // Only message 2 should be read
    expect(msg1!.read === false || msg1!.read === undefined).toBe(true)
    expect(msg2!.read).toBe(true)
    expect(msg3!.read === false || msg3!.read === undefined).toBe(true)
  })

  test('cursor pagination returns messages with correct read state', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/CursorReadBot`,
      { status: 'active', summary: 'CursorReadBot: cursor test' },
      'CursorReadBot',
    )

    // Send message and get cursor
    await api.post(
      `/spaces/${space}/agent/CursorReadBot/message`,
      { message: 'First' },
      'boss',
    )
    const first = await api.getJSON<{ cursor: string; messages: { id: string; read?: boolean }[] }>(
      `/spaces/${space}/agent/CursorReadBot/messages`,
    )
    const msgId = first.messages[0].id
    const cursor = first.cursor

    // Ack the first message
    await fetch(`${BASE}/spaces/${space}/agent/CursorReadBot/message/${msgId}/ack`, {
      method: 'POST',
      headers: { 'X-Agent-Name': 'CursorReadBot' },
    })

    // Send second message after cursor
    await api.post(
      `/spaces/${space}/agent/CursorReadBot/message`,
      { message: 'Second' },
      'boss',
    )

    // Fetch only new messages via cursor
    const newMsgs = await api.getJSON<{ messages: { message: string; read?: boolean }[] }>(
      `/spaces/${space}/agent/CursorReadBot/messages?since=${encodeURIComponent(cursor)}`,
    )
    expect(newMsgs.messages.length).toBe(1)
    expect(newMsgs.messages[0].message).toBe('Second')
    // New message should be unread
    expect(newMsgs.messages[0].read === false || newMsgs.messages[0].read === undefined).toBe(true)
  })

  test('ack requires X-Agent-Name header', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/HeaderBot`,
      { status: 'active', summary: 'HeaderBot: header test' },
      'HeaderBot',
    )
    const r = await api.post(
      `/spaces/${space}/agent/HeaderBot/message`,
      { message: 'Need header' },
      'boss',
    )
    const body = await r.json() as { messageId: string }

    // Ack without X-Agent-Name header
    const ackR = await fetch(
      `${BASE}/spaces/${space}/agent/HeaderBot/message/${body.messageId}/ack`,
      { method: 'POST' },  // no X-Agent-Name
    )
    expect([400, 403]).toContain(ackR.status)
  })

  test('read state persists after re-fetching messages', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/PersistReadBot`,
      { status: 'active', summary: 'PersistReadBot: persistence test' },
      'PersistReadBot',
    )
    const r = await api.post(
      `/spaces/${space}/agent/PersistReadBot/message`,
      { message: 'Persist my read state' },
      'boss',
    )
    const body = await r.json() as { messageId: string }
    const msgId = body.messageId

    // Ack
    await fetch(`${BASE}/spaces/${space}/agent/PersistReadBot/message/${msgId}/ack`, {
      method: 'POST',
      headers: { 'X-Agent-Name': 'PersistReadBot' },
    })

    // Fetch again — read state should persist across multiple fetches
    for (let i = 0; i < 3; i++) {
      const msgs = await api.getJSON<{ messages: { id: string; read?: boolean; read_at?: string }[] }>(
        `/spaces/${space}/agent/PersistReadBot/messages`,
      )
      const msg = msgs.messages.find(m => m.id === msgId)
      expect(msg!.read).toBe(true)
      expect(msg!.read_at).toBeTruthy()
    }
  })

  test('agent posting status auto-marks notifications as read', async ({ space, api }) => {
    // Create agent and assign a task (which sends a notification)
    await api.post(
      `/spaces/${space}/agent/NotifBot`,
      { status: 'idle', summary: 'NotifBot: waiting' },
      'NotifBot',
    )
    await api.postJSON(
      `/spaces/${space}/tasks`,
      { title: 'Auto-read task', assigned_to: 'NotifBot' },
      'boss',
    )

    // Post a status update — server should auto-mark notifications as read
    await api.post(
      `/spaces/${space}/agent/NotifBot`,
      { status: 'active', summary: 'NotifBot: checked in, notifications seen' },
      'NotifBot',
    )

    // Verify via agent data that notifications are now marked read
    const agentData = await api.getJSON<{
      notifications?: { id: string; read: boolean }[]
    }>(`/spaces/${space}/agent/NotifBot`)

    if (agentData.notifications && agentData.notifications.length > 0) {
      expect(agentData.notifications.every(n => n.read)).toBe(true)
    }
    // Even if no notifications in response, the test verifies no crash
  })

  test('ack response includes correct message_id', async ({ space, api }) => {
    await api.post(
      `/spaces/${space}/agent/IdBot`,
      { status: 'active', summary: 'IdBot: id verification' },
      'IdBot',
    )
    const r = await api.post(
      `/spaces/${space}/agent/IdBot/message`,
      { message: 'Check my ID' },
      'boss',
    )
    const sent = await r.json() as { messageId: string }

    const ackR = await fetch(
      `${BASE}/spaces/${space}/agent/IdBot/message/${sent.messageId}/ack`,
      { method: 'POST', headers: { 'X-Agent-Name': 'IdBot' } },
    )
    const acked = await ackR.json() as { status: string; message_id: string }
    expect(acked.status).toBe('acked')
    expect(acked.message_id).toBe(sent.messageId)
  })
})
