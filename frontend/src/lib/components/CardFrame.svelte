<script module lang="ts">
  import type { FrameAnswerValue } from '../../cardframe/reviewer';
  import type { AVTag } from '../api/types';

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
  import { tick } from 'svelte';
  import { appVersion } from '../version';

  interface Props {
    /**
     * Card-face HTML handed to the frame exactly as the server rendered it. Empty means the caller has no rich content and
     * shows plain text instead of a frame.
     */
    html: string;
    /** Card-face CSS; the frame document already carries the app stylesheet, so this holds the face typography only. */
    css: string;
    /** Class list written on the frame's `<body>`. */
    bodyClass: string;
    /** `question` or `answer`; on the answer side the frame reports where `#answer` sits (see `onshown`). */
    side: string;
    /** `<base href>` prefix that relative media names resolve against, `/fm/<token>/`. */
    mediaBase: string;
    autoplay?: boolean;
    /** The audio and TTS tags the face's play markers address (Go: CardFace.av_tags). */
    avTags?: AVTag[];
    /** The card's zero-based template ordinal; the frame writes `card` + ordinal + 1 on its body, as Anki does. */
    ordinal?: number;
    /** Receives every key press the frame reports; the view maps it onto its own shortcuts. */
    onkey?: (message: CardFrameKey) => void;
    /** Receives the answer value a control posted (`answer {value}`). */
    onanswer?: (value: FrameAnswerValue) => void;
    /** Accessible name of the iframe. */
    title?: string;
    /**
     * Whether the frame has finished showing the current props: true once it reports `shown` for the latest show, false from
     * every new show until then. A caller binds it to cover the face with a loading placeholder while it renders.
     */
    shown?: boolean;
    /**
     * Called once the frame has finished showing the latest face and the iframe carries that face's height. `answerTop` is
     * the distance from the frame's top edge to the face's `#answer` element, or null when the face has none or is a
     * question. The frame never scrolls itself, so a caller that wants Anki's jump to the answer scrolls its own page.
     */
    onshown?: (report: { frame: HTMLIFrameElement; answerTop: number | null }) => void;
  }

  let {
    html,
    css,
    bodyClass,
    side,
    mediaBase,
    autoplay = false,
    avTags = [],
    ordinal = 0,
    onkey,
    onanswer,
    title = '',
    shown = $bindable(false),
    onshown,
  }: Props = $props();

  // The version segment only busts the iframe cache; a build without an injected version uses a fixed placeholder.
  const src = $derived(`/card-frame/${encodeURIComponent(appVersion() || 'dev')}`);

  let iframe = $state<HTMLIFrameElement | null>(null);
  let ready = $state(false);
  let height = $state(0);
  // The number of the latest show sent. Not state: only the `shown` report compares against it.
  let showSeq = 0;

  /** Sends the whole face again: `show` replaces the document's content, so a single changed prop is a new show. */
  function sendShow(): void {
    const win = iframe?.contentWindow;
    if (!win || !ready) return;
    showSeq += 1;
    shown = false;
    // The tag list usually comes from Svelte state, which is a proxy, and postMessage cannot clone a proxy
    // (DataCloneError): the show would never arrive. A snapshot is the plain value of the same list.
    win.postMessage(
      { type: 'show', html, css, bodyClass, side, mediaBase, autoplay, avTags: $state.snapshot(avTags), ordinal, seq: showSeq },
      '*',
    );
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
    void [html, css, bodyClass, side, mediaBase, autoplay, avTags, ordinal, ready, iframe];
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
      case 'shown': {
        // A report for an older show means a newer face is still rendering.
        if (data.seq !== showSeq) return;
        shown = true;
        const answerTop =
          typeof data.answerTop === 'number' && Number.isFinite(data.answerTop) && data.answerTop >= 0 && data.answerTop <= 100000
            ? data.answerTop
            : null;
        // The frame posts its height just before `shown`; the tick lets that height reach the iframe element first, so the
        // caller measures the page with the face at its final size.
        if (onshown) void tick().then(() => onshown({ frame, answerTop }));
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
