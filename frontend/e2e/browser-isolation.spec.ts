import { expect, test } from '@playwright/test';

/**
 * Proves the card-frame sandbox in a real browser (M17-4).
 *
 * The frame is the only security boundary for unsanitised card HTML, and CSP, sandbox and origin rules are enforced by
 * the browser alone. The test therefore loads the real frame route and has a card's own inline script run every escape
 * attempt the sandbox claims to stop, plus one https image load the policy must still allow. The harness page is
 * fulfilled for a same-origin URL through `page.route`, which gives it the server's origin without adding a product
 * route; the frame still rejects any other embedder through `frame-ancestors 'self'`.
 *
 * Every probe is reported by the card script with a stable id and the outcome it observed, and the Playwright side
 * asserts each one. The identifiers are:
 *
 *   - `cookie`: `document.cookie` cannot reveal the parent's cookies; Chromium throws a `SecurityError` in an
 *     opaque-origin sandbox (an empty string is accepted as the equivalent blocked outcome);
 *   - `fetch`: `fetch('/api/v1/decks')` rejects and a `connect-src` CSP violation arrives;
 *   - `top-location`: `top.location = ...` leaves both the parent page and the frame on their URLs;
 *   - `form-submit`: submitting a form is blocked and reaches no server. With `sandbox="allow-scripts"` only, the
 *     missing `allow-forms` permission stops the submission before CSP's `form-action 'none'` is consulted, so no
 *     `securitypolicyviolation` is observable; the CSP remains the second layer of defence;
 *   - `window-open`: `window.open` returns `null`;
 *   - `parent-document`: `parent.document` throws a `SecurityError`;
 *   - `image` / `image-csp`: an https image loads to one pixel and no `img-src` violation arrives.
 */

const baseURL = process.env.ENGRAM_E2E_BASE_URL;
if (!baseURL) {
  throw new Error(
    'ENGRAM_E2E_BASE_URL is not set. Run the suite with `npm run test:e2e`, which starts the Go server in the Playwright global setup and passes its address to the workers.',
  );
}

test.use({ baseURL });

const HARNESS_PATH = '/__e2e_harness';
const SUBMIT_PATH = '/__e2e_submit';
const FRAME_URL = `${baseURL}/card-frame/e2e`;

const EDIT_HARNESS_PATH = '/__e2e_edit_harness';
const EDIT_FRAME_URL = `${baseURL}/edit-frame/e2e`;
const MISSING_IMAGE_PATH = '/__e2e_missing.png';

/** A 1x1 transparent PNG; the route serves it so the image assertion does not depend on the network. */
const PIXEL_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
);

interface Probe {
  id: string;
  ok: boolean;
  detail: string;
}

/**
 * The harness page: same origin as the frame, one sandboxed iframe, and a message sink for the probes the card posts.
 * It posts the card's face only after the frame signals `ready`, exactly like the product's CardFrame component.
 */
const HARNESS_HTML = `<!doctype html>
<html lang="en">
  <head><meta charset="utf-8" /><title>card frame sandbox harness</title></head>
  <body>
    <iframe id="frame" sandbox="allow-scripts" allow="autoplay" src="/card-frame/e2e"></iframe>
    <script>
      window.__probes = [];
      window.__frameReady = false;
      window.__showCard = function (html) {
        document.getElementById('frame').contentWindow.postMessage(
          { type: 'show', html: html, css: '', bodyClass: '', side: 'front', mediaBase: '', autoplay: false, ordinal: 0 },
          '*',
        );
      };
      var frame = document.getElementById('frame');
      window.addEventListener('message', function (event) {
        if (event.source !== frame.contentWindow) return;
        var data = event.data;
        if (!data || typeof data !== 'object') return;
        if (data.type === 'ready') {
          window.__frameReady = true;
          return;
        }
        if (data.type === 'probe') {
          window.__probes.push({ id: data.id, ok: data.ok, detail: data.detail });
        }
      });
    </script>
  </body>
</html>`;

/**
 * The card's inline script, run by the frame runtime exactly like authored card content. It is a plain string rather
 * than a function serialised with `toString()`, so its syntax is not at the mercy of the transpiler.
 */
