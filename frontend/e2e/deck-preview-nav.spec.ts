import { test, expect, type Page } from '@playwright/test';
import { api, signIn } from './session';

/**
 * The deck preview's navigation bar around a card taller than the screen, in a real browser.
 *
 * Only a browser lays out the sticky bar against the window scroll, so this is where it shows whether the four steps stay on
 * screen above a long face, and whether the note steps fold to their icons on a narrow screen and still fit in one row.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL });

const LONG_FRONT = Array.from({ length: 40 }, (_, index) => `<p>question line ${index + 1}</p>`).join('');

/** Creates a deck with three long cards and opens its preview. */
async function previewLongCards(page: Page): Promise<void> {
  const deck = await api(page, 'POST', '/api/v1/decks', { name: 'Long preview' });
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
      templates: [{ id: 't-one', name: 'Card 1', qfmt: '{{Front}}', afmt: '{{FrontSide}}<hr id=answer>{{Back}}' }],
      css: '',
    },
  });
  expect(type.status, JSON.stringify(type.json)).toBe(201);
  const typeId = (type.json as { id: string }).id;
  const notes = Array.from({ length: 3 }, () => ({ note_type: typeId, fields: { Front: LONG_FRONT, Back: 'back' } }));
  const created = await api(page, 'POST', `/api/v1/decks/${deckId}/notes`, { notes });
  expect(created.status).toBe(200);

  await page.goto(`/decks/${deckId}/preview`);
  await expect(page.frameLocator('iframe[data-side="question"]').locator('text=question line 40')).toBeAttached();
}

/** The bottom edge of a test id's box, which must not pass the viewport's bottom edge. */
async function bottomOf(page: Page, testId: string): Promise<number> {
  const box = await page.getByTestId(testId).boundingBox();
  expect(box, testId).not.toBeNull();
  return box!.y + box!.height;
}

test('the navigation bar stays on screen above a long card and steps through the deck', async ({ page }) => {
  const viewport = { width: 1280, height: 700 };
  await page.setViewportSize(viewport);
  await signIn(page);
  await previewLongCards(page);

  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeGreaterThan(viewport.height * 2);
  for (const id of ['preview-note-prev', 'preview-prev', 'preview-next', 'preview-note-next']) {
    expect(await bottomOf(page, id)).toBeLessThanOrEqual(viewport.height);
  }
  await expect(page.getByTestId('preview-nav-position')).toHaveText('1 / 3');
  await expect(page.getByTestId('preview-note-next')).toContainText('Next note');

  await page.getByTestId('preview-next').click();
  await expect(page.getByTestId('preview-nav-position')).toHaveText('2 / 3');
});

test('on a narrow screen every step keeps only its icon and the bar fits in one row', async ({ page }) => {
  const viewport = { width: 360, height: 640 };
  await page.setViewportSize(viewport);
  await signIn(page);
  await previewLongCards(page);

  for (const [id, name] of [
    ['preview-note-prev', 'Previous note'],
    ['preview-prev', 'Previous card'],
    ['preview-next', 'Next card'],
    ['preview-note-next', 'Next note'],
  ]) {
    const step = page.getByTestId(id);
    await expect(step).toHaveAttribute('aria-label', name);
    await expect(step.getByText(name)).toBeHidden();
  }

  // One row: every step shares the bar's vertical centre, and the bar is no wider than the screen.
  const centres = await Promise.all(
    ['preview-note-prev', 'preview-prev', 'preview-next', 'preview-note-next'].map(async (id) => {
      const box = (await page.getByTestId(id).boundingBox())!;
      expect(box.x + box.width, id).toBeLessThanOrEqual(viewport.width);
      return Math.round(box.y + box.height / 2);
    }),
  );
  expect(new Set(centres).size).toBe(1);
  expect(await bottomOf(page, 'preview-nav')).toBeLessThanOrEqual(viewport.height);
});
