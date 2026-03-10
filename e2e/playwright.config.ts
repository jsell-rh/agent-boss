import { defineConfig, devices } from '@playwright/test'
import { TEST_PORT, BASE_URL } from './constants.ts'

export { TEST_PORT, BASE_URL }

export default defineConfig({
  testDir: './tests',
  timeout: 60_000,          // 60s per test (server restarts can be slow)
  expect: { timeout: 15_000 },
  fullyParallel: false,     // Serial — tests share a live server
  workers: 1,               // Single worker for deterministic ordering
  retries: 0,
  reporter: [
    ['list'],
    ['html', { outputFolder: 'playwright-report', open: 'never' }],
  ],

  globalSetup: './global-setup.ts',
  globalTeardown: './global-teardown.ts',

  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
