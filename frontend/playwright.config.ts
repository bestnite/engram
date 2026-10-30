import { defineConfig, devices } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

/**
 * Playwright configuration for the browser-isolation suite (M17-4).
 *
 * The suite proves the sandbox of the card frame in a real browser, because the browser is the only place where CSP,
 * sandbox and origin rules are enforced; a DOM implementation in a unit test cannot prove them. The Go server is not
 * configured here: Playwright loads this file in its worker processes as well, so a top-level side effect would pick a
 * different port and database in every process. `e2e/global-setup.ts` runs once, starts the server on a free port with a
 * throwaway SQLite database and exports `ENGRAM_E2E_BASE_URL`, which the test file uses as its `baseURL`.
 */
export default defineConfig({
  testDir: './e2e',

  // The browser check is a security gate: an empty run must fail instead of reporting success, and a stray `.only`
  // must not silently shrink the suite to one test.
  forbidOnly: true,
  fullyParallel: false,
  workers: 1,

  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],

  // Artifacts are written outside the repository so a failing run never leaves the working tree dirty.
  outputDir: join(tmpdir(), 'engram-e2e-results'),

  globalSetup: './e2e/global-setup.ts',

  use: {
    // A trace is kept only when a test fails; it carries the frame's console and network activity.
    trace: 'retain-on-failure',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
