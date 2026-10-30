/**
 * MathJax loading and typesetting on the SPA side.
 *
 * MathJax 3 is a self-hosted static asset (`internal/web/static/js/mathjax/tex-chtml-full.js`, embedded with go:embed into
 * the binary, no public CDN). Its content-hashed URL is unknown at Vite build time, so on the Go side, into the SPA entry
 * `index.html`'s <head>, `<meta name="engram-mathjax" content="...">` is injected to hand the URL to the frontend;
 * this module reads that meta and loads it with a dynamic `<script src>`. The CSP's `script-src 'self'` already allows same-origin
 * external scripts, so neither an inline config script nor nonce / 'unsafe-inline' is needed.
 *
 * Typesetting boundary: typeset is called only on "HTML containers returned sanitized by the server", never on the whole page - a
 * note field's HTML is edited inside the sandboxed editing frame and must never pass through a parent HTML sink or be
 * interpreted by the parent's MathJax.
 */

const META_NAME = 'engram-mathjax';

/** The members of the MathJax global object that this module uses. */
interface MathJaxGlobal {
  typesetPromise?: (elements?: Element[]) => Promise<void>;
  typesetClear?: (elements?: Element[]) => void;
}

/**
 * MathJax v3 startup config. `startup.typeset: false` turns off whole-page auto typesetting: SPA content arrives asynchronously, so it should
 * only be typeset explicitly on server-sanitized nodes. Delimiters keep the bundled build's defaults - inline `\(…\)` and block
 * `\[…\]` (keeping `$$…$$` as well), matching SSR page behavior. The bundle is `tex-chtml-full`, shared with the card frame; the
 * frame replaces this object with Anki's reviewer configuration before it loads the same script.
 */
export const MATHJAX_CONFIG = {
  tex: {
    inlineMath: [['\\(', '\\)']],
    displayMath: [['$$', '$$'], ['\\[', '\\]']],
  },
  // The CHTML web fonts are served from a stable path, because a font request cannot carry its own content hash.
  chtml: {
    fontURL: '/static/mathjax/output/chtml/fonts/woff-v2',
  },
  startup: { typeset: false },
};

let loadPromise: Promise<MathJaxGlobal | null> | null = null;

function currentDocument(): Document | null {
  return typeof document === 'undefined' ? null : document;
}

/**
 * mathjaxURL reads the MathJax hashed URL injected by the server. Returns an empty string when absent (dev mode, assets not embedded),
 * so the caller skips loading, consistent with SSR not referencing MathJax when the asset is missing.
 */
export function mathjaxURL(): string {
  const doc = currentDocument();
  if (!doc) return '';
  const meta = doc.querySelector(`meta[name="${META_NAME}"]`);
  const content = meta?.getAttribute('content') ?? '';
  return content.startsWith('/') ? content : '';
}

/**
 * ensureMathJax idempotently injects the self-hosted MathJax script and returns its global object.
 *
 * Returns null when nothing is loaded (no DOM, no URL, or the script failed to load). Repeated calls share the same load
 * and never insert a second <script>.
 */
export function ensureMathJax(): Promise<MathJaxGlobal | null> {
  if (loadPromise) return loadPromise;
  const doc = currentDocument();
  const url = mathjaxURL();
  if (!doc || !url) {
    loadPromise = Promise.resolve(null);
    return loadPromise;
  }
  const win = (doc.defaultView ?? (globalThis as unknown as Window)) as unknown as {
    MathJax?: MathJaxGlobal;
  };
  // Reuse an existing instance directly (e.g. the script was already loaded elsewhere) instead of injecting it again.
  if (win.MathJax && typeof win.MathJax.typesetPromise === 'function') {
    loadPromise = Promise.resolve(win.MathJax);
    return loadPromise;
  }
  // MathJax v3 reads window.MathJax when the script loads, so the config is written before the script is injected.
  win.MathJax = MATHJAX_CONFIG as unknown as MathJaxGlobal;
  loadPromise = new Promise((resolve) => {
    const script = doc.createElement('script');
    script.src = url;
    script.async = true;
    script.onload = () => resolve(win.MathJax ?? null);
    script.onerror = () => resolve(null);
    doc.head.appendChild(script);
  });
  return loadPromise;
}

/**
 * typeset re-typesets the given "server-sanitized HTML" container.
 *
 * typesetClear first, then typesetPromise: clear the old node's MathJax typesetting state so that after switching card/answer no
 * stale formula DOM is left behind (the card-switch path). Rendering failures are swallowed - a formula typesetting failure must not
 * interrupt the edit page or the review page.
 */
export async function typeset(nodes: Array<Element | null | undefined>): Promise<void> {
  const targets = nodes.filter((node): node is Element => node != null);
  if (targets.length === 0) return;
  const mj = await ensureMathJax();
  if (!mj || typeof mj.typesetPromise !== 'function') return;
  try {
    mj.typesetClear?.(targets);
    await mj.typesetPromise(targets);
  } catch {
    // Typesetting failures do not bubble: the rest of the page keeps working as usual.
  }
}

/**
 * removeMenuTabStops clears the tabindex MathJax puts on every rendered formula (`mjx-container[tabindex]`) below the given nodes.
 *
 * A formula inside a button, a checkbox row or another interactive control must not add a focus stop of its own: a tab stop nested
 * in a control splits one control into two, and the keyboard user reaches the same control twice. The formula's context menu stays
 * reachable with the mouse, which is the only reported use of that tab stop. Call this only after `typeset` on containers that live
 * inside an interactive control; ordinary card content keeps the tab stop so keyboard users can still reach the menu there.
 */
export function removeMenuTabStops(nodes: Array<Element | null | undefined>): void {
  for (const node of nodes) {
    if (!node) continue;
    for (const container of node.querySelectorAll('mjx-container[tabindex]')) {
      container.removeAttribute('tabindex');
    }
  }
}
