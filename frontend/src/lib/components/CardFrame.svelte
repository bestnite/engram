<script module lang="ts">
  import type { FrameAnswerValue } from '../../cardframe/reviewer';

  /**
   * One key press reported by the frame. The frame suppresses keys typed into an input, a textarea or a contenteditable
   * element, so every message is a shortcut the card face itself did not consume.
   */
  export interface CardFrameKey {
    key: string;
    code: string;
    ctrl: boolean;
    alt: boolean;
    shift: boolean;
    meta: boolean;
  }
</script>

<script lang="ts">
  /**
   * CardFrame shows one card face inside the sandboxed frame document.
   *
   * The face never enters the parent DOM: the frame receives it with `show` and renders it in its own document, so card
   * content (including a card author's scripts and styles) cannot reach the parent page. The parent only sizes the iframe
   * from the height the frame reports, forwards the key presses it receives, sends the theme, and opens links in a new tab.
   *
   * Every incoming message is accepted only from this component's own iframe and every field is validated, because the
   * frame may report anything a card's script asks it to.
   */
  import { appVersion } from '../version';

  interface Props {
    /** Sanitized card-face HTML. Empty means the caller has no rich content and shows plain text instead of a frame. */
    html: string;
    /** Card-face CSS; the frame document already carries the app stylesheet, so this holds the face typography only. */
    css: string;
    /** Class list written on the frame's `<body>`. */
    bodyClass: string;
    /** `question` or `answer`; the frame scrolls to `#answer` on the answer side. */
    side: string;
    /** `<base href>` prefix that relative media names resolve against, `/fm/<token>/`. */
    mediaBase: string;
    autoplay?: boolean;
    /** The card's zero-based template ordinal; the frame writes `card` + ordinal + 1 on its body, as Anki does. */
    ordinal?: number;
    /** Receives every key press the frame reports; the view maps it onto its own shortcuts. */
    onkey?: (message: CardFrameKey) => void;
    /** Receives the answer value a control posted (`answer {value}`). */
    onanswer?: (value: FrameAnswerValue) => void;
    /** Accessible name of the iframe. */
    title?: string;
  }

  let {
    html,
    css,
    bodyClass,
    side,
    mediaBase,
    autoplay = false,
    ordinal = 0,
    onkey,
    onanswer,
    title = '',
  }: Props = $props();

  // The version segment only busts the iframe cache; a build without an injected version uses a fixed placeholder.
  const src = $derived(`/card-frame/${encodeURIComponent(appVersion() || 'dev')}`);

  let iframe = $state<HTMLIFrameElement | null>(null);
  let ready = $state(false);
  let height = $state(0);

  /** Sends the whole face again: `show` replaces the document's content, so a single changed prop is a new show. */
  function sendShow(): void {
    const win = iframe?.contentWindow;
    if (!win || !ready) return;
    win.postMessage({ type: 'show', html, css, bodyClass, side, mediaBase, autoplay, ordinal }, '*');
  }

  /** Sends the current theme; the frame owns its own `<html>` element, so it cannot inherit the parent's class. */
  function sendTheme(): void {
    const win = iframe?.contentWindow;
    if (!win || !ready) return;
    win.postMessage({ type: 'theme', dark: document.documentElement.classList.contains('dark') }, '*');
  }

  // One show per face: the first after `ready`, and one after every prop change. The effect reads every prop that `show`
  // carries, so a change to any of them resends the face instead of leaving the frame with a stale one.
  $effect(() => {
    void [html, css, bodyClass, side, mediaBase, autoplay, ordinal, ready, iframe];
    sendShow();
  });

  function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === 'object' && value !== null;
  }

  function parseKey(data: Record<string, unknown>): CardFrameKey | null {
    const { key, code, ctrl, alt, shift, meta } = data;
    if (
      typeof key !== 'string' ||
      typeof code !== 'string' ||
      typeof ctrl !== 'boolean' ||
      typeof alt !== 'boolean' ||
      typeof shift !== 'boolean' ||
      typeof meta !== 'boolean'
    ) {
      return null;
    }
    return { key, code, ctrl, alt, shift, meta };
  }

  function parseAnswer(value: unknown): FrameAnswerValue | null {
    if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return value;
    if (Array.isArray(value)) {
      if (value.every((item) => typeof item === 'number')) return value as number[];
      if (value.every((item) => typeof item === 'string')) return value as string[];
      return null;
    }
    return null;
  }

  function onMessage(event: MessageEvent): void {
    const frame = iframe;
    if (!frame || event.source !== frame.contentWindow) return;
    const data: unknown = event.data;
    if (!isRecord(data) || typeof data.type !== 'string') return;

    switch (data.type) {
      case 'ready': {
        // A second `ready` means the frame document reloaded: the effect already sent the current face for the first one,
        // so a reload has to be answered explicitly.
        const reloaded = ready;
        ready = true;
        if (reloaded) sendShow();
        sendTheme();
        return;
      }
      case 'height':
        if (typeof data.px === 'number' && Number.isFinite(data.px) && data.px >= 0 && data.px <= 100000) {
          height = Math.round(data.px);
        }
        return;
      case 'key': {
        const message = parseKey(data);
        if (message) onkey?.(message);
        return;
      }
      case 'answer': {
        // The answer control lives in the frame; the shape is the one internal/answer decodes.
        const value = parseAnswer(data.value);
        if (value !== null) onanswer?.(value);
        return;
      }
      case 'link':
        // Only http(s) targets are opened: a frame script must not be able to hand the parent a javascript: or data: URL.
        if (typeof data.href === 'string' && /^https?:\/\//i.test(data.href)) {
          window.open(data.href, '_blank', 'noopener,noreferrer');
        }
        return;
      default:
        return;
    }
  }

  $effect(() => {
    // The listener and the observer live as long as the component. The observer watches only the `class` attribute of
    // `<html>`, which is what the theme switch writes.
    window.addEventListener('message', onMessage);
    const observer = new MutationObserver(() => sendTheme());
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    return () => {
      window.removeEventListener('message', onMessage);
      observer.disconnect();
    };
  });
</script>

<iframe
  bind:this={iframe}
  class="card-frame block w-full border-0"
  sandbox="allow-scripts"
  allow="autoplay"
  {title}
  {src}
  style:height={`${height}px`}
  data-card-frame=""
  data-side={side}
></iframe>
