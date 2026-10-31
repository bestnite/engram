import { test, expect } from '@playwright/test';
import { api, signIn } from './session';

/**
 * The note type page with a stored note type, in a real browser.
 *
 * The page copies the selected type's config out of Svelte state before editing it. That state is a proxy, and only a
 * real browser's structuredClone refuses a proxy: the DOM implementation the client tests run on clones it without
 * complaint, so the failure ("could not load note types" on every deck that has a type) never showed there. This case
 * opens the page against the Go server, signed in through the same API calls a user's browser makes.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL });

test('the note type page opens a deck that has a note type', async ({ page }) => {
  await signIn(page);

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
