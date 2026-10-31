import { test, expect } from '@playwright/test';
import { api, signIn } from './session';

/**
 * The rendered cards of the note type help's worked examples, in a real browser against the real server.
 *
 * Each example is sent as an unsaved note type with its sample note, so this suite proves that the server accepts every
 * example the help prints, and that the card generation rules the examples explain hold: a cloze with two numbers makes
 * two cards, and a filled Add Reverse makes the second card.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL, viewport: { width: 1280, height: 900 } });

const EXAMPLES: Array<{ id: string; cards: number }> = [
  { id: 'basic', cards: 1 },
  { id: 'reversed', cards: 2 },
  { id: 'optional_reverse', cards: 2 },
  { id: 'cloze', cards: 2 },
  { id: 'type_in', cards: 1 },
  { id: 'reading', cards: 1 },
  { id: 'hint', cards: 1 },
  { id: 'tts', cards: 1 },
  { id: 'choice', cards: 1 },
  { id: 'style', cards: 1 },
];

test('every worked example renders its cards through the server', async ({ page }) => {
  await signIn(page);
  const deck = await api(page, 'POST', '/api/v1/decks', { name: 'Help previews' });
  expect(deck.status).toBe(201);
  const deckId = (deck.json as { id: string }).id;
  await page.goto(`/decks/${deckId}/note-types/help`);

  for (const { id, cards } of EXAMPLES) {
    const preview = page.getByTestId(`note-type-help-preview-${id}`);
    await preview.scrollIntoViewIfNeeded();
    await expect(preview, `example ${id}`).toHaveAttribute('data-status', 'ready');
    await expect(preview.locator('iframe[data-card-frame]')).toHaveCount(2);
    if (cards > 1) {
      await expect(preview.getByText(`1 / ${cards}`)).toBeVisible();
    } else {
      await expect(preview.getByTestId('note-type-help-preview-pager')).toHaveCount(0);
    }
  }

  // The sample value reaches the face: the basic example's back shows the answer below the question.
  const basicBack = page.getByTestId('note-type-help-preview-basic').locator('iframe[data-side="answer"]');
  await expect(page.frameLocator(`[data-testid="note-type-help-preview-basic"] iframe[data-side="answer"]`).locator('#answer')).toBeAttached();
  await expect(basicBack).toBeVisible();
});
