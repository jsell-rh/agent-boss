import { defineConfig, devices } from '@playwright/test'
import { TEST_PORT, BASE_URL } from './constants.ts'

export { TEST_PORT, BASE_URL }

export default defineConfig({
  testDir: './tests',
  timeout: 60_000,          // 60s per test (server restarts can be slow)
  expect: { timeout: 15_000 },
  // Parallelism: most tests use isolated spaces and are safe to run concurrently.
  // The persistence test (07) restarts the server, so it must run after all others.
  // We use two projects with `dependencies` to enforce ordering:
  //   1. parallel-tests — all non-persistence specs, 4 workers
  //   2. persistence    — 07-persistence only, runs after parallel-tests finishes
  fullyParallel: true,
  workers: 4,
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
      // All tests except 07-persistence — run in parallel with 4 workers
      name: 'parallel-tests',
      use: { ...devices['Desktop Chrome'] },
      testIgnore: ['**/07-persistence.spec.ts'],
    },
    {
      // Server-restart test: must run after all parallel tests to avoid
      // disrupting concurrent workers during server restart.
      name: 'persistence',
      use: { ...devices['Desktop Chrome'] },
      testMatch: ['**/07-persistence.spec.ts'],
      dependencies: ['parallel-tests'],
    },
  ],
})
