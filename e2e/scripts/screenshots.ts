/**
 * Agent visual snapshot tool.
 *
 * Navigates to key pages and saves screenshots to e2e/snapshots/.
 * Agents can read these PNGs to understand current UI state.
 *
 * Usage:
 *   BOSS_URL=http://localhost:8899 npx tsx e2e/scripts/screenshots.ts
 *
 * Or via Makefile:
 *   make e2e-screenshots
 */

import { chromium } from '@playwright/test'
import fs from 'fs'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const SNAPSHOTS_DIR = path.resolve(__dirname, '..', 'snapshots')
const BASE_URL = process.env.BOSS_URL ?? 'http://localhost:8899'

async function take(name: string, url: string, page: import('@playwright/test').Page) {
  console.log(`  → ${name}: ${url}`)
  await page.goto(url, { waitUntil: 'domcontentloaded' })
  // Give Vue components a moment to render
  await page.waitForTimeout(500)
  const dest = path.join(SNAPSHOTS_DIR, `${name}.png`)
  await page.screenshot({ path: dest, fullPage: true })
  console.log(`    saved ${dest}`)
}

async function main() {
  fs.mkdirSync(SNAPSHOTS_DIR, { recursive: true })

  // Fetch space list to find a real space for deep-link screenshots
  let firstSpace: string | null = null
  try {
    const res = await fetch(`${BASE_URL}/spaces`)
    if (res.ok) {
      const spaces = (await res.json()) as Array<{ name: string }>
      if (spaces.length > 0) firstSpace = spaces[0].name
    }
  } catch {
    // server may be down — screenshots will show error state
  }

  const browser = await chromium.launch()
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })

  console.log(`\nCapturing screenshots from ${BASE_URL}`)
  console.log(`Saving to ${SNAPSHOTS_DIR}\n`)

  await take('01-home', BASE_URL, page)

  if (firstSpace) {
    const encoded = encodeURIComponent(firstSpace)
    await take('02-space', `${BASE_URL}/${encoded}`, page)
    await take('03-kanban', `${BASE_URL}/${encoded}/kanban`, page)
    await take('04-conversations', `${BASE_URL}/${encoded}/conversations`, page)

    // Agent detail — find first agent in the space
    try {
      const res = await fetch(`${BASE_URL}/spaces/${encoded}/agents`)
      if (res.ok) {
        const agents = (await res.json()) as Array<{ name: string }>
        if (agents.length > 0) {
          const agentName = encodeURIComponent(agents[0].name)
          await take('05-agent-detail', `${BASE_URL}/${encoded}/${agentName}`, page)
        }
      }
    } catch {
      // no agents — skip
    }
  } else {
    console.log('  (no spaces found — skipping space/kanban/agent screenshots)')
  }

  await browser.close()
  console.log('\nDone. Screenshots saved to e2e/snapshots/')
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
