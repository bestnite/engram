import { test, expect, type Frame, type Page } from '@playwright/test';
import { api, signIn } from './session';

/**
 * How a card face lays out a deck's HTML and CSS on the review page, in a real browser.
 *
 * The frame loads the application stylesheet, and the review page adds its own type scale. Only a real browser computes
 * the cascade, so this suite checks that the note type's CSS outranks the engram defaults, that the newlines between a
 * template's tags take no space, and that card content keeps the browser defaults Tailwind's preflight would remove.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL, viewport: { width: 1280, height: 900 } });

// A 1x1 PNG; the template sizes it with width and height attributes.
const PNG_BASE64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

// The front template spreads its blocks over several lines and indents them, as deck templates usually do.
const FRONT = `<div id="first">first</div>


  <div id="second">second</div>
  <div id="picture"><img id="image" src="{{Picture}}" width="120" height="60"></div>
  <ul id="list"><li>item</li></ul>`;

/** Creates a deck whose one note uses a type with the given CSS, and opens its review. */
async function reviewWithCSS(page: Page, deckName: string, css: string): Promise<Frame> {
  const deck = await api(page, 'POST', '/api/v1/decks', { name: deckName });
  expect(deck.status).toBe(201);
  const deckId = (deck.json as { id: string }).id;
  const upload = await page.evaluate(
    async ({ deckId, png }) => {
      const session = await (await fetch('/api/v1/auth/session')).json();
      const bytes = Uint8Array.from(atob(png), (char) => char.charCodeAt(0));
      const form = new FormData();
      form.append('file', new File([bytes], 'dot.png', { type: 'image/png' }));
      const res = await fetch(`/decks/${encodeURIComponent(deckId)}/media`, {
        method: 'POST',
        body: form,
        headers: { 'X-CSRF-Token': session.csrf_token ?? '' },
      });
      return res.json();
    },
    { deckId, png: PNG_BASE64 },
  );
  const type = await api(page, 'POST', `/api/v1/decks/${deckId}/note-types`, {
    name: 'Face style',
    config: {
      kind: 'normal',
      fields: [
        { id: 'f-picture', name: 'Picture' },
        { id: 'f-back', name: 'Back' },
      ],
      templates: [{ id: 't-one', name: 'Card 1', qfmt: FRONT, afmt: '{{FrontSide}}<hr id=answer>{{Back}}' }],
      css,
    },
  });
  expect(type.status, JSON.stringify(type.json)).toBe(201);
  const created = await api(page, 'POST', `/api/v1/decks/${deckId}/notes`, {
    notes: [{ note_type: (type.json as { id: string }).id, fields: { Picture: (upload as { name: string }).name, Back: 'back' } }],
  });
  expect(created.status).toBe(200);

  await page.goto(`/review?deck=${deckId}`);
  await expect(page.frameLocator('iframe[data-card-frame]').first().locator('#second')).toBeVisible();
  const frame = page.frames().find((candidate) => candidate.url().includes('/card-frame/'));
  expect(frame).toBeTruthy();
  return frame!;
}

test('a note type rule outranks the engram defaults and card content keeps its layout', async ({ page }) => {
  await signIn(page);
  const frame = await reviewWithCSS(page, 'Face style with CSS', '.card { font-size: 16px; text-align: center; }');
  const layout = await frame.evaluate(() => {
    const box = (id: string) => document.getElementById(id)!.getBoundingClientRect();
    const image = document.getElementById('image')!;
    return {
      bodyFontSize: getComputedStyle(document.body).fontSize,
      qaFontSize: getComputedStyle(document.getElementById('qa')!).fontSize,
      gap: box('second').top - box('first').bottom,
      imageDisplay: getComputedStyle(image).display,
      imageCentre: box('image').left + box('image').width / 2,
      pictureCentre: box('picture').left + box('picture').width / 2,
      listStyle: getComputedStyle(document.getElementById('list')!).listStyleType,
    };
  });
  // The note type's size reaches the content: the review page's default does not override it on the content root.
  expect.soft(layout.bodyFontSize).toBe('16px');
  expect.soft(layout.qaFontSize).toBe('16px');
  // Three newlines and an indentation between two blocks are not content.
  expect.soft(layout.gap).toBe(0);
  // An image stays inline, so the note type's text-align centres it.
  expect.soft(layout.imageDisplay).toBe('inline');
  expect.soft(Math.abs(layout.imageCentre - layout.pictureCentre)).toBeLessThan(1);
  expect.soft(layout.listStyle).toBe('disc');
});

test('without a note type size the engram default applies, and a note type may still make images blocks', async ({ page }) => {
  await signIn(page);
  const frame = await reviewWithCSS(page, 'Face style defaults', '.card img { display: block; }');
  const style = await frame.evaluate(() => ({
    qaFontSize: getComputedStyle(document.getElementById('qa')!).fontSize,
    imageDisplay: getComputedStyle(document.getElementById('image')!).display,
  }));
  // The review front's default at this width is 1.5rem.
  expect(style.qaFontSize).toBe('24px');
  expect(style.imageDisplay).toBe('block');
});
