/**
 * 16 — UI: Mobile layout + persona display
 *
 * Covers:
 * - ConversationsView mobile single-column: list visible, thread hidden until tap
 * - ConversationsView mobile: tapping conversation reveals thread panel
 * - ConversationsView mobile: back button returns to list
 * - Persona badge displayed on agent card when persona assigned
 * - Kanban filter toolbar: mobile toggle button visible, filters collapse on mobile
 */
import { test, expect } from '../fixtures/index.ts'

const BASE = 'http://localhost:18899'

// Mobile viewport used across mobile tests
const MOBILE_VIEWPORT = { width: 375, height: 812 }

test.describe('UI: ConversationsView mobile single-column', () => {
  test('on mobile: conversation list is visible, thread panel is hidden initially', async ({
    page,
    space,
  }) => {
    // Do NOT add any agents/messages — empty space means no conversations,
    // so the auto-select watch cannot fire and the list stays visible.
    await page.setViewportSize(MOBILE_VIEWPORT)
    await page.goto(`${BASE}/${encodeURIComponent(space)}/conversations`)
    await page.waitForTimeout(1500)

    // Conversation list (aside) should be visible at full width
    const convList = page.locator('aside[aria-label="Conversations"]')
    await expect(convList).toBeVisible({ timeout: 10_000 })

    // Thread panel should be hidden (no conversation selected)
    const threadPanel = page.locator('aside[aria-label="Conversations"] ~ div').first()
    const isHidden = await threadPanel.evaluate(el => {
      const style = window.getComputedStyle(el)
      return style.display === 'none'
    })
    expect(isHidden).toBe(true)
  })

  test('on mobile: tapping conversation shows thread panel, hides list', async ({
    page,
    space,
    api,
  }) => {
    await api.post(
      `/spaces/${space}/agent/TapBot`,
      { status: 'active', summary: 'TapBot: tap to open' },
      'TapBot',
    )
    await api.post(
      `/spaces/${space}/agent/TapBot/message`,
      { message: 'Tap to open this thread' },
      'boss',
    )

    await page.setViewportSize(MOBILE_VIEWPORT)
    await page.goto(`${BASE}/${encodeURIComponent(space)}/conversations`)
    await page.waitForTimeout(1500)

    // Auto-select fires and selects TapBot (the only conversation).
    // The thread panel is already showing — verify the message is visible.
    await expect(page.getByText('Tap to open this thread').first()).toBeVisible({ timeout: 10_000 })

    // With a conversation selected on mobile, list must be hidden
    const convList = page.locator('aside[aria-label="Conversations"]')
    const listHidden = await convList.evaluate(el => {
      const style = window.getComputedStyle(el)
      return style.display === 'none'
    })
    expect(listHidden).toBe(true)
  })

  test('on mobile: back button returns to conversation list', async ({
    page,
    space,
    api,
  }) => {
    await api.post(
      `/spaces/${space}/agent/BackBot`,
      { status: 'active', summary: 'BackBot: back button test' },
      'BackBot',
    )
    await api.post(
      `/spaces/${space}/agent/BackBot/message`,
      { message: 'Back button test' },
      'boss',
    )

    await page.setViewportSize(MOBILE_VIEWPORT)
    await page.goto(`${BASE}/${encodeURIComponent(space)}/conversations`)
    await page.waitForTimeout(1500)

    // Auto-select fires and selects BackBot (the only conversation).
    // Thread should be visible.
    await expect(page.getByText('Back button test').first()).toBeVisible({ timeout: 10_000 })

    // Click the back button to return to the list
    const backBtn = page.getByRole('button', { name: 'Back to conversation list' })
    await expect(backBtn).toBeVisible({ timeout: 5_000 })
    await backBtn.click()
    await page.waitForTimeout(500)

    // Conversation list should be visible again
    const convList = page.locator('aside[aria-label="Conversations"]')
    await expect(convList).toBeVisible({ timeout: 5_000 })
    const listVisible = await convList.evaluate(el => {
      const style = window.getComputedStyle(el)
      return style.display !== 'none'
    })
    expect(listVisible).toBe(true)
  })

  test('on desktop: both list and thread visible side-by-side', async ({
    page,
    space,
    api,
  }) => {
    await api.post(
      `/spaces/${space}/agent/DesktopBot`,
      { status: 'active', summary: 'DesktopBot' },
      'DesktopBot',
    )
    await api.post(
      `/spaces/${space}/agent/DesktopBot/message`,
      { message: 'Desktop test' },
      'boss',
    )

    // Desktop viewport
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto(`${BASE}/${encodeURIComponent(space)}/conversations/DesktopBot`)
    await page.waitForTimeout(1500)

    // Both panels visible simultaneously
    const convList = page.locator('aside[aria-label="Conversations"]')
    await expect(convList).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('Desktop test').first()).toBeVisible({ timeout: 10_000 })
  })
})

