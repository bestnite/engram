import { defineConfig, devices } from '@playwright/test';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

/**
 * Playwright configuration for the real-browser suite.
 *
 * The suite holds what only a real browser can prove. `browser-isolation.spec.ts` proves the sandbox of the card frame,
 * because the browser is the only place where CSP, sandbox and origin rules are enforced. `note-types.spec.ts` and
 * `review-av.spec.ts` open pages whose failure depends on how the browser clones a Svelte state proxy (structuredClone and
 * postMessage refuse one), which the DOM implementation of the client tests does not reproduce; they sign in through
 * `e2e/session.ts`. `edit-frame-theme.spec.ts` proves that the rich-text frame on a dark page stays hidden until its
 * document is dark, because only a browser paints the opaque canvas of a frame whose color-scheme differs from the
 * page's. `edit-frame-media.spec.ts` proves that a stored and a freshly uploaded image both load inside that frame, because
 * only a browser applies the frame's base element and withholds the session cookie from it. The Go server is not
 * configured here: Playwright loads this file in its worker processes as well, so a top-level side effect would pick a
 * different port and database in every process. `e2e/global-setup.ts` runs once, starts the server on a free port with a
 * throwaway SQLite database and exports `ENGRAM_E2E_BASE_URL`, which each test file uses as its `baseURL`.
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
