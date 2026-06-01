import { defineConfig, devices } from '@playwright/test'

/**
 * Playwright config — drives the Vite preview build against a mocked
 * gateway-svc via Playwright `page.route()` (see tests/e2e/fixtures/mocks.ts).
 *
 * Running locally:
 *
 *   pnpm exec playwright install --with-deps chromium
 *   pnpm build && pnpm exec playwright test
 *
 * In CI:
 *
 *   - retries=2 for flaky network-stub timing
 *   - workers=1 to keep storageState isolation simple
 *   - HTML report uploaded as an artifact
 */
export default defineConfig({
  testDir: './tests/e2e',
  // Each spec stands on its own mocks and storageState. fullyParallel is safe.
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: 'http://localhost:5173',
    trace: 'on-first-retry',
    // Screenshots only on failures — keeps artifact size manageable in CI.
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  // The web server is the production preview build, not `pnpm dev`. This
  // ensures we exercise the same bundle that ships, and it boots faster
  // than Vite dev with HMR.
  webServer: {
    command: 'pnpm preview',
    url: 'http://localhost:5173',
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
})