test.describe('UI: Persona display on agent cards', () => {
  test('persona badge appears on agent card when persona assigned', async ({
    page,
    space,
    api,
  }) => {
    // Create agent
    await api.post(
      `/spaces/${space}/agent/PersonaAgent`,
      { status: 'active', summary: 'PersonaAgent: has persona' },
      'PersonaAgent',
    )

    // Create a persona
    const personaResp = await api.post('/personas', {
      name: 'TestPersona',
      description: 'A test persona',
      prompt: 'You are a test persona.',
    })
    const persona = (await personaResp.json()) as { id: string; version: number }

    // Assign persona to agent config BEFORE navigating
    await api.put(`/spaces/${space}/agent/PersonaAgent/config`, {
      personas: [{ id: persona.id, pinned_version: persona.version }],
    })

    // Navigate and reload to ensure the page sees the latest config
    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(500)
    await page.reload()
    await page.waitForTimeout(1500)

    // Persona name badge should appear on the agent card
    const badge = page.getByText('TestPersona').first()
    await expect(badge).toBeVisible({ timeout: 10_000 })

    // Clean up persona
    await api.del(`/personas/${persona.id}`)
  })

  test('no persona badge on agent card when no persona assigned', async ({
    page,
    space,
    api,
  }) => {
    await api.post(
      `/spaces/${space}/agent/NoPeronaAgent`,
      { status: 'active', summary: 'NoPeronaAgent: no persona' },
      'NoPeronaAgent',
    )

    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
    await page.waitForTimeout(1500)

    // No persona-related violet badge should appear
    // (just verify the agent card renders without persona chip)
    await expect(page.getByText('NoPeronaAgent').first()).toBeVisible({ timeout: 10_000 })
    // The text "Active persona" should not be present in the page
    const hasPersonaBadge = await page.getByTitle(/Active persona/).isVisible().catch(() => false)
    expect(hasPersonaBadge).toBe(false)
  })
})

test.describe('UI: Kanban mobile filter toolbar', () => {
  test('on mobile: filter toggle button is visible', async ({ page, space }) => {
    await page.setViewportSize(MOBILE_VIEWPORT)
    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1000)

    // The SlidersHorizontal filter toggle button should be visible on mobile
    const filterToggle = page.getByRole('button', { name: /Show filters|Hide filters/ })
    await expect(filterToggle).toBeVisible({ timeout: 10_000 })
  })

  test('on mobile: tapping filter toggle opens filter panel', async ({ page, space }) => {
    await page.setViewportSize(MOBILE_VIEWPORT)
    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1000)

    // Click toggle to open filter panel
    const filterToggle = page.getByRole('button', { name: /Show filters|Hide filters/ })
    await filterToggle.click()
    await page.waitForTimeout(300)

    // Filter panel should now be open — the mobile search input (inside the
    // expanded panel) must be visible. Use filter({ visible: true }) to avoid
    // picking the desktop-only hidden input.
    const searchInput = page.getByPlaceholder('Search tasks…').filter({ visible: true }).first()
    await expect(searchInput).toBeVisible({ timeout: 5_000 })
  })

  test('on desktop: filter controls are visible without toggle', async ({ page, space }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto(`${BASE}/${encodeURIComponent(space)}/kanban`)
    await page.waitForTimeout(1000)

    // On desktop, filter controls are always visible inline — no toggle needed
    const filterToggle = page.getByRole('button', { name: /Show filters|Hide filters/ })
    const toggleVisible = await filterToggle.isVisible().catch(() => false)
    expect(toggleVisible).toBe(false)
  })
})
