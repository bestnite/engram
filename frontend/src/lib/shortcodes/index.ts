/**
 * Client-side controls for the shortcodes the server expands.
 *
 * The renderer turns `{{tts:…}}` and `{{audio:…}}` into elements that carry data attributes and nothing else
 * (`span.engram-tts[data-tts-*]`, `span.engram-audio[data-audio-*]`). Ruby needs no behaviour and is left alone.
 * This module is the single enhancement entry point: it reads only the server's data attributes, never the raw card
 * text, and it only ever operates on containers whose HTML the server already sanitized and that `typeset` was
 * called on.
 *
 * The visible body of each element stays in the output: a reader without JavaScript, without a speech engine, or on
 * a page whose media it may not read keeps the text. The audio shortcode's body is a bare `/media/<sha256>` path, so
 * once a play control exists the path is moved into a visually hidden wrapper - kept in the DOM, not deleted - and
 * moved back into view when the media turns out to be unplayable.
 *
 * Idempotence: the marker attribute `data-shortcode-enhanced` on a shortcode element records that it was processed,
 * and a WeakSet records which containers already carry the delegated click listener. A second enhancement of the
 * same DOM therefore adds neither a second button nor a second listener, and re-rendering a container with new
 * server HTML works because the listener lives on the container, not on the elements inside it.
 */

import { formatMessage, getLocale } from '../i18n';

const SOURCE_MARKER = 'data-shortcode-enhanced';
const BUTTON_MARKER = 'data-shortcode-control';
const FALLBACK_MARKER = 'data-shortcode-fallback';
const SVG_NS = 'http://www.w3.org/2000/svg';

/**
 * A shortcode inside one of these elements cannot host a control: a button inside a button (or a link) is invalid
 * markup and splits one control into two focus stops for keyboard users. The review page's option rows are such
 * controls, so their shortcodes keep the visible text and get no control of their own.
 */
const INTERACTIVE_SELECTOR =
  'button, a[href], [role="button"], [role="link"], [role="radio"], [role="checkbox"], [role="switch"], [role="tab"], [role="menuitem"]';

const LABEL_KEYS = {
  tts: { play: 'shortcode.tts.play', stop: 'shortcode.tts.stop', unavailable: 'shortcode.tts.unavailable' },
  audio: { play: 'shortcode.audio.play', stop: 'shortcode.audio.stop', unavailable: 'shortcode.audio.unavailable' },
} as const;

/**
 * The control is a 28px circle whose ::before extends the touch target to 40px without taking layout space, so
 * adding a control never shifts the line box. `align-middle` and the negative block margin keep it on the text's
 * centre line.
 */
const BUTTON_BASE = [
  'relative -my-1 mr-1.5 inline-flex size-7 shrink-0 cursor-pointer items-center justify-center',
  'rounded-full border align-middle transition-colors',
  'before:absolute before:-inset-1.5 before:content-[""]',
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
].join(' ');
const BUTTON_IDLE = `${BUTTON_BASE} border-border bg-surface text-muted-foreground hover:bg-muted hover:text-foreground`;
const BUTTON_PLAYING = `${BUTTON_BASE} border-brand bg-brand text-brand-foreground`;

type ControlKind = 'tts' | 'audio';
type Phase = 'idle' | 'playing';

interface AudioHandlers {
  loadedmetadata: () => void;
  timeupdate: () => void;
  ended: () => void;
  error: () => void;
}

interface ControlState {
  kind: ControlKind;
  /** The server-produced element that carries the data attributes; its text is the accessible fallback. */
  source: HTMLElement;
  button: HTMLButtonElement;
  phase: Phase;
  /** For audio: the wrapper that holds the media path out of sight while a control exists. */
  fallback: HTMLElement | null;
  audio: HTMLAudioElement | null;
  handlers: AudioHandlers | null;
  loop: boolean;
}

interface ActiveSpeech {
  state: ControlState;
  synth: SpeechSynthesis;
  utterance: SpeechSynthesisUtterance;
}

/** Containers that already carry the delegated click listener. */
const boundContainers = new WeakSet<Element>();
/** State of every control, keyed by its button. */
const controls = new WeakMap<HTMLButtonElement, ControlState>();