const CARD_SCRIPT = String.raw`
(async function () {
  var sleep = function (ms) { return new Promise(function (done) { setTimeout(done, ms); }); };
  var post = function (id, ok, detail) {
    parent.postMessage({ type: 'probe', id: id, ok: ok, detail: String(detail) }, '*');
  };
  var violations = [];
  document.addEventListener('securitypolicyviolation', function (event) {
    violations.push({ directive: event.violatedDirective, effective: event.effectiveDirective, blockedURI: event.blockedURI });
  });
  var violationNamed = function (fragment) {
    var hit = violations.find(function (violation) {
      return (violation.directive + ' ' + violation.effective).indexOf(fragment) !== -1;
    });
    return hit ? hit.directive + ' blocked ' + hit.blockedURI : '';
  };

  // 1. Cookies: the opaque origin of the sandboxed frame cannot read the parent's cookies. The exact browser
  // behaviour is a thrown SecurityError, not an empty string; either outcome is recorded as blocked.
  var cookie = '';
  var cookieError = '';
  try { cookie = document.cookie; } catch (error) { cookieError = error.name; }
  var cookieBlocked = cookieError === 'SecurityError' || (cookieError === '' && cookie === '');
  post('cookie', cookieBlocked,
    cookieError ? 'threw ' + cookieError : 'value=' + JSON.stringify(cookie));

  // 2. parent.document: the harness is cross-origin to the sandboxed frame, so the read must throw.
  var parentDocument = '';
  try { void parent.document; parentDocument = 'read succeeded'; } catch (error) { parentDocument = error.name; }
  post('parent-document', parentDocument === 'SecurityError', parentDocument);

  // 3. window.open: the sandbox has no allow-popups token.
  var popped = 'not attempted';
  try { popped = window.open('https://example.com/popup'); } catch (error) { popped = 'threw ' + error.name; }
  post('window-open', popped === null, 'returned ' + String(popped));

  // 4. fetch: connect-src only names the origin's /fm/ paths.
  var fetchOutcome = '';
  var rejected = false;
  try { await fetch('/api/v1/decks'); fetchOutcome = 'resolved'; } catch (error) { rejected = true; fetchOutcome = 'rejected ' + error.name; }
  await sleep(150);
  var connectViolation = violationNamed('connect-src');
  post('fetch', rejected && connectViolation !== '', fetchOutcome + '; violation=' + (connectViolation || 'none'));

  // 5. top.location: the sandbox has no allow-top-navigation token, so both documents keep their URLs.
  var topError = '';
  try { top.location = 'https://example.com/escape'; } catch (error) { topError = error.name; }
  await sleep(300);
  var stillOnFrame = location.href.indexOf('/card-frame/e2e') !== -1;
  post('top-location', stillOnFrame,
    'location=' + location.href + (topError ? '; threw ' + topError : ''));

  // 6. A form submission. With sandbox="allow-scripts" only, the missing allow-forms permission stops the submit
  // before CSP form-action 'none' is consulted, so no securitypolicyviolation is dispatched. The invariant is that
  // nothing is submitted: the frame keeps its URL here, and the Playwright side asserts the parent URL is unchanged
  // and no request reaches /__e2e_submit. form-action 'none' remains the second layer of defence.
  var form = document.createElement('form');
  form.action = '/__e2e_submit';
  form.method = 'post';
  var field = document.createElement('input');
  field.name = 'probe';
  field.value = '1';
  form.appendChild(field);
  document.body.appendChild(form);
  var submitError = '';
  try { form.submit(); } catch (error) { submitError = error.name; }
  await sleep(400);
  var stillOnFrameAfterSubmit = location.href.indexOf('/card-frame/e2e') !== -1;
  var formViolation = violationNamed('form-action');
  post('form-submit', stillOnFrameAfterSubmit,
    'location=' + location.href + '; form-action violation=' + (formViolation || 'none') +
    (submitError ? '; submit threw ' + submitError : ''));

  // 7. An https image: img-src allows https:, and the Playwright route fulfils a 1x1 pixel.
  var image = document.createElement('img');
  var imageSettled = new Promise(function (done) {
    image.addEventListener('load', function () { done(true); }, { once: true });
    image.addEventListener('error', function () { done(false); }, { once: true });
  });
  image.src = 'https://example.com/pixel.png';
  document.body.appendChild(image);
  var loaded = await imageSettled;
  await sleep(150);
  var imageViolation = violationNamed('img-src');
  post('image', loaded && image.complete && image.naturalWidth === 1,
    'loaded=' + loaded + ' complete=' + image.complete + ' naturalWidth=' + image.naturalWidth);
  post('image-csp', imageViolation === '', imageViolation || 'no img-src violation');

  post('done', true, 'all probes reported');
})().catch(function (error) {
  parent.postMessage({ type: 'probe', id: 'fatal', ok: false, detail: String((error && error.stack) || error) }, '*');
});
`;

const CARD_HTML = `<div id="sandbox-probe-card">sandbox probe</div><script>${CARD_SCRIPT}</script>`;

const PROBE_IDS = [
  'cookie',
  'fetch',
  'top-location',
  'form-submit',
  'window-open',
  'parent-document',
  'image',
  'image-csp',
] as const;

