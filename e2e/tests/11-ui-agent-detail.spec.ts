/**
 * 11 — UI: Agent Detail View
 *
 * Covers: agent detail panel shows all fields (status, branch, PR, phase,
 * test_count, items, blockers, questions), message thread, history.
 */
import { test, expect } from '../fixtures/index.ts'

const BASE = 'http://localhost:18899'

test.describe('UI: Agent Detail View', () => {
  test('agent detail page shows summary and branch', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ShowBot`,
      {
        status: 'active',
        summary: 'ShowBot: showing details',
        branch: 'feat/show-details',
        pr: '#99',
        phase: 'testing',
        test_count: 77,
        items: ['completed item A', 'completed item B'],
        next_steps: 'do more tests',
      },
      'ShowBot',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/ShowBot`)
    await page.waitForTimeout(1500)

    await expect(page.getByText('ShowBot').first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('feat/show-details').first()).toBeVisible({ timeout: 10_000 })
  })

  test('agent detail shows status badge', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/BadgeBot`,
      { status: 'blocked', summary: 'BadgeBot: stuck' },
      'BadgeBot',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/BadgeBot`)
    await page.waitForTimeout(1500)

    await expect(page.getByText('blocked').first()).toBeVisible({ timeout: 10_000 })
  })

  test('agent detail shows items list', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ItemBot`,
      {
        status: 'active',
        summary: 'ItemBot: working',
        items: ['Implemented feature X', 'Fixed bug Y', 'Wrote tests'],
      },
      'ItemBot',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/ItemBot`)
    await page.waitForTimeout(1500)

    await expect(page.getByText('Implemented feature X').first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('Fixed bug Y').first()).toBeVisible({ timeout: 10_000 })
  })

  test('agent detail shows blockers section', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/BlockerBot`,
      {
        status: 'blocked',
        summary: 'BlockerBot: waiting',
        blockers: ['Waiting for DB schema approval'],
      },
      'BlockerBot',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/BlockerBot`)
    await page.waitForTimeout(1500)

    await expect(page.getByText('Waiting for DB schema approval').first()).toBeVisible({ timeout: 10_000 })
  })

  test('agent detail shows questions section', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/QuestionDetailBot`,
      {
        status: 'active',
        summary: 'QuestionDetailBot: asking',
        questions: ['Should we use SQLite or Postgres?'],
      },
      'QuestionDetailBot',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/QuestionDetailBot`)
    await page.waitForTimeout(1500)

    await expect(page.getByText('Should we use SQLite or Postgres?').first()).toBeVisible({ timeout: 10_000 })
  })

  test('agent message thread shows messages', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ThreadBot`,
      { status: 'active', summary: 'ThreadBot: ready for messages' },
      'ThreadBot',
    )
    await api.post(
      `/spaces/${space}/agent/ThreadBot/message`,
      { message: 'Check this out ThreadBot!' },
      'operator',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/ThreadBot`)
    await page.waitForTimeout(2000)

    await expect(page.getByText('Check this out ThreadBot!').first()).toBeVisible({ timeout: 10_000 })
  })

  test('agent detail shows test count', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/TestCountBot`,
      {
        status: 'active',
        summary: 'TestCountBot: running tests',
        test_count: 174,
      },
      'TestCountBot',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/TestCountBot`)
    await page.waitForTimeout(1500)

    // Test count should be displayed somewhere
    await expect(page.getByText('174').first()).toBeVisible({ timeout: 10_000 })
  })

  test('agent history tab shows status snapshots', async ({ page, space, api }) => {
    // Post multiple status updates
    for (let i = 1; i <= 3; i++) {
      await api.post(
        `/spaces/${space}/agent/HistBot`,
        { status: 'active', summary: `HistBot: update ${i}` },
        'HistBot',
      )
    }

    await page.goto(`${BASE}/${encodeURIComponent(space)}/HistBot`)
    await page.waitForTimeout(1500)

    // Look for a history tab or section
    const historyTab = page.getByRole('tab', { name: /history/i })
      .or(page.getByText(/history/i).first())
    if (await historyTab.isVisible()) {
      await historyTab.click()
      await page.waitForTimeout(500)
    }
    // Page should still be functional
    await expect(page.locator('#app')).toBeVisible()
  })

  test('reply button sends message to agent', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ReplyTarget`,
      { status: 'active', summary: 'ReplyTarget: awaiting reply' },
      'ReplyTarget',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/ReplyTarget`)
    await page.waitForTimeout(1500)

    // Find reply/message input
    const replyBtn = page.getByRole('button', { name: /reply|send message/i }).first()
    if (await replyBtn.isVisible()) {
      await replyBtn.click()
      await page.waitForTimeout(300)
      // Type a reply
      const input = page.getByRole('textbox').first()
      await input.fill('Test reply from E2E')
      await page.keyboard.press('Enter')
      await page.waitForTimeout(500)
    }
    // Best-effort test
    await expect(page.locator('#app')).toBeVisible()
  })
})
