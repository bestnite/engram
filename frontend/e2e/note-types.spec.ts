import { test, expect, type Page } from '@playwright/test';

/**
 * The note type page with a stored note type, in a real browser.
 *
 * The page copies the selected type's config out of Svelte state before editing it. That state is a proxy, and only a
 * real browser's structuredClone refuses a proxy: the DOM implementation the client tests run on clones it without
 * complaint, so the failure ("could not load note types" on every deck that has a type) never showed there. This case
 * opens the page against the Go server, through the same setup, login and API calls a user's browser makes.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL });

const USERNAME = 'e2e-note-types';
const PASSWORD = 'e2e-password-CHANGE_ME';

/** Calls the JSON API from the page, so the session cookie and the CSRF token are the browser's own. */
async function api(page: Page, method: string, path: string, body?: unknown): Promise<{ status: number; json: unknown }> {
  return page.evaluate(
    async ({ method, path, body }) => {
      const session = await (await fetch('/api/v1/auth/session')).json();
      const res = await fetch(path, {
        method,
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf_token ?? '' },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const text = await res.text();
      return { status: res.status, json: text ? JSON.parse(text) : null };
    },
    { method, path, body },
  );
}

test('the note type page opens a deck that has a note type', async ({ page }) => {
  await page.goto('/');
  // The global setup gives every run a fresh database, so this is its first account.
  const setup = await api(page, 'POST', '/api/v1/auth/setup', { username: USERNAME, email: 'e2e@example.com', password: PASSWORD });
  expect(setup.status, JSON.stringify(setup.json)).toBe(200);
  const login = await api(page, 'POST', '/api/v1/auth/login', { username: USERNAME, password: PASSWORD });
  expect(login.status).toBe(200);

  const deck = await api(page, 'POST', '/api/v1/decks', { name: 'Note type page' });
  expect(deck.status).toBe(201);
  const deckId = (deck.json as { id: string }).id;
  const created = await api(page, 'POST', `/api/v1/decks/${deckId}/note-types`, { stock_id: 'engram:basic' });
  expect(created.status).toBe(201);

  const pageErrors: string[] = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));
  await page.goto(`/decks/${deckId}/note-types`);

  await expect(page.getByTestId('note-types-failed')).toHaveCount(0);
  await expect(page.getByTestId('note-types-empty')).toHaveCount(0);
  // The stored type is opened in the editor: its name fills the draft's name input.
  await expect(page.getByTestId('notetype-name')).toHaveValue('Basic');
  expect(pageErrors).toEqual([]);
});
