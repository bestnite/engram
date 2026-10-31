import { test, expect } from '@playwright/test';
import { api, signIn } from './session';

/**
 * The note editor's shortcut to the note's type, in a real browser.
 *
 * The note type page loads its types itself and must open on the type the shortcut names rather than on the first
 * one; the editor must ask before the navigation discards an unsaved change, and the page's back link must lead to the
 * note again. The client tests render the note type page from fixtures, so only this case runs the fetching page.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL });

test('the note editor opens the note type of the note and comes back to it', async ({ page }) => {
  await signIn(page);
  const deck = await api(page, 'POST', '/api/v1/decks', { name: 'Type shortcut' });
  expect(deck.status).toBe(201);
  const deckId = (deck.json as { id: string }).id;
  // Two types, and the note uses the second, so opening the first type would be visibly wrong.
  const first = await api(page, 'POST', `/api/v1/decks/${deckId}/note-types`, { stock_id: 'anki:basic', name: 'Alpha type' });
  expect(first.status).toBe(201);
  const second = await api(page, 'POST', `/api/v1/decks/${deckId}/note-types`, { stock_id: 'anki:basic', name: 'Zulu type' });
  expect(second.status).toBe(201);
  const created = await api(page, 'POST', `/api/v1/decks/${deckId}/notes`, {
    notes: [{ note_type: (second.json as { id: string }).id, fields: { Front: 'front', Back: 'back' } }],
  });
  expect(created.status).toBe(200);
  const list = await api(page, 'GET', `/api/v1/decks/${deckId}/notes`);
  const noteId = (list.json as { notes: Array<{ id: string }> }).notes[0]!.id;
  const editPath = `/decks/${deckId}/notes/${noteId}/edit`;

  await page.goto(editPath);
  const tags = page.getByTestId('note-tags-editor');
  await expect(tags).toBeVisible();

  // An unsaved change holds the navigation behind a confirmation; cancelling keeps the page and the change.
  await tags.fill('unsaved-tag');
  await page.getByTestId('note-edit-edit-type').click();
  await expect(page.getByTestId('note-edit-leave-dialog')).toBeVisible();
  await page.getByTestId('note-edit-leave-cancel').click();
  await expect(page.getByTestId('note-edit-leave-dialog')).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`${editPath}$`));
  await expect(tags).toHaveValue('unsaved-tag');

  // Confirming leaves for the note type page, opened on the note's own type.
  await page.getByTestId('note-edit-edit-type').click();
  await page.getByTestId('note-edit-leave-confirm').click();
  await expect(page).toHaveURL(/\/note-types\?/);
  await expect(page.getByTestId('notetype-name')).toHaveValue('Zulu type');

  // The back link returns to the note, which shows its stored content again.
  await page.getByTestId('notetype-back').click();
  await expect(page).toHaveURL(new RegExp(`${editPath}$`));
  await expect(page.getByTestId('note-tags-editor')).toHaveValue('');
});
