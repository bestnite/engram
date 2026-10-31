import { test, expect, type Page } from '@playwright/test';
import { api, signIn } from './session';
import { ANSWER_SCROLL_MARGIN } from '../src/lib/review-scroll';

/**
 * How the review page scrolls around a card taller than the screen, in a real browser.
 *
 * The card frame is as tall as its face and the window scrolls, so only a browser shows whether the action bar stays on
 * screen, whether the reveal lands the face's `#answer` at the top of the viewport (the frame has to reach its new height
 * before the page scrolls), and whether the next card starts at the page top again.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

const VIEWPORT = { width: 1280, height: 700 };
test.use({ baseURL, viewport: VIEWPORT });

// Forty paragraphs make the question alone several screens tall. The answer is a screen tall as well: below a short answer
// the page ends before `#answer` reaches the viewport top, and the browser stops the scroll at the page end.
const LONG_FRONT = Array.from({ length: 40 }, (_, index) => `<p>question line ${index + 1}</p>`).join('');
const LONG_BACK = Array.from({ length: 30 }, (_, index) => `<p>answer line ${index + 1}</p>`).join('');

/** Creates a deck with two long cards whose answer side repeats the question above `#answer`, and opens its review. */
async function reviewLongCards(page: Page): Promise<void> {
  const deck = await api(page, 'POST', '/api/v1/decks', { name: 'Long cards' });
  expect(deck.status).toBe(201);
  const deckId = (deck.json as { id: string }).id;
  const type = await api(page, 'POST', `/api/v1/decks/${deckId}/note-types`, {
    name: 'Long',
    config: {
      kind: 'normal',
      fields: [
        { id: 'f-front', name: 'Front' },
        { id: 'f-back', name: 'Back' },
      ],
      templates: [{ id: 't-one', name: 'Card 1', qfmt: '{{Front}}', afmt: '{{FrontSide}}<hr id=answer><div id="back">{{Back}}</div>' }],
      css: '',
    },
  });
  expect(type.status, JSON.stringify(type.json)).toBe(201);
  const typeId = (type.json as { id: string }).id;
  const created = await api(page, 'POST', `/api/v1/decks/${deckId}/notes`, {
    notes: [
      { note_type: typeId, fields: { Front: LONG_FRONT, Back: LONG_BACK } },
      { note_type: typeId, fields: { Front: LONG_FRONT, Back: LONG_BACK } },
    ],
  });
  expect(created.status).toBe(200);

  await page.goto(`/review?deck=${deckId}`);
  await expect(page.frameLocator('iframe[data-side="question"]').locator('text=question line 40')).toBeAttached();
}

test('a long card keeps its buttons on screen, reveals at #answer and the next card starts at the top', async ({ page }) => {
  await signIn(page);
  await reviewLongCards(page);

  // The page is taller than the screen, and the reveal button is still inside the viewport.
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeGreaterThan(VIEWPORT.height * 2);
  const reveal = await page.getByTestId('review-reveal').boundingBox();
  expect(reveal).not.toBeNull();
  expect(reveal!.y + reveal!.height).toBeLessThanOrEqual(VIEWPORT.height);

  await page.getByTestId('review-reveal').click();
  const answer = page.frameLocator('iframe[data-side="answer"]').locator('#answer');
  await expect(answer).toBeAttached();
  // The face's #answer lands at the top of the viewport, below the scroll margin.
  await expect.poll(async () => Math.round((await answer.boundingBox())?.y ?? -1)).toBe(ANSWER_SCROLL_MARGIN);
  expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
  // The rating buttons are on screen without scrolling further.
  const good = await page.locator('[data-rating="3"]').boundingBox();
  expect(good!.y + good!.height).toBeLessThanOrEqual(VIEWPORT.height);

  await page.locator('[data-rating="3"]').click();
  await expect(page.getByTestId('review-reveal')).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
});

test('the learner can turn the answer scroll off and the choice survives a reload', async ({ page }) => {
  await signIn(page);
  await reviewLongCards(page);

  const toggle = page.getByTestId('review-scroll-toggle');
  await expect(toggle).toHaveAttribute('aria-checked', 'true');
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-checked', 'false');

  await page.getByTestId('review-reveal').click();
  const answer = page.frameLocator('iframe[data-side="answer"]').locator('#answer');
  await expect(answer).toBeAttached();
  // The frame reports its new height asynchronously, and only that report moves the page; waiting past it is what makes
  // "the page stayed still" a real assertion rather than a race the click simply won.
  await page.waitForTimeout(500);
  expect(await page.evaluate(() => window.scrollY)).toBe(0);

  await page.reload();
  await expect(page.getByTestId('review-scroll-toggle')).toHaveAttribute('aria-checked', 'false');
});