/**
 * The harness page for the editing frame: one sandboxed iframe on `/edit-frame/e2e` and the same `load` message the
 * parent page's `RichTextField` sends. The runtime only accepts messages from its parent window, so the harness must
 * be the embedder.
 */
const EDIT_HARNESS_HTML = `<!doctype html>
<html lang="en">
  <head><meta charset="utf-8" /><title>edit frame csp harness</title></head>
  <body>
    <iframe id="frame" sandbox="allow-scripts" src="/edit-frame/e2e"></iframe>
    <script>
      window.__probes = [];
      window.__frameReady = false;
      window.__loadField = function (html) {
        document.getElementById('frame').contentWindow.postMessage(
          { type: 'load', html: html, plainText: false, rtl: false, font: '', size: 0, description: '', dark: false, source: false },
          '*',
        );
      };
      var frame = document.getElementById('frame');
      window.addEventListener('message', function (event) {
        if (event.source !== frame.contentWindow) return;
        var data = event.data;
        if (!data || typeof data !== 'object') return;
        if (data.type === 'ready') {
          window.__frameReady = true;
          return;
        }
        if (data.type === 'probe') {
          window.__probes.push({ id: data.id, detail: data.detail });
        }
      });
    </script>
  </body>
</html>`;

/**
 * A field value with an event handler attribute, the shape a shared deck can carry. The image cannot load, so the
 * browser would dispatch `error`; the handler records that it ran and posts a probe the parent page would receive. Both
 * are observable, so the assertion distinguishes "the handler was blocked" from "the handler never had a chance".
 */
const FIELD_WITH_HANDLER = `<p id="field-probe">field value</p><img id="handler-probe" src="${MISSING_IMAGE_PATH}" onerror="window.__handlerRan=true;parent.postMessage({type:'probe',id:'onerror',detail:'inline handler ran'},'*')">`;

test('the card frame sandbox blocks every escape attempt and still loads https media', async ({ page }) => {
  const harnessURL = new URL(HARNESS_PATH, baseURL).toString();
  const unexpectedRequests: string[] = [];
  const submitRequests: string[] = [];
  const pixelRequests: string[] = [];
  const pageErrors: string[] = [];
  const failedRequests: string[] = [];
  const consoleErrors: string[] = [];

  page.on('pageerror', (error) => pageErrors.push(error.message));
  page.on('requestfailed', (request) =>
    failedRequests.push(`${request.url()} ${request.failure()?.errorText ?? ''}`),
  );
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });

  // The harness has to share the frame's origin: frame-ancestors 'self' rejects every other embedder.
  await page.route(`**${HARNESS_PATH}`, (route) =>
    route.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: HARNESS_HTML }),
  );
  // The escape and popup URLs must never be requested; only the pixel is served, and only from the placeholder domain.
  await page.route('https://example.com/**', async (route) => {
    const url = route.request().url();
    if (url === 'https://example.com/pixel.png') {
      pixelRequests.push(url);
      await route.fulfill({ status: 200, contentType: 'image/png', body: PIXEL_PNG });
      return;
    }
    unexpectedRequests.push(url);
    await route.abort();
  });
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (url.pathname === SUBMIT_PATH) submitRequests.push(request.url());
  });

  await page.goto(harnessURL);
  try {
    await page.waitForFunction(
      () => (window as unknown as { __frameReady?: boolean }).__frameReady === true,
      undefined,
      { timeout: 20_000 },
    );
  } catch {
    // A frame document that never boots cannot report any probe; say what the browser saw instead of hiding it behind
    // a bare timeout.
    throw new Error(
      'the card frame never signalled ready, so no probe could run. ' +
        `frame URLs: ${JSON.stringify(page.frames().map((frame) => frame.url()))}; ` +
        `page errors: ${JSON.stringify(pageErrors)}; ` +
        `failed requests: ${JSON.stringify(failedRequests)}; ` +
        `console errors: ${JSON.stringify(consoleErrors)}`,
    );
  }
  await page.evaluate(
    (html) => (window as unknown as { __showCard(html: string): void }).__showCard(html),
    CARD_HTML,
  );

  let probes: Probe[];
  try {
    await page.waitForFunction(
      () =>
        ((window as unknown as { __probes?: Probe[] }).__probes ?? []).some((probe) => probe.id === 'done'),
      undefined,
      { timeout: 20_000 },
    );
    probes = await page.evaluate(() => (window as unknown as { __probes: Probe[] }).__probes);
  } catch {
    // Without `done` the frame may have navigated away, which is itself the failure worth reporting.
    const seen = await page
      .evaluate(() => (window as unknown as { __probes?: Probe[] }).__probes ?? [])
      .catch(() => [] as Probe[]);
    throw new Error(
      `the card script never reported "done"; probes observed: ${JSON.stringify(seen)}; ` +
        `page URL: ${page.url()} (expected ${harnessURL})`,
    );
  }

  const byId = new Map(probes.map((probe) => [probe.id, probe]));
  const fatal = byId.get('fatal');
  expect.soft(fatal?.ok ?? true, `the card script threw before finishing: ${fatal?.detail ?? ''}`).toBe(true);

  for (const id of PROBE_IDS) {
    const probe = byId.get(id);
    const message = probe
      ? `probe "${id}" did not hold: ${probe.detail}`
      : `probe "${id}" was never reported by the card script; probes observed: ${JSON.stringify(probes)}`;
    expect.soft(probe?.ok, message).toBe(true);
  }

  // The parent page must not have navigated: `top.location = ...` is blocked by the sandbox.
  expect.soft(page.url(), 'the parent page navigated, so `top.location` was not blocked').toBe(harnessURL);
  const frame = page.frames().find((candidate) => candidate.url().includes('/card-frame/'));
  expect.soft(frame, 'the card frame no longer exists; a navigation replaced it').toBeDefined();
  expect
    .soft(frame?.url(), 'the card frame navigated away from the frame document')
    .toBe(FRAME_URL);

  // No escape or popup URL may have been requested, and the form must not have reached the server.
  expect.soft(unexpectedRequests, 'the sandbox requested an escape or popup URL').toEqual([]);
  expect.soft(submitRequests, 'the form submission reached the server').toEqual([]);
  expect.soft(pixelRequests, 'the https image was never requested').toHaveLength(1);
});

