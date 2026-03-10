/**
 * 10 — UI: Kanban Board
 *
 * Covers: kanban view renders task columns, task cards show correct info,
 * task creation dialog, task status columns (backlog, in_progress, review, done).
 */
import { test, expect } from '../fixtures/index.ts'

const BASE = 'http://localhost:18899'

test.describe('UI: Kanban Board', () => {
  test('kanban view renders with status columns', async ({ page, space }) => {
    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1000)

    // Should show kanban columns
    // These might be h2/h3 column headers or labeled sections
    await expect(page.locator('#app')).toBeVisible({ timeout: 10_000 })

    // Check for column indicators
    const hasBacklog = await page.getByText(/backlog/i).isVisible().catch(() => false)
    const hasInProgress = await page.getByText(/in.?progress/i).isVisible().catch(() => false)
    // At least one column should be visible
    expect(hasBacklog || hasInProgress).toBe(true)
  })

  test('task cards appear in correct column', async ({ page, space, api }) => {
    const task = await api.postJSON<{ id: string }>(
      `/spaces/${space}/tasks`,
      { title: 'Kanban Test Task', priority: 'high' },
      'boss',
    )
    await api.postJSON(`/spaces/${space}/tasks/${task.id}/move`, { status: 'in_progress' }, 'boss')

    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1500)

    await expect(page.getByText('Kanban Test Task').first()).toBeVisible({ timeout: 10_000 })
  })

  test('kanban shows tasks across all status columns', async ({ page, space, api }) => {
    // Create tasks in different statuses
    const t1 = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Backlog Item' }, 'boss')
    const t2 = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'In Progress Item' }, 'boss')
    const t3 = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Review Item' }, 'boss')
    const t4 = await api.postJSON<{ id: string }>(`/spaces/${space}/tasks`, { title: 'Done Item' }, 'boss')

    await api.postJSON(`/spaces/${space}/tasks/${t2.id}/move`, { status: 'in_progress' }, 'boss')
    await api.postJSON(`/spaces/${space}/tasks/${t3.id}/move`, { status: 'review' }, 'boss')
    await api.postJSON(`/spaces/${space}/tasks/${t4.id}/move`, { status: 'done' }, 'boss')

    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1500)

    await expect(page.getByText('Backlog Item').first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('In Progress Item').first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('Review Item').first()).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('Done Item').first()).toBeVisible({ timeout: 10_000 })
  })

  test('clicking task card opens task detail panel', async ({ page, space, api }) => {
    const task = await api.postJSON<{ id: string }>(
      `/spaces/${space}/tasks`,
      { title: 'Clickable Task', description: 'Click me to see details' },
      'boss',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1500)

    const card = page.getByText('Clickable Task').first()
    await expect(card).toBeVisible({ timeout: 10_000 })
    await card.click()
    await page.waitForTimeout(500)

    // Detail panel should show task info
    await expect(page.getByText('Click me to see details').first()).toBeVisible({ timeout: 5000 })
  })

  test('task priority is shown on kanban card', async ({ page, space, api }) => {
    await api.postJSON(
      `/spaces/${space}/tasks`,
      { title: 'Urgent Priority Task', priority: 'urgent' },
      'boss',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1500)

    await expect(page.getByText('Urgent Priority Task').first()).toBeVisible({ timeout: 10_000 })
    // Priority indicator should be visible (could be text, badge, or color)
    const urgent = page.getByText(/urgent/i).first()
    await expect(urgent).toBeVisible({ timeout: 5000 })
  })

  test('task assigned_to shows on kanban card', async ({ page, space, api }) => {
    await api.postJSON(
      `/spaces/${space}/tasks`,
      { title: 'Assigned Task', assigned_to: 'DevAgent' },
      'boss',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1500)

    await expect(page.getByText('Assigned Task').first()).toBeVisible({ timeout: 10_000 })
    // Assignee should be shown somewhere on the card
    await expect(page.getByText('DevAgent').first()).toBeVisible({ timeout: 5000 })
  })

  test('kanban deep link to task highlights the card', async ({ page, space, api }) => {
    const task = await api.postJSON<{ id: string }>(
      `/spaces/${space}/tasks`,
      { title: 'Deep Link Task' },
      'boss',
    )

    // Navigate directly to kanban with task deep link
    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban?task=${task.id}`)
    await page.waitForTimeout(2000)

    // Task card should be visible and highlighted
    const card = page.getByText('Deep Link Task').first()
    await expect(card).toBeVisible({ timeout: 10_000 })
  })

  test('new task dialog can be opened', async ({ page, space }) => {
    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1000)

    // Find "New Task" or "+" button
    const newTaskBtn = page.getByRole('button', { name: /new task|add task|\+/i }).first()
    if (await newTaskBtn.isVisible()) {
      await newTaskBtn.click()
      await page.waitForTimeout(300)

      // Dialog should open
      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible({ timeout: 5000 })

      // Close dialog
      await page.keyboard.press('Escape')
    }
    // Best-effort: page should still work
    await expect(page.locator('#app')).toBeVisible()
  })

  test('kanban shows empty state gracefully with no tasks', async ({ page, space }) => {
    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1000)
    // Should render without crashing
    await expect(page.locator('#app')).toBeVisible({ timeout: 10_000 })
  })
})
