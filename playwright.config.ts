import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: 'e2e',
  retries: process.env.CI ? 1 : 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: process.env.BASE_URL ?? 'http://localhost:3000',
    trace: 'retain-on-failure',
  },
  // *.cnote-e2e.test resolves to this machine, so a test can browse a "custom domain" (site-routing.spec.ts).
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: { args: ['--host-resolver-rules=MAP *.cnote-e2e.test 127.0.0.1'] },
      },
    },
  ],
})
