import { isSupportedLocale, setLocale } from '../lib/i18n';
import { enhanceShortcodes } from '../lib/shortcodes';
import { readMathJaxURL, typesetCard } from './mathjax';
import {
  applyBrowserClasses,
  attachAnswerControls,
  bodyClassName,
  enhanceSoundTags,
  installReviewerGlobals,
  playSound,
  type AnswerReader,
  type FrameAnswerValue,
} from './reviewer';

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
  /** The card's zero-based template ordinal; the body class is `card` + ordinal + 1, as Anki writes it. */
  ordinal: number;
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
  const { html, css, bodyClass, side, mediaBase, autoplay, ordinal } = data;
  if (
    !isString(html) ||
    !isString(css) ||
    !isString(bodyClass) ||
    !isString(side) ||
    !isString(mediaBase) ||
    typeof autoplay !== 'boolean' ||
    typeof ordinal !== 'number' ||
    !Number.isFinite(ordinal)
  ) {
    return null;
  }
  return { type: 'show', html, css, bodyClass, side, mediaBase, autoplay, ordinal };
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

  // The browser and platform classes are written once at boot: the user agent does not change during the frame's life,
  // and Anki's `addBrowserClasses` runs in the same place (before any card renders).
  applyBrowserClasses(doc.documentElement, win.navigator.userAgent);

  let dark = false;
  // Stored for the autoplay phase; this phase deliberately does nothing with it.
  let autoplay = false;
  let lastHeight = -1;
  let observer: ResizeObserver | null = null;
  let styleElement: HTMLStyleElement | null = null;
  let baseElement: HTMLBaseElement | null = null;
  // The face's body class recipe and the state the global helpers read.
  let bodyBase = 'card';
  let cardOrdinal = 0;
  let side = 'question';
  let sounds: HTMLElement[] = [];

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

  function applyBodyClass(): void {
    if (doc.body) doc.body.className = bodyClassName(bodyBase, cardOrdinal, dark);
  }

  function emitAnswer(value: FrameAnswerValue): void {
    post({ type: 'answer', value });
  }

  const root = contentRoot();
  // The answer controls live in the same content root for the frame's whole life; one delegated listener pair is enough
  // and re-attaching it per show would post every value twice.
  const answerReader: AnswerReader | null = root ? attachAnswerControls(root, emitAnswer) : null;

  installReviewerGlobals(win, {
    answer: () => answerReader?.currentAnswer(),
    emitAnswer,
    typedAnswer: () => answerReader?.typedAnswer() ?? '',
    play: (requested, index) => {
      // The frame holds one side of the card. A command for the other side has nothing to play here and is ignored;
      // the answer frame receives `play:a:N` and the question frame `play:q:N`.
      const expected = side === 'answer' ? 'a' : 'q';
      if (requested === expected && Number.isInteger(index) && index >= 0) {
        playSound(sounds, index);
      }
    },
  });

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

    side = message.side;
    bodyBase = message.bodyClass;
    cardOrdinal = message.ordinal;
    resetHead(message.css, message.mediaBase);
    // The browser and platform classes were written at boot; the show must not wipe them, so only the theme class changes.
    doc.documentElement.classList.toggle('dark', dark);
    doc.documentElement.style.colorScheme = dark ? 'dark' : 'light';
    applyBodyClass();

    const nextRoot = contentRoot();
    if (!nextRoot) return;
    nextRoot.innerHTML = message.html;

    await runScripts(nextRoot);

    win.onUpdateHook?.();
    if (containsMath(message.html)) {
      await typesetCard(win, doc, mathjaxURL, nextRoot);
    }
    // `[sound:]` tags become audio-control sources before the shortcode enhancer runs, so every tag gets engram's
    // play/pause, seek, ±5 s and time controls. The order is the tag order Anki's `play:N` commands address.
    sounds = enhanceSoundTags(doc, nextRoot);
    // The shortcode controls are part of the face, so they are attached here, after the content and its typesetting: a card
    // face leaving the parent DOM must not lose its audio or TTS controls.
    enhanceShortcodes([nextRoot]);
    if (message.side === 'question') focusAnswer(nextRoot);
    win.onShownHook?.();

    if (message.side === 'answer') scrollToAnswer();
  }

  /** Puts the caret in the answer input a question face shows, as Anki's reviewer does on a type-in card. */
  function focusAnswer(node: Element): void {
    const input = node.querySelector<HTMLElement>('input.answer-input[type=text], textarea.answer-input');
    if (!input || typeof input.focus !== 'function') return;
    input.focus();
  }

  function applyTheme(nextDark: boolean): void {
    dark = nextDark;
    doc.documentElement.classList.toggle('dark', dark);
    doc.documentElement.style.colorScheme = dark ? 'dark' : 'light';
    applyBodyClass();
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
        // Replay starts the first tag in the face's own order; the tag's own control does the playback.
        playSound(sounds, 0);
        return;
      default:
        return;
    }
  }

  function postKey(event: KeyboardEvent): void {
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

  function onKeyDown(event: KeyboardEvent): void {
    const active = doc.activeElement;
    if (active instanceof HTMLElement) {
      // Enter in a single-line answer input is the graded card's submit key, so it is forwarded even though the
      // control owns the rest of the keyboard. A textarea keeps Enter for a newline (the short-answer card).
      const submitFromInput =
        active instanceof HTMLInputElement &&
        active.classList.contains('answer-input') &&
        event.key === 'Enter' &&
        !event.isComposing &&
        event.keyCode !== 229;
      if (submitFromInput) {
        postKey(event);
        return;
      }
      const tag = active.tagName.toLowerCase();
      if (tag === 'input' || tag === 'textarea' || active.isContentEditable) return;
    }
    postKey(event);
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
