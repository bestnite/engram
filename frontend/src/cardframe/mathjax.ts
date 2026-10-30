/**
 * MathJax for the card frame.
 *
 * The frame cannot reuse the SPA loader directly: the SPA loader reads `<meta name="engram-mathjax">` at typeset time, while the
 * frame replaces its whole `<head>` on every `show`, so the meta is gone by then. The URL is therefore captured once at startup,
 * and the same self-hosted `tex-chtml-full` build is used. The loader is idempotent per window: once the script has run,
 * `window.MathJax` is reused and no second `<script>` is inserted.
 */

const META_NAME = 'engram-mathjax';

/**
 * Anki's reviewer configuration at tag 26.09.3 (ts/mathjax/index.ts): display math is `\[ … \]` only, inline math keeps
 * MathJax's `\( … \)` default, and the package list adds `noerrors` and `mathtools` while removing `textmacros`. The frame
 * must render a card the way the desktop reviewer does, so these values are copied rather than derived from the SPA config.
 */
export const FRAME_MATHJAX_CONFIG = {
  tex: {
    displayMath: [['\\[', '\\]']],
    processEscapes: false,
    processEnvironments: false,
    processRefs: false,
    packages: {
      '[+]': ['noerrors', 'mathtools'],
      '[-]': ['textmacros'],
    },
  },
  loader: {
    load: ['[tex]/noerrors', '[tex]/mathtools'],
  },
  // The CHTML web fonts are served from a stable path, because a font request cannot carry its own content hash.
  chtml: {
    fontURL: '/static/mathjax/output/chtml/fonts/woff-v2',
  },
  startup: { typeset: false },
};

interface MathJaxGlobal {
  typesetPromise?: (elements?: Element[]) => Promise<void>;
  typesetClear?: (elements?: Element[]) => void;
}

interface MathJaxWindow extends Window {
  MathJax?: MathJaxGlobal;
}

const loadPromises = new WeakMap<Window, Promise<MathJaxGlobal | null>>();

/**
 * readMathJaxURL returns the URL injected by the server, or an empty string when the asset is absent (dev mode or no embedded
 * build). It must be called before the first `show` clears the `<head>`.
 */
export function readMathJaxURL(doc: Document): string {
  const content = doc.querySelector(`meta[name="${META_NAME}"]`)?.getAttribute('content') ?? '';
  return content.startsWith('/') ? content : '';
}

function existingMathJax(win: MathJaxWindow): MathJaxGlobal | null {
  const mj = win.MathJax;
  return mj && typeof mj.typesetPromise === 'function' ? mj : null;
}

function loadMathJax(win: MathJaxWindow, doc: Document, url: string): Promise<MathJaxGlobal | null> {
  const cached = loadPromises.get(win);
  if (cached) return cached;

  // MathJax v3 reads window.MathJax when the script loads, so the configuration is written before the script is inserted.
  win.MathJax = FRAME_MATHJAX_CONFIG as unknown as MathJaxGlobal;
  const promise = new Promise<MathJaxGlobal | null>((resolve) => {
    const script = doc.createElement('script');
    script.src = url;
    script.async = true;
    script.addEventListener('load', () => resolve(existingMathJax(win)));
    script.addEventListener('error', () => resolve(null));
    doc.head.appendChild(script);
  });
  loadPromises.set(win, promise);
  return promise;
}

/**
 * typesetCard typesets the frame content root. A load or typeset failure is swallowed: a broken formula must not stop the card
 * from being shown, exactly as on the SPA side.
 */
export async function typesetCard(win: Window, doc: Document, url: string, node: Element): Promise<void> {
  const frameWindow = win as MathJaxWindow;
  let mj = existingMathJax(frameWindow);
  if (!mj) {
    if (!url) return;
    mj = await loadMathJax(frameWindow, doc, url);
  }
  if (!mj) return;
  try {
    mj.typesetClear?.([node]);
    await mj.typesetPromise?.([node]);
  } catch {
    // Typesetting failures do not bubble.
  }
}
