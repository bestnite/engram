import { test, expect } from '@playwright/test';
import { api, signIn } from './session';

/**
 * The rich-text field on a dark page, in a real browser.
 *
 * The field's iframe document starts with a light color-scheme and learns the page theme only by message. A browser
 * paints a frame whose color-scheme differs from the page's with an opaque canvas, so before the fix a dark page showed a
 * white editing box until the theme arrived. Only a real browser applies that canvas rule, and only a real frame answers
 * the theme, so this case checks both halves: every frame is hidden when it is inserted, and it is shown once its
 * document carries the dark scheme.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL, colorScheme: 'dark' });

test('a rich-text field on a dark page is hidden until its frame is dark', async ({ page }) => {
  // Records, for every editing frame, whether it was hidden at the moment it entered the page.
  await page.addInitScript(() => {
    const seen: boolean[] = [];
    (window as unknown as { editFramesHiddenOnInsert: boolean[] }).editFramesHiddenOnInsert = seen;
    new MutationObserver((records) => {
      for (const record of records) {
        for (const node of record.addedNodes) {
          if (!(node instanceof Element)) continue;
          const frames = node.matches('iframe[src^="/edit-frame/"]') ? [node] : [...node.querySelectorAll('iframe[src^="/edit-frame/"]')];
          for (const frame of frames) seen.push(getComputedStyle(frame).visibility === 'hidden');
        }
      }
    }).observe(document, { childList: true, subtree: true });
  });

  await signIn(page);
  const deck = await api(page, 'POST', '/api/v1/decks', { name: 'Dark editor' });
  expect(deck.status).toBe(201);
  const deckId = (deck.json as { id: string }).id;
  const created = await api(page, 'POST', `/api/v1/decks/${deckId}/note-types`, { stock_id: 'engram:basic' });
  expect(created.status).toBe(201);

  await page.goto(`/decks/${deckId}/notes/new`);
  await expect(page.locator('html')).toHaveClass(/\bdark\b/);

  const frames = page.locator('iframe[src^="/edit-frame/"]');
  await expect(frames.first()).toBeVisible();
  const count = await frames.count();
  expect(count).toBeGreaterThan(0);
  for (let i = 0; i < count; i += 1) await expect(frames.nth(i)).toBeVisible();

  const hiddenOnInsert = await page.evaluate(() => (window as unknown as { editFramesHiddenOnInsert: boolean[] }).editFramesHiddenOnInsert);
  expect(hiddenOnInsert.length).toBeGreaterThanOrEqual(count);
  expect(hiddenOnInsert.every(Boolean)).toBe(true);

  const schemes = await Promise.all(
    page.frames()
      .filter((frame) => frame.url().includes('/edit-frame/'))
      .map((frame) => frame.evaluate(() => document.documentElement.style.colorScheme)),
  );
  expect(schemes.length).toBe(count);
  expect(schemes.every((scheme) => scheme === 'dark')).toBe(true);
});
