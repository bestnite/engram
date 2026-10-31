import { test, expect } from '@playwright/test';
import { api, signIn } from './session';

/**
 * A card face with a speech tag, reviewed in a real browser.
 *
 * The review page keeps the face's audio tag list in Svelte state, which is a proxy. Posting a proxy to the frame fails
 * with DataCloneError in a browser, so the frame never received the face and every card with audio or speech showed
 * blank. The DOM implementation of the client tests clones a proxy without complaint, so only a browser shows it.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL });

test('a card with a speech tag shows its face in review', async ({ page }) => {
  await signIn(page);
  const deck = await api(page, 'POST', '/api/v1/decks', { name: 'Speech review' });
  expect(deck.status).toBe(201);
  const deckId = (deck.json as { id: string }).id;
  const type = await api(page, 'POST', `/api/v1/decks/${deckId}/note-types`, { stock_id: 'anki:basic' });
  expect(type.status).toBe(201);
  const created = await api(page, 'POST', `/api/v1/decks/${deckId}/notes`, {
    notes: [
      {
        note_type: (type.json as { id: string }).id,
        fields: { Front: 'spoken-front[anki:tts lang=en_US]spoken-front[/anki:tts]', Back: 'written-back' },
      },
    ],
  });
  expect(created.status).toBe(200);

  const pageErrors: string[] = [];
  page.on('pageerror', (error) => pageErrors.push(String(error)));
  await page.goto(`/review?deck=${deckId}`);

  const front = page.frameLocator('iframe[data-card-frame]').first();
  await expect(front.locator('#qa')).toContainText('spoken-front');
  expect(pageErrors.filter((message) => message.includes('DataCloneError'))).toEqual([]);
});
