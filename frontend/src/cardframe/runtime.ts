import { isSupportedLocale, setLocale } from '../lib/i18n';
import { enhanceShortcodes } from '../lib/shortcodes';
import { readMathJaxURL, typesetCard } from './mathjax';

/**
 * The card frame runtime.
 *
 * The frame document is deliberately tiny: the parent page sends it card content with `postMessage`, the runtime writes that
 * content into `<div id="qa">`, re-executes its scripts, typesets the face, attaches the shortcode controls and reports
 * height, keys, answers and links back. It renders no Svelte component and reads no cookie: the sandboxed iframe has an opaque
 * origin, and everything it needs arrives in the messages or the frame document's own `<html lang>`.
 *
 * Every message is accepted only from the embedding page (`event.source` must be the parent window) and every field is
 * type-checked, because the frame renders untrusted card content.
 */

export interface FrameRuntimeOptions {
  win?: Window;
  doc?: Document;
  /** The embedding window; defaults to `win.parent` and exists so tests can supply their own. */
  parent?: Window | null;
}

export interface ShowMessage {
  type: 'show';
  html: string;
  css: string;
  bodyClass: string;
  side: string;
  mediaBase: string;
  autoplay: boolean;
}

export interface FrameRuntime {
  /** Handles one parent message; resolves after the show sequence (scripts, hooks, typesetting) has finished. */
  handleMessage(event: MessageEvent): Promise<void>;
  /** Posts `ready` once, after the runtime has installed its listeners. */
  signalReady(): void;
  destroy(): void;
}

