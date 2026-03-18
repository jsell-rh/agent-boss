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
    api,
  }) => {
    await api.post(
      `/spaces/${space}/agent/MobileBot`,
      { status: 'active', summary: 'MobileBot: mobile test' },
      'MobileBot',
    )
    await api.post(
      `/spaces/${space}/agent/MobileBot/message`,
      { message: 'Mobile test message' },
      'boss',
    )

    await page.setViewportSize(MOBILE_VIEWPORT)
    await page.goto(`${BASE}/${encodeURIComponent(space)}/conversations`)
    await page.waitForTimeout(1500)

    // Conversation list (aside) should be visible at full width
    const convList = page.locator('aside[aria-label="Conversations"]')
    await expect(convList).toBeVisible({ timeout: 10_000 })

    // Thread panel should be hidden (no conversation selected yet)
    // The right panel has class "hidden md:flex" when no selectedKey
    const threadPanel = page.locator('aside[aria-label="Conversations"] ~ div').first()
    // On mobile with no selection: thread panel has display:none equivalent
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

    // Tap the conversation
    const convBtn = page.getByRole('listbox').getByText('TapBot').first()
    await expect(convBtn).toBeVisible({ timeout: 10_000 })
    await convBtn.click()
    await page.waitForTimeout(500)

    // Thread panel should now be visible
    await expect(page.getByText('Tap to open this thread').first()).toBeVisible({ timeout: 5_000 })

    // Conversation list should now be hidden (hidden md:flex with selectedKey set)
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

    // Open the thread
    const convBtn = page.getByRole('listbox').getByText('BackBot').first()
    await expect(convBtn).toBeVisible({ timeout: 10_000 })
    await convBtn.click()
    await page.waitForTimeout(500)

    // Thread should be visible
    await expect(page.getByText('Back button test').first()).toBeVisible({ timeout: 5_000 })

    // Click the back button
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

    // Assign persona to agent config
    await api.put(`/spaces/${space}/agent/PersonaAgent/config`, {
      personas: [{ id: persona.id, pinned_version: persona.version }],
    })

    await page.goto(`${BASE}/${encodeURIComponent(space)}`)
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

    // Toggle should initially show filters closed (search input not visible)
    const searchInput = page.getByPlaceholder('Search tasks…').first()
    // On mobile, search is inside the collapsed filter panel (not always visible)

    // Click toggle
    const filterToggle = page.getByRole('button', { name: /Show filters|Hide filters/ })
    await filterToggle.click()
    await page.waitForTimeout(300)

    // Filter panel should now be open with search input visible
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
