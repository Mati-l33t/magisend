import { defineConfig } from '@playwright/test';

// Browser tests create accounts and data, so they only ever run against the
// disposable loopback server started by scripts/e2e.mjs (npm run e2e).
export default defineConfig({
  testDir: './e2e',
  workers: 1,
  timeout: 90000,
  retries: 0,
  use: {
    baseURL: 'http://127.0.0.1:18087',
    browserName: 'chromium',
    headless: true,
    locale: 'sv-SE',
    viewport: { width: 1440, height: 1000 },
    // The flows are written for the original look; layout.spec.ts checks the default.
    storageState: {
      cookies: [],
      origins: [
        {
          origin: 'http://127.0.0.1:18087',
          localStorage: [{ name: 'magisend-look', value: 'classic' }],
        },
      ],
    },
    trace: 'retain-on-failure',
    // A service worker that controls the page hides its requests from
    // page.route, so tests that hold back or stub requests would depend on
    // how fast it starts. Only pwa.spec.ts needs it.
    serviceWorkers: 'block',
  },
  reporter: [['list']],
  outputDir: 'test-results',
});