interface FrameWindow extends Window {
  onUpdateHook?: () => void;
  onShownHook?: () => void;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function isString(value: unknown): value is string {
  return typeof value === 'string';
}

function parseShow(data: Record<string, unknown>): ShowMessage | null {
  const { html, css, bodyClass, side, mediaBase, autoplay } = data;
  if (
    !isString(html) ||
    !isString(css) ||
    !isString(bodyClass) ||
    !isString(side) ||
    !isString(mediaBase) ||
    typeof autoplay !== 'boolean'
  ) {
    return null;
  }
  return { type: 'show', html, css, bodyClass, side, mediaBase, autoplay };
}

function containsMath(html: string): boolean {
  return html.includes('\\(') || html.includes('\\[');
}

export function createRuntime(options: FrameRuntimeOptions = {}): FrameRuntime {
  const win = (options.win ?? window) as FrameWindow;
  const doc = options.doc ?? win.document;
  // `Window.parent` is typed as WindowProxy; the runtime only needs postMessage and identity comparison.
  const parent = (options.parent ?? (win.parent as Window | null) ?? win) as Window | null;

  if (typeof win.onUpdateHook !== 'function') win.onUpdateHook = () => {};
  if (typeof win.onShownHook !== 'function') win.onShownHook = () => {};

  // The frame document carries the request's resolved language on <html lang>, exactly like the SPA entry: the server knows
  // the user's profile setting, the runtime does not and reads no cookie. Adopting it before the first face is shown makes
  // the shortcode controls label themselves from the user's catalog; without a supported lang the module's browser-language
  // detection stays in charge.
  const documentLang = doc.documentElement.lang.trim();
  if (documentLang !== '' && isSupportedLocale(documentLang)) setLocale(documentLang);

  // The URL comes from the meta the server injects into the frame entry; reading it at boot keeps it independent of any later
  // head change.
  const mathjaxURL = readMathJaxURL(doc);

  let dark = false;
  // Stored for the autoplay phase; this phase deliberately does nothing with it.
  let autoplay = false;
  let lastHeight = -1;
  let observer: ResizeObserver | null = null;
  let styleElement: HTMLStyleElement | null = null;
  let baseElement: HTMLBaseElement | null = null;

  function post(message: Record<string, unknown>): void {
    parent?.postMessage(message, '*');
  }

  function contentRoot(): HTMLElement | null {
    const existing = doc.getElementById('qa');
    if (existing) return existing;
    const created = doc.createElement('div');
    created.id = 'qa';
    doc.body?.appendChild(created);
    return created;
  }

  function runScripts(root: Element): Promise<void> {
    // innerHTML never executes scripts, so each one is replaced by a fresh element. External scripts are awaited one at a time,
    // which keeps document order even though a dynamically inserted script would otherwise run as soon as it loads.
    const replacements: Array<() => Promise<void>> = [];
    for (const original of Array.from(root.querySelectorAll('script'))) {
      const external = (original.getAttribute('src') ?? '') !== '';
      replacements.push(async () => {
        const fresh = doc.createElement('script');
        for (const attr of Array.from(original.attributes)) {
          fresh.setAttribute(attr.name, attr.value);
        }
        if (external) {
          fresh.async = false;
          const loaded = new Promise<void>((resolve) => {
            fresh.addEventListener('load', () => resolve(), { once: true });
            fresh.addEventListener('error', () => resolve(), { once: true });
          });
          original.replaceWith(fresh);
          await loaded;
        } else {
          fresh.textContent = original.textContent;
          original.replaceWith(fresh);
        }
      });
    }
    // Sequential awaiting is what guarantees the order; an inline script executes synchronously on insertion.
    return replacements.reduce<Promise<void>>((chain, run) => chain.then(run), Promise.resolve());
  }

  function resetHead(css: string, mediaBase: string): void {
    // The frame document's own stylesheet belongs to the runtime bundle and must survive a show: card CSS is written into a
    // dedicated style element instead, and only that element is rewritten.
    if (!styleElement) {
      styleElement = doc.createElement('style');
      styleElement.setAttribute('data-card-css', '');
      doc.head.appendChild(styleElement);
    }
    styleElement.textContent = css;
    if (mediaBase) {
      if (!baseElement) {
        baseElement = doc.createElement('base');
        doc.head.insertBefore(baseElement, doc.head.firstChild);
      }
      baseElement.href = mediaBase;
    } else if (baseElement) {
      baseElement.remove();
      baseElement = null;
    }
  }

  function scrollToAnswer(): void {
    const target = doc.getElementById('answer');
    if (!target || typeof target.scrollIntoView !== 'function') return;
    try {
      target.scrollIntoView({ block: 'start' });
    } catch {
      // A missing layout implementation must not break the show sequence.
    }
  }

  async function show(message: ShowMessage): Promise<void> {
    // Stored for the autoplay phase; this phase does nothing with it.
    autoplay = message.autoplay;
    void autoplay;

    resetHead(message.css, message.mediaBase);
    doc.documentElement.className = dark ? 'dark' : '';
    if (doc.body) doc.body.className = message.bodyClass;

    const root = contentRoot();
    if (!root) return;
    root.innerHTML = message.html;

    await runScripts(root);

    win.onUpdateHook?.();
    if (containsMath(message.html)) {
      await typesetCard(win, doc, mathjaxURL, root);
    }
    // The shortcode controls are part of the face, so they are attached here, after the content and its typesetting: a card
    // face leaving the parent DOM must not lose its audio or TTS controls.
    enhanceShortcodes([root]);
    win.onShownHook?.();

    if (message.side === 'answer') scrollToAnswer();
  }

  function applyTheme(nextDark: boolean): void {
    dark = nextDark;
    doc.documentElement.classList.toggle('dark', dark);
    doc.documentElement.style.colorScheme = dark ? 'dark' : 'light';
  }

  async function handleMessage(event: MessageEvent): Promise<void> {
    if ((event.source as unknown) !== (parent as unknown)) return;
    const data: unknown = event.data;
    if (!isRecord(data) || !isString(data.type)) return;

    switch (data.type) {
      case 'show': {
        const message = parseShow(data);
        if (message) await show(message);
        return;
      }
      case 'theme':
        if (typeof data.dark === 'boolean') applyTheme(data.dark);
        return;
      case 'replay':
        doc.dispatchEvent(new CustomEvent('engram:replay'));
        return;
      default:
        return;
    }
  }

  function onKeyDown(event: KeyboardEvent): void {
    const active = doc.activeElement;
    if (active instanceof HTMLElement) {
      const tag = active.tagName.toLowerCase();
      if (tag === 'input' || tag === 'textarea' || active.isContentEditable) return;
    }
    post({
      type: 'key',
      key: event.key,
      code: event.code,
      ctrl: event.ctrlKey,
      alt: event.altKey,
      shift: event.shiftKey,
      meta: event.metaKey,
    });
  }

  function onClick(event: MouseEvent): void {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const anchor = target.closest('a[href]');
    if (!(anchor instanceof HTMLAnchorElement)) return;
    const href = anchor.href;
    if (!href.startsWith('http://') && !href.startsWith('https://')) return;
    event.preventDefault();
    post({ type: 'link', href });
  }

  function onMessage(event: MessageEvent): void {
    void handleMessage(event);
  }

  win.addEventListener('keydown', onKeyDown);
  doc.addEventListener('click', onClick);
  win.addEventListener('message', onMessage);

  const root = contentRoot();
  const ResizeObserverCtor = (win as unknown as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver;
  if (root && typeof ResizeObserverCtor === 'function') {
    observer = new ResizeObserverCtor((entries) => {
      for (const entry of entries) {
        const px = Math.round(entry.contentRect.height);
        if (px === lastHeight) continue;
        lastHeight = px;
        post({ type: 'height', px });
      }
    });
    observer.observe(root);
  }

  return {
    handleMessage,
    signalReady() {
      post({ type: 'ready' });
    },
    destroy() {
      win.removeEventListener('keydown', onKeyDown);
      doc.removeEventListener('click', onClick);
      win.removeEventListener('message', onMessage);
      observer?.disconnect();
      observer = null;
    },
  };
}
