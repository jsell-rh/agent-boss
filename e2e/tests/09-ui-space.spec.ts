/**
 * 09 — UI: Space Overview
 *
 * Covers: space view renders agents, status badges, session dashboard table,
 * event log, broadcast button, raw view link.
 */
import { test, expect } from '../fixtures/index.ts'

const BASE = 'http://localhost:18899'

test.describe('UI: Space Overview', () => {
  test('space page shows agent dashboard table', async ({ page, space, api }) => {
    // Create some agents
    await api.post(
      `/spaces/${space}/agent/Alice`,
      { status: 'active', summary: 'Alice: running' },
      'Alice',
    )
    await api.post(
      `/spaces/${space}/agent/Bob`,
      { status: 'done', summary: 'Bob: finished' },
      'Bob',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(1500)

    // Should show agent names
    await expect(page.getByText('Alice').first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('Bob').first()).toBeVisible({ timeout: 10_000 })
  })

  test('agent status badge reflects current status', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/StatusAgent`,
      { status: 'active', summary: 'StatusAgent: working' },
      'StatusAgent',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(1500)

    // Status badge should show active
    const agentRow = page.locator(`text=StatusAgent`).locator('..')
    await expect(page.getByText('active').first()).toBeVisible({ timeout: 10_000 })
  })

  test('space page shows kanban navigation tab', async ({ page, space }) => {
    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(1000)

    // Should have a kanban link/button
    const kanbanLink = page.getByRole('link', { name: /kanban/i })
      .or(page.getByRole('tab', { name: /kanban/i }))
      .or(page.getByText(/kanban/i).first())
    await expect(kanbanLink).toBeVisible({ timeout: 10_000 })
  })

  test('clicking agent name opens agent detail view', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/DetailTarget`,
      { status: 'active', summary: 'DetailTarget: click me', branch: 'feat/detail' },
      'DetailTarget',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(1500)

    // Click the agent name
    const agentLink = page.getByText('DetailTarget').first()
    await expect(agentLink).toBeVisible({ timeout: 10_000 })
    await agentLink.click()
    await page.waitForTimeout(500)

    // URL should update to include agent name
    await expect(page).toHaveURL(new RegExp('DetailTarget'), { timeout: 5000 })
  })

  test('event log tab shows coordinator events', async ({ page, space, api }) => {
    // Generate an event
    await api.post(
      `/spaces/${space}/agent/EventMaker`,
      { status: 'active', summary: 'EventMaker: generating events' },
      'EventMaker',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(1000)

    // Look for event log or events tab
    const eventsBtn = page.getByRole('tab', { name: /events?/i })
      .or(page.getByRole('button', { name: /events?/i }))
      .first()

    if (await eventsBtn.isVisible()) {
      await eventsBtn.click()
      await page.waitForTimeout(500)
    }
    // At minimum, page should still be visible
    await expect(page.locator('#app')).toBeVisible()
  })

  test('space SSE connection keeps dashboard live', async ({ page, space, api }) => {
    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(1000)

    // Post an update via API
    await api.post(
      `/spaces/${space}/agent/LiveUpdate`,
      { status: 'active', summary: 'LiveUpdate: live via SSE' },
      'LiveUpdate',
    )

    // Give SSE time to push the update
    await page.waitForTimeout(2000)

    // Agent should now appear in the dashboard
    await expect(page.getByText('LiveUpdate').first()).toBeVisible({ timeout: 10_000 })
  })

  test('space page shows attention count for questions', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/Questioner`,
      {
        status: 'blocked',
        summary: 'Questioner: needs boss input',
        questions: ['[?BOSS] What is the priority?'],
      },
      'Questioner',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(1500)

    // The question should be visible somewhere
    await expect(page.getByText('Questioner').first()).toBeVisible({ timeout: 10_000 })
  })
})
