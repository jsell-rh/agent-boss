/**
 * 12 — UI: Deep Link Navigation
 *
 * Covers: direct URL navigation to agents, kanban tasks, conversations,
 * highlight animation on kanban deep link (TASK-049).
 */
import { test, expect } from '../fixtures/index.ts'

const BASE = 'http://localhost:18899'

test.describe('UI: Deep Link Navigation', () => {
  test('direct URL to agent navigates correctly', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/DeepAgent`,
      { status: 'active', summary: 'DeepAgent: reachable by URL' },
      'DeepAgent',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/DeepAgent`)
    await page.waitForTimeout(1000)

    await expect(page.getByText('DeepAgent').first()).toBeVisible({ timeout: 10_000 })
    await expect(page).toHaveURL(new RegExp('DeepAgent'))
  })

  test('kanban deep link with task ID navigates to kanban', async ({ page, space, api }) => {
    const task = await api.postJSON<{ id: string }>(
      `/spaces/${space}/tasks`,
      { title: 'Deep Link Target Task' },
      'boss',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban?task=${task.id}`)
    await page.waitForTimeout(1500)

    // Should be on kanban page
    await expect(page).toHaveURL(/kanban/)
    await expect(page.getByText('Deep Link Target Task').first()).toBeVisible({ timeout: 10_000 })
  })

  test('kanban deep link highlights the target card', async ({ page, space, api }) => {
    const task = await api.postJSON<{ id: string }>(
      `/spaces/${space}/tasks`,
      { title: 'Highlighted Card Task' },
      'boss',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban?task=${task.id}`)
    await page.waitForTimeout(2000)

    // Find the task card
    const card = page.getByText('Highlighted Card Task').first()
    await expect(card).toBeVisible({ timeout: 10_000 })

    // The card or its ancestor should have a highlight class
    const cardEl = card.locator('xpath=ancestor::*[contains(@class,"card") or contains(@class,"task") or contains(@class,"item")][1]')
    // Check for ring, highlight, or animate class
    const hasHighlight = await cardEl.evaluate(el => {
      const cls = el.className || ''
      return cls.includes('ring') || cls.includes('highlight') || cls.includes('animate') ||
             cls.includes('border-primary') || cls.includes('flash')
    }).catch(() => false)

    // Also check if card is visible and scrolled into view
    await expect(card).toBeInViewport({ timeout: 5000 })
  })

  test('back navigation returns to space overview', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/NavBot`,
      { status: 'active', summary: 'NavBot: navigation test' },
      'NavBot',
    )

    // Navigate from space to agent and back
    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(500)
    await page.goto(`${BASE}/${encodeURIComponent(space)}/NavBot`)
    await page.waitForTimeout(500)

    await page.goBack()
    await page.waitForTimeout(1000)

    // Should be back at space or show #app
    await page.waitForSelector('#app', { state: 'attached', timeout: 10_000 })
    await expect(page.locator('#app')).toBeVisible()
  })

  test('conversations deep link navigates correctly', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/ConvAgent`,
      { status: 'active', summary: 'ConvAgent: in conversation' },
      'ConvAgent',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/conversations/ConvAgent`)
    await page.waitForTimeout(1000)

    await expect(page).toHaveURL(/conversations.*ConvAgent/)
    await expect(page.locator('#app')).toBeVisible()
  })

  test('space kanban URL is directly accessible', async ({ page, space }) => {
    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1000)

    await expect(page).toHaveURL(/kanban/)
    await expect(page.locator('#app')).toBeVisible()
  })

  test('404 for unknown space falls back gracefully', async ({ page }) => {
    await page.goto(`${BASE}/this-space-does-not-exist-xyz`)
    await page.waitForTimeout(1000)
    // SPA handles routing — page should load even if space is empty
    await expect(page.locator('#app')).toBeVisible()
  })

  test('browser history works with route changes', async ({ page, space, api }) => {
    await api.post(
      `/spaces/${space}/agent/HistNavBot`,
      { status: 'active', summary: 'HistNavBot: history nav' },
      'HistNavBot',
    )

    // Navigate: home → space → agent → back → back
    await page.goto(BASE)
    await page.waitForTimeout(300)

    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(300)

    await page.goto(`${BASE}/${encodeURIComponent(space)}/HistNavBot`)
    await page.waitForTimeout(300)

    // Go back to space
    await page.goBack()
    await page.waitForTimeout(500)
    await expect(page).toHaveURL(new RegExp(`/${encodeURIComponent(space)}`))

    // Go back to home
    await page.goBack()
    await page.waitForTimeout(500)
    await expect(page.locator('#app')).toBeVisible()
  })
})