/**
 * Proves the editing frame runs no inline script (M17-42).
 *
 * The editing frame writes the field value with `innerHTML`, so an event handler attribute in a shared deck's field
 * would execute inside the frame if its `script-src` allowed inline script. The narrowed policy allows only the
 * same-origin runtime module, so the handler must stay inert. The test loads a real field value containing
 * `<img onerror=...>`, waits until the image has failed (which is what dispatches `error`), and then asserts that the
 * attribute is still present but its handler never ran and never posted its probe.
 */
test('an inline event handler in a loaded field value does not run in the editing frame', async ({ page }) => {
  const harnessURL = new URL(EDIT_HARNESS_PATH, baseURL).toString();

  await page.route(`**${EDIT_HARNESS_PATH}`, (route) =>
    route.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: EDIT_HARNESS_HTML }),
  );
  // The image must fail, because a failing image is what dispatches the `error` event the handler is attached to.
  await page.route(`**${MISSING_IMAGE_PATH}`, (route) =>
    route.fulfill({ status: 404, contentType: 'text/plain; charset=utf-8', body: 'not an image' }),
  );

  const pageErrors: string[] = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));

  await page.goto(harnessURL);
  await page.waitForFunction(
    () => (window as unknown as { __frameReady?: boolean }).__frameReady === true,
    undefined,
    { timeout: 20_000 },
  );

  const frame = page.frames().find((candidate) => candidate.url().includes('/edit-frame/'));
  expect(frame, `the editing frame was not found; frames: ${JSON.stringify(page.frames().map((f) => f.url()))}`).toBeDefined();
  expect(frame?.url()).toBe(EDIT_FRAME_URL);

  await page.evaluate(
    (html) => (window as unknown as { __loadField(html: string): void }).__loadField(html),
    FIELD_WITH_HANDLER,
  );

  // The runtime renders the field only after `load`, so the field marker proves the value reached the frame.
  await frame!.waitForFunction(() => document.querySelector('#field-probe') !== null, undefined, { timeout: 10_000 });
  await frame!.waitForFunction(
    () => {
      const image = document.querySelector('#handler-probe');
      return image instanceof HTMLImageElement && image.complete;
    },
    undefined,
    { timeout: 10_000 },
  );
  // The image is complete before the error event is dispatched; leave room for it and for a postMessage that was
  // allowed to travel.
  await page.waitForTimeout(500);

  const present = await frame!.evaluate(() => {
    const image = document.querySelector('#handler-probe');
    return image instanceof HTMLImageElement && image.getAttribute('onerror') !== null;
  });
  expect(present, 'the handler attribute was not rendered, so the assertion would not prove it was blocked').toBe(true);

  const handlerRan = await frame!.evaluate(
    () => (window as unknown as { __handlerRan?: boolean }).__handlerRan === true,
  );
  expect(handlerRan, 'the inline onerror handler ran inside the editing frame').toBe(false);

  const probes = await page.evaluate(() => (window as unknown as { __probes: Array<{ id: string; detail: string }> }).__probes);
  expect(probes, 'the inline handler posted a message to the parent page').toEqual([]);
  // A blocked handler is not a page error: the frame stays healthy and keeps its document.
  expect(pageErrors).toEqual([]);
  expect(frame!.url()).toBe(EDIT_FRAME_URL);
});
