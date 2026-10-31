import { test, expect, type Frame, type Page } from '@playwright/test';
import { api, signIn } from './session';

/**
 * Field media in the rich-text editor, in a real browser.
 *
 * A field stores media by deck-scoped name, and the editing frame is a sandboxed, opaque-origin document without the
 * session cookie. Only a real browser applies the frame's base element to an image and withholds the cookie from the
 * frame's requests, so this suite checks that a stored image and a freshly uploaded one both load inside the frame.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL });

// A 1x1 PNG: a real image, so a loaded <img> reports a natural width of 1.
const PNG_BASE64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

/** Creates a deck with Anki's basic note type and returns their ids. */
async function seedDeck(page: Page, name: string): Promise<{ deckId: string; typeId: string }> {
  const deck = await api(page, 'POST', '/api/v1/decks', { name });
  expect(deck.status).toBe(201);
  const deckId = (deck.json as { id: string }).id;
  const type = await api(page, 'POST', `/api/v1/decks/${deckId}/note-types`, { stock_id: 'anki:basic' });
  expect(type.status).toBe(201);
  return { deckId, typeId: (type.json as { id: string }).id };
}

/** The editing frame of the first rich-text field, once it is shown. */
async function firstEditFrame(page: Page): Promise<Frame> {
  await expect(page.locator('iframe[src^="/edit-frame/"]').first()).toBeVisible();
  const frame = page.frames().find((candidate) => candidate.url().includes('/edit-frame/'));
  expect(frame).toBeTruthy();
  return frame!;
}

/** Waits until the frame shows an image whose src attribute is name, and returns how it loaded. */
async function loadedImage(frame: Frame, name: string): Promise<{ path: string; width: number }> {
  const image = frame.locator(`#editor img[src="${name}"]`);
  await expect(image).toHaveCount(1);
  await expect.poll(() => image.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0)).toBe(true);
  return image.evaluate((img: HTMLImageElement) => ({ path: new URL(img.src).pathname, width: img.naturalWidth }));
}

test('a stored image in a field loads inside the editing frame', async ({ page }) => {
  await signIn(page);
  const { deckId, typeId } = await seedDeck(page, 'Stored field image');
  const upload = await page.evaluate(
    async ({ deckId, png }) => {
      const session = await (await fetch('/api/v1/auth/session')).json();
      const bytes = Uint8Array.from(atob(png), (char) => char.charCodeAt(0));
      const form = new FormData();
      form.append('file', new File([bytes], 'map.png', { type: 'image/png' }));
      const res = await fetch(`/decks/${encodeURIComponent(deckId)}/media`, {
        method: 'POST',
        body: form,
        headers: { 'X-CSRF-Token': session.csrf_token ?? '' },
      });
      return { status: res.status, json: await res.json() };
    },
    { deckId, png: PNG_BASE64 },
  );
  expect(upload.status).toBe(201);
  const name = (upload.json as { name: string }).name;
  const created = await api(page, 'POST', `/api/v1/decks/${deckId}/notes`, {
    notes: [{ note_type: typeId, fields: { Front: `<img src="${name}">`, Back: 'back' } }],
  });
  expect(created.status).toBe(200);
  const list = await api(page, 'GET', `/api/v1/decks/${deckId}/notes`);
  const noteId = (list.json as { notes: Array<{ id: string }> }).notes[0]!.id;

  await page.goto(`/decks/${deckId}/notes/${noteId}/edit`);
  const loaded = await loadedImage(await firstEditFrame(page), name);
  expect(loaded.path).toMatch(new RegExp(`^/fm/[^/]+/${name}$`));
  expect(loaded.width).toBe(1);
});

test('an image uploaded from the toolbar loads inside the editing frame before the note is saved', async ({ page }) => {
  await signIn(page);
  const { deckId } = await seedDeck(page, 'Uploaded field image');
  await page.goto(`/decks/${deckId}/notes/new`);
  const frame = await firstEditFrame(page);

  const uploaded = page.waitForResponse((res) => res.url().endsWith(`/decks/${deckId}/media`) && res.request().method() === 'POST');
  await page
    .locator('[data-testid$="-file-input"]')
    .first()
    .setInputFiles({ name: 'map.png', mimeType: 'image/png', buffer: Buffer.from(PNG_BASE64, 'base64') });
  const name = ((await (await uploaded).json()) as { name: string }).name;
  expect(name).toMatch(/^[0-9a-f]{64}\.png$/);

  const loaded = await loadedImage(frame, name);
  expect(loaded.path).toMatch(new RegExp(`^/fm/[^/]+/${name}$`));
  expect(loaded.width).toBe(1);
});