/** At most one utterance and one audio play at a time, across the whole page. */
let activeSpeech: ActiveSpeech | null = null;
let activeAudio: ControlState | null = null;

function label(key: string): string {
  return formatMessage(getLocale(), key);
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

/** Reads a decimal attribute as a number; absent or malformed values are ignored rather than guessed at. */
function numberAttr(source: Element, name: string): number | null {
  const raw = source.getAttribute(name);
  if (raw === null) return null;
  const value = Number(raw);
  return Number.isFinite(value) ? value : null;
}

/* ---------------------------------------------------------------------------------------------------------------- */
/* Icons                                                                                                             */
/* ---------------------------------------------------------------------------------------------------------------- */

type IconName = 'speak' | 'stop' | 'play' | 'pause';

interface IconPart {
  tag: 'path' | 'rect';
  attrs: Record<string, string>;
}

const ICONS: Record<IconName, IconPart[]> = {
  speak: [
    { tag: 'path', attrs: { d: 'M11 5 6 9H2v6h4l5 4V5Z', fill: 'currentColor' } },
    {
      tag: 'path',
      attrs: {
        d: 'M15.5 8.5a5 5 0 0 1 0 7M18.5 5.5a9 9 0 0 1 0 13',
        fill: 'none',
        stroke: 'currentColor',
        'stroke-width': '2',
        'stroke-linecap': 'round',
      },
    },
  ],
  stop: [{ tag: 'rect', attrs: { x: '6', y: '6', width: '12', height: '12', rx: '2', fill: 'currentColor' } }],
  play: [{ tag: 'path', attrs: { d: 'M8 5v14l11-7Z', fill: 'currentColor' } }],
  pause: [
    { tag: 'rect', attrs: { x: '6', y: '5', width: '4', height: '14', rx: '1', fill: 'currentColor' } },
    { tag: 'rect', attrs: { x: '14', y: '5', width: '4', height: '14', rx: '1', fill: 'currentColor' } },
  ],
};

/** Builds the icon from constant attribute maps; no card text ever passes through this path. */
function makeIcon(name: IconName): SVGSVGElement {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('width', '16');
  svg.setAttribute('height', '16');
  svg.setAttribute('class', 'size-4');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  for (const part of ICONS[name]) {
    const node = document.createElementNS(SVG_NS, part.tag);
    for (const [key, value] of Object.entries(part.attrs)) node.setAttribute(key, value);
    svg.appendChild(node);
  }
  return svg;
}

/* ---------------------------------------------------------------------------------------------------------------- */
/* Control rendering                                                                                                 */
/* ---------------------------------------------------------------------------------------------------------------- */

function updateButton(state: ControlState): void {
  const playing = state.phase === 'playing';
  const keys = LABEL_KEYS[state.kind];
  const text = label(playing ? keys.stop : keys.play);
  state.button.className = playing ? BUTTON_PLAYING : BUTTON_IDLE;
  state.button.setAttribute('aria-pressed', playing ? 'true' : 'false');
  state.button.setAttribute('aria-label', text);
  state.button.setAttribute('title', text);
  const icon: IconName = state.kind === 'tts' ? (playing ? 'stop' : 'speak') : playing ? 'pause' : 'play';
  state.button.replaceChildren(makeIcon(icon));
}

/** Moves the audio path out of sight while keeping it in the DOM for the no-control cases. */
function hideFallback(source: HTMLElement): HTMLElement {
  const wrapper = document.createElement('span');
  wrapper.className = 'sr-only';
  wrapper.setAttribute(FALLBACK_MARKER, '');
  while (source.firstChild) wrapper.appendChild(source.firstChild);
  source.appendChild(wrapper);
  return wrapper;
}

function showFallback(state: ControlState): void {
  state.fallback?.classList.remove('sr-only');
}

function detachAudio(state: ControlState): void {
  const audio = state.audio;
  const handlers = state.handlers;
  if (audio && handlers) {
    audio.removeEventListener('loadedmetadata', handlers.loadedmetadata);
    audio.removeEventListener('timeupdate', handlers.timeupdate);
    audio.removeEventListener('ended', handlers.ended);
    audio.removeEventListener('error', handlers.error);
  }
  state.handlers = null;
  if (audio) {
    try {
      audio.pause();
    } catch {
      // A paused or already-detached element has nothing to pause; the failure carries no information.
    }
  }
}

function resetPhase(state: ControlState): void {
  if (state.phase === 'idle') return;
  state.phase = 'idle';
  updateButton(state);
}

/**
 * Removes the control and restores the visible fallback. Used for the two cases in which a click produced nothing:
 * no voice for the requested language and media that could not be loaded. The source keeps the enhanced marker so a
 * re-render of the same container does not keep recreating a control that is known not to work.
 */
function markUnavailable(state: ControlState): void {
  // Only the failing control's own speech is cancelled; a media failure must not silence another card's utterance.
  if (activeSpeech?.state === state) stopSpeech();
  detachAudio(state);
  if (state.audio) {
    state.audio.removeAttribute('src');
    try {
      state.audio.load();
    } catch {
      // Loading an empty source is not required to succeed; the control is going away either way.
    }
    state.audio = null;
  }
  if (activeAudio === state) activeAudio = null;
  state.button.remove();
  controls.delete(state.button);
  showFallback(state);
  const message = label(LABEL_KEYS[state.kind].unavailable);
  state.source.setAttribute('title', message);
  const status = document.createElement('span');
  status.className = 'sr-only';
  status.setAttribute('role', 'status');
  status.textContent = message;
  state.source.appendChild(status);
}

/* ---------------------------------------------------------------------------------------------------------------- */
/* Speech                                                                                                            */
/* ---------------------------------------------------------------------------------------------------------------- */

function synthesis(): SpeechSynthesis | null {
  if (typeof window === 'undefined') return null;
  const synth = window.speechSynthesis;
  if (!synth || typeof window.SpeechSynthesisUtterance !== 'function') return null;
  return synth;
}

function voicesOf(synth: SpeechSynthesis): SpeechSynthesisVoice[] {
  try {
    return synth.getVoices() ?? [];
  } catch {
    return [];
  }
}

/**
 * The voice for a BCP-47 tag: an exact match first, then the same primary subtag (so `ja-JP` accepts a `ja` voice).
 * Voices load asynchronously in some browsers; an empty list means "not known yet", which the click path re-checks.
 */
function voiceFor(synth: SpeechSynthesis, lang: string): SpeechSynthesisVoice | null {
  const wanted = lang.toLowerCase();
  const voices = voicesOf(synth);
  const exact = voices.find((voice) => voice.lang.toLowerCase() === wanted);
  if (exact) return exact;
  const primary = wanted.split('-')[0] ?? wanted;
  return voices.find((voice) => (voice.lang.toLowerCase().split('-')[0] ?? '') === primary) ?? null;
}

/**
 * Whether a speech control can exist at all. Without the API the element keeps only its text and gets no button.
 * With the API but no voice for the requested language the same holds as soon as the voice list is populated; while
 * the list is still empty the control is created and the click path decides.
 */
function speechUsable(source: Element): boolean {
  const synth = synthesis();
  if (!synth) return false;
  const lang = source.getAttribute('data-tts-lang');
  if (!lang) return true;
  const voices = voicesOf(synth);
  return voices.length === 0 || voiceFor(synth, lang) !== null;
}

function stopSpeech(): void {
  const active = activeSpeech;
  if (!active) return;
  activeSpeech = null;
  active.utterance.onend = null;
  active.utterance.onerror = null;
  try {
    active.synth.cancel();
  } catch {
    // A missing or already-finished utterance has nothing to cancel.
  }
  resetPhase(active.state);
}

function finishSpeech(state: ControlState): void {
  if (activeSpeech?.state === state) {
    activeSpeech.utterance.onend = null;
    activeSpeech.utterance.onerror = null;
    activeSpeech = null;
  }
  resetPhase(state);
}

function toggleSpeech(state: ControlState): void {
  if (state.phase === 'playing') {
    stopSpeech();
    return;
  }
  const synth = synthesis();
  if (!synth) {
    markUnavailable(state);
    return;
  }
  const text = state.source.getAttribute('data-tts-text') ?? '';
  if (text === '') {
    markUnavailable(state);
    return;
  }
  const lang = state.source.getAttribute('data-tts-lang') || document.documentElement.lang || '';
  const voice = lang ? voiceFor(synth, lang) : null;
  if (lang && voicesOf(synth).length > 0 && !voice) {
    markUnavailable(state);
    return;
  }
  // One utterance at a time: a second card's speech replaces the first instead of overlapping it.
  stopSpeech();
  let utterance: SpeechSynthesisUtterance;
  try {
    utterance = new window.SpeechSynthesisUtterance(text);
  } catch {
    markUnavailable(state);
    return;
  }
  if (lang) utterance.lang = lang;
  if (voice) utterance.voice = voice;
  const rate = numberAttr(state.source, 'data-tts-rate');
  if (rate !== null) utterance.rate = clamp(rate, 0.1, 10);
  utterance.onend = () => finishSpeech(state);
  utterance.onerror = (event) => {
    // A cancel or an interruption is this module's own doing (card change, another click), not a failure to report.
    if (event.error === 'canceled' || event.error === 'interrupted') {
      finishSpeech(state);
      return;
    }
    markUnavailable(state);
  };
  state.phase = 'playing';
  activeSpeech = { state, synth, utterance };
  updateButton(state);
  try {
    synth.speak(utterance);
  } catch {
    markUnavailable(state);
  }
}

/* ---------------------------------------------------------------------------------------------------------------- */
/* Audio                                                                                                             */
/* ---------------------------------------------------------------------------------------------------------------- */

function stopAudio(state: ControlState): void {
  detachAudio(state);
  if (activeAudio === state) activeAudio = null;
  resetPhase(state);
}

function playAudio(state: ControlState): void {
  const audio = state.audio;
  if (!audio) {
    markUnavailable(state);
    return;
  }
  let result: Promise<void> | undefined;
  try {
    result = audio.play();
  } catch {
    markUnavailable(state);
    return;
  }
  // A rejected play means the bytes could not be decoded or fetched: the control goes, the text comes back. A
  // rejection that arrives after the user stopped playback (AbortError from the pause) is not a load failure.
  void Promise.resolve(result).catch(() => {
    if (state.phase === 'playing' && state.audio === audio) markUnavailable(state);
  });
}

function startAudio(state: ControlState): void {
  const audio = state.audio;
  const src = state.source.getAttribute('data-audio-src') ?? '';
  if (!audio || src === '') {
    markUnavailable(state);
    return;
  }
  if (activeAudio) stopAudio(activeAudio);

  const start = numberAttr(state.source, 'data-audio-start') ?? 0;
  const endValue = numberAttr(state.source, 'data-audio-end');
  const end = endValue !== null && endValue > start ? endValue : null;
  const rate = numberAttr(state.source, 'data-audio-rate');
  state.loop = state.source.hasAttribute('data-audio-loop');

  // The seek happens in the same click that starts playback. Assigning currentTime before metadata is best-effort;
  // the loadedmetadata listener repeats it once the duration is known.
  const seek = (): void => {
    if (start <= 0) return;
    try {
      audio.currentTime = start;
    } catch {
      // Some engines refuse an early seek; the loadedmetadata pass runs again.
    }
  };

  audio.pause();
  audio.src = src;
  try {
    audio.load();
  } catch {
    // Not every engine implements load(); assigning src already starts the fetch.
  }
  audio.playbackRate = rate === null ? 1 : clamp(rate, 0.1, 16);
  // The loop flag is honoured as "loop the start..end segment", handled below instead of the element's whole-file loop.
  audio.loop = false;
  seek();

  const handlers: AudioHandlers = {
    loadedmetadata: seek,
    timeupdate: () => {
      if (end === null) return;
      if (audio.currentTime < end) return;
      if (state.loop) {
        seek();
        return;
      }
      stopAudio(state);
    },
    ended: () => {
      if (!state.loop) {
        stopAudio(state);
        return;
      }
      seek();
      playAudio(state);
    },
    error: () => markUnavailable(state),
  };
  state.handlers = handlers;
  audio.addEventListener('loadedmetadata', handlers.loadedmetadata, { once: true });
  audio.addEventListener('timeupdate', handlers.timeupdate);
  audio.addEventListener('ended', handlers.ended);
  audio.addEventListener('error', handlers.error);

  state.phase = 'playing';
  activeAudio = state;
  updateButton(state);
  playAudio(state);
}

function toggleAudio(state: ControlState): void {
  if (state.phase === 'playing') {
    stopAudio(state);
    return;
  }
  startAudio(state);
}

/* ---------------------------------------------------------------------------------------------------------------- */
/* Binding                                                                                                           */
/* ---------------------------------------------------------------------------------------------------------------- */

function createControl(source: HTMLElement, kind: ControlKind): void {
  const button = document.createElement('button');
  button.type = 'button';
  button.setAttribute(BUTTON_MARKER, kind);
  const state: ControlState = {
    kind,
    source,
    button,
    phase: 'idle',
    fallback: null,
    audio: null,
    handlers: null,
    loop: false,
  };
  if (kind === 'audio') {
    state.fallback = hideFallback(source);
    state.audio = document.createElement('audio');
    state.audio.preload = 'none';
  }
  updateButton(state);
  source.insertBefore(button, source.firstChild);
  controls.set(button, state);
  source.setAttribute(SOURCE_MARKER, kind);
}

function bindSource(source: HTMLElement): void {
  if (source.hasAttribute(SOURCE_MARKER)) return;
  // A control nested in an interactive element would be a control inside a control: the text stays, the button does not.
  if (source.closest(INTERACTIVE_SELECTOR)) return;
  const kind: ControlKind = source.hasAttribute('data-audio-src') ? 'audio' : 'tts';
  if (kind === 'tts') {
    if ((source.getAttribute('data-tts-text') ?? '') === '') return;
    if (!speechUsable(source)) return;
  }
  createControl(source, kind);
}

/** Stops speech and audio whose source element left the document, which is what a card change or an unmount does. */
function reconcilePlayback(): void {
  if (activeSpeech && !activeSpeech.state.source.isConnected) stopSpeech();
  if (activeAudio && !activeAudio.source.isConnected) stopAudio(activeAudio);
}

/** The delegated handler: every container gets exactly one of these, and buttons carry no listener of their own. */
function handleClick(event: Event): void {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest<HTMLButtonElement>(`button[${BUTTON_MARKER}]`);
  if (!button) return;
  const state = controls.get(button);
  if (!state) return;
  event.preventDefault();
  event.stopPropagation();
  if (state.kind === 'tts') toggleSpeech(state);
  else toggleAudio(state);
}

/**
 * enhanceShortcodes attaches controls to every shortcode element below the given server-sanitized containers.
 *
 * Safe to call twice on the same DOM and safe to call on a re-rendered container: the marker attribute prevents a
 * second button on a shortcode element, and the WeakSet keeps one delegated listener per container. Nullish nodes are
 * ignored so callers can pass the same list they handed to `typeset`.
 */
export function enhanceShortcodes(nodes: Array<Element | null | undefined>): void {
  if (typeof document === 'undefined') return;
  const containers = nodes.filter((node): node is Element => node != null);
  if (containers.length === 0) return;
  reconcilePlayback();
  for (const container of containers) {
    if (!boundContainers.has(container)) {
      container.addEventListener('click', handleClick);
      boundContainers.add(container);
    }
    // Labels follow the active locale even when a card face is not re-rendered.
    for (const button of container.querySelectorAll<HTMLButtonElement>(`button[${BUTTON_MARKER}]`)) {
      const state = controls.get(button);
      if (state) updateButton(state);
    }
    for (const source of container.querySelectorAll<HTMLElement>('[data-tts-text], [data-audio-src]')) {
      bindSource(source);
    }
  }
}

/** Stops any playback this module started; views call it when they unmount so speech does not outlive the page. */
export function stopShortcodePlayback(): void {
  stopSpeech();
  if (activeAudio) stopAudio(activeAudio);
}
