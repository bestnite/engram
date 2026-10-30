import { isSupportedLocale, setLocale } from '../lib/i18n';
import { enhanceShortcodes, playShortcodeSequence } from '../lib/shortcodes';
import { readMathJaxURL, typesetCard } from './mathjax';
import type { AVTag } from '../lib/api/types';
import {
  applyBrowserClasses,
  attachAnswerControls,
  bodyClassName,
  enhanceAVMarkers,
  enhanceSoundTags,
  installReviewerGlobals,
  playAVTag,
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
  /** The audio and TTS tags the face's play markers address, with the side each was extracted from. */
  avTags: AVTag[];
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

/** One AV tag from the parent. Only the fields the frame reads are checked; the server owns their types. */
function isAVTag(value: unknown): value is AVTag {
  if (!isRecord(value)) return false;
  return (value.kind === 'sound' || value.kind === 'tts') && (value.side === 'q' || value.side === 'a');
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
  // A face from an older server carries no tag list; the literal [sound:] fallback still finds those tags.
  const avTags = Array.isArray(data.avTags) ? data.avTags.filter(isAVTag) : [];
  return { type: 'show', html, css, bodyClass, side, mediaBase, autoplay, avTags, ordinal };
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
  let autoplay = false;
  let lastHeight = -1;
  let observer: ResizeObserver | null = null;
  let styleElement: HTMLStyleElement | null = null;
  let baseElement: HTMLBaseElement | null = null;
  // The face's body class recipe and the state the global helpers read.
  let bodyBase = 'card';
  let cardOrdinal = 0;
  let side = 'question';
  /** Every playable element of the current face, in document order. */
  let sources: HTMLElement[] = [];

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
      // A command names the side it was written for: the question frame addresses q markers and the answer frame a
      // markers, while an answer face also holds the question's q markers inside {{FrontSide}}.
      if ((requested === 'q' || requested === 'a') && Number.isInteger(index) && index >= 0) {
        playAVTag(sources, requested, index);
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
    autoplay = message.autoplay;

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
    // Play markers become the same audio/video/speech elements the shortcodes produce, so one control module drives
    // every tag. A face that still carries literal [sound:] tags gets the fallback; both lists stay in document order.
    const sideCode = side === 'answer' ? 'a' : 'q';
    sources = enhanceAVMarkers(doc, nextRoot, message.avTags);
    sources.push(...enhanceSoundTags(doc, nextRoot, sideCode));
    // The shortcode controls are part of the face, so they are attached here, after the content and its typesetting: a card
    // face leaving the parent DOM must not lose its audio or TTS controls.
    enhanceShortcodes([nextRoot]);
    if (message.side === 'question') focusAnswer(nextRoot);
    win.onShownHook?.();
    // Autoplay follows the deck setting and starts only the tags this face adds: on the answer side the question's
    // tags, carried in by {{FrontSide}}, are already markers with side q and are not replayed.
    if (autoplay) playSide(sideCode);

    if (message.side === 'answer') scrollToAnswer();
  }

  /** Elements of one marker side, in document order. */
  function sideSources(markerSide: string): HTMLElement[] {
    return sources.filter((source) => source.dataset.avSide === markerSide);
  }

  /** Starts every tag of one side: audio and speech run one after another, videos start at their own place. */
  function playSide(markerSide: string): void {
    const elements = sideSources(markerSide);
    const videos = elements.filter((element) => element.tagName.toLowerCase() === 'video');
    const controls = elements.filter((element) => element.tagName.toLowerCase() !== 'video');
    if (controls.length > 0) playShortcodeSequence(controls);
    for (const video of videos) {
      const element = video as HTMLVideoElement;
      try {
        element.currentTime = 0;
      } catch {
        // Metadata may not be loaded yet; the element still plays from its start.
      }
      void element.play().catch(() => {});
    }
  }

  /** `r` and `F5` replay every tag the face holds, including the question's tags carried into an answer face. */
  function replayAll(): void {
    playShortcodeSequence(sources.filter((element) => element.tagName.toLowerCase() !== 'video'));
    for (const video of sources) {
      if (video.tagName.toLowerCase() !== 'video') continue;
      const element = video as HTMLVideoElement;
      try {
        element.currentTime = 0;
      } catch {
        // A video whose metadata is not loaded cannot seek.
      }
      void element.play().catch(() => {});
    }
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
        // Replay restarts the face's tags in their own order.
        replayAll();
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
    // r and F5 replay the face's audio. F5 is the browser's reload key, so it is prevented here; a modifier means the
    // user is asking the browser for something else (Ctrl+R reloads), and the key then travels on unchanged.
    const plainKey = !event.ctrlKey && !event.altKey && !event.metaKey;
    if (plainKey && (event.key === 'F5' || event.key.toLowerCase() === 'r')) {
      event.preventDefault();
      replayAll();
      return;
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
