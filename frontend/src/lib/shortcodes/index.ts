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
 * moved back into view when the media turns out to be unplayable. Beside the audio button sits a native range track
 * over the played segment, so the learner can see the position and seek within the same piece of audio. A pair of
 * five-second buttons around the play button moves within that same segment, and a `m:ss` readout after the track
 * names the elapsed position and the segment length.
 *
 * The audio play button pauses rather than stops: a pause keeps the media element, its source and its position, so
 * the next click resumes the same element where it stood. Only an end of segment, a media error, a teardown, or a
 * different piece of audio starting resets a control to its segment start.
 *
 * A tts element whose `data-tts-hide="1"` flag is present hides its body the same way, but only once a speech control
 * exists: the text stays in a visually hidden wrapper, so assistive technology still reads it and the card still says
 * what was written, while the control takes the line. Without the flag the body is never hidden, and a control that is
 * removed because speech turned out to be unavailable puts the body back into view.
 *
 * Idempotence: the marker attribute `data-shortcode-enhanced` on a shortcode element records that it was processed,
 * and a WeakSet records which containers already carry the delegated listeners. A second enhancement of the same DOM
 * therefore adds neither a second button nor a second track nor a second listener, and re-rendering a container with
 * new server HTML works because the listeners live on the container, not on the elements inside it.
 */

import { formatMessage, getLocale } from '../i18n';

const SOURCE_MARKER = 'data-shortcode-enhanced';
const BUTTON_MARKER = 'data-shortcode-control';
const FALLBACK_MARKER = 'data-shortcode-fallback';
const BODY_MARKER = 'data-shortcode-body';
const PROGRESS_MARKER = 'data-shortcode-progress';
const PLAYER_MARKER = 'data-shortcode-player';
const SEEK_MARKER = 'data-shortcode-seek';
const TIME_MARKER = 'data-shortcode-time';
const SVG_NS = 'http://www.w3.org/2000/svg';

/**
 * The progress track is a native range input whose value is a whole-number ratio over the played segment. A 0-1000
 * scale keeps a click or a drag to a tenth of a percent; the keyboard step is fixed separately at five percent,
 * because a native arrow press would otherwise move by one unit of the scale.
 */
const PROGRESS_MAX = 1000;
const PROGRESS_KEY_STEP = PROGRESS_MAX / 20;

/** One press of a seek button moves this far, and the buttons switch off when less than the tolerance remains. */
const SEEK_STEP = 5;
const SEEK_EDGE_TOLERANCE = 0.25;

/**
 * A shortcode inside one of these elements cannot host a control: a button inside a button (or a link) is invalid
 * markup and splits one control into two focus stops for keyboard users. The review page's option rows are such
 * controls, so their shortcodes keep the visible text and get no control of their own.
 */
const INTERACTIVE_SELECTOR =
  'button, a[href], [role="button"], [role="link"], [role="radio"], [role="checkbox"], [role="switch"], [role="tab"], [role="menuitem"]';

const LABEL_KEYS = {
  tts: {
    play: 'shortcode.tts.play',
    active: 'shortcode.tts.stop',
    resume: 'shortcode.tts.play',
    unavailable: 'shortcode.tts.unavailable',
  },
  audio: {
    play: 'shortcode.audio.play',
    active: 'shortcode.audio.pause',
    resume: 'shortcode.audio.resume',
    unavailable: 'shortcode.audio.unavailable',
  },
} as const;

/**
 * The control is a 40px square hit box whose circle is drawn on top of it at 1.25em of the surrounding text; the
 * icon is 0.875em. `align-middle` puts the box on the text's centre line, so the circle shares that centre without a
 * negative block margin. A line that holds a control grows to the hit box height, which keeps the whole pointer target
 * inside its own line: the button paints a small circle in the middle of the box, never a 40px circle, and never
 * reaches into the line above or below. The circle is capped at the box size so atypically large text (a heading that
 * contains a shortcode) cannot make it paint outside the line either.
 */
const BUTTON_BASE = [
  'relative isolate inline-flex size-10 shrink-0 cursor-pointer items-center justify-center align-middle transition-colors',
  'before:absolute before:inset-0 before:-z-10 before:m-auto before:size-[min(1.25em,2.5rem)] before:rounded-full before:border',
  'before:transition-colors before:content-[""]',
  'focus-visible:outline-none focus-visible:before:ring-2 focus-visible:before:ring-ring focus-visible:before:ring-offset-2 focus-visible:before:ring-offset-background',
].join(' ');
const BUTTON_IDLE = `${BUTTON_BASE} text-muted-foreground before:border-border before:bg-surface hover:text-foreground hover:before:bg-muted`;
const BUTTON_PLAYING = `${BUTTON_BASE} text-brand-foreground before:border-brand before:bg-brand`;

/**
 * The audio player is one inline group: a back button, the play button, a forward button, then the track and the
 * readout. A paragraph cannot break inside an inline-level box, so the group moves to the next line as a whole instead
 * of being split by the surrounding text. Inside the group the track and the readout share a box, so the only internal
 * break puts that box under the three buttons; that happens only on a card too narrow for the entire player, and it
 * keeps every target on screen.
 */
const PLAYER_CLASS = 'inline-flex max-w-full flex-wrap items-center gap-x-1 gap-y-1 align-middle';

/**
 * The seek buttons share the play button's 40px hit box and 1.25em circle, so all three targets sit on the text's
 * centre line and none reaches into the line above or below; only their icon is smaller and only the play button is
 * ever filled with the brand colour, which leaves one obvious primary action. A disabled button keeps its box but
 * fades, so the pair does not shift when a segment edge is reached.
 */
const SEEK_BUTTON_CLASS = [
  BUTTON_BASE,
  'text-muted-foreground before:border-border before:bg-surface hover:text-foreground hover:before:bg-muted',
  'disabled:cursor-not-allowed disabled:opacity-40',
].join(' ');

/**
 * The audio progress track: a native range on the same centre line as the button circles. It is 8rem wide, and 10rem
 * from the `lg` breakpoint, so a short segment is still easy to aim at; when the line is short the track is the item
 * that gives way. The unfilled part is the muted token and the filled part the brand token; the fill boundary is an
 * inline gradient written from the current ratio, which a class name cannot express.
 */
const PROGRESS_CLASS = [
  'h-1.5 w-32 min-w-0 shrink cursor-pointer appearance-none rounded-full bg-muted align-middle transition-opacity lg:w-40',
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background',
  'disabled:cursor-not-allowed disabled:opacity-50',
  '[&::-webkit-slider-thumb]:size-2.5 [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:border-0 [&::-webkit-slider-thumb]:bg-brand',
  '[&::-moz-range-track]:bg-transparent [&::-moz-range-progress]:bg-transparent',
  '[&::-moz-range-thumb]:size-2.5 [&::-moz-range-thumb]:rounded-full [&::-moz-range-thumb]:border-0 [&::-moz-range-thumb]:bg-brand',
].join(' ');

/** The track and its readout move together when the group wraps, so the time never lands on a line of its own. */
const TRACK_GROUP_CLASS = 'inline-flex min-w-0 items-center gap-1.5 align-middle';

/**
 * The readout is em-based so it follows the card's own text size, and tabular so a digits change does not change its
 * width. It repeats what the track's `aria-valuetext` already announces, so assistive technology is told nothing twice.
 */
const TIME_CLASS = 'shrink-0 whitespace-nowrap text-[0.75em] leading-none tabular-nums text-muted-foreground';

/** Placeholder for a length that is not known yet; a segment needs metadata before it has one. */
const TIME_PLACEHOLDER = '--:--';

type ControlKind = 'tts' | 'audio';
/**
 * `paused` exists only for audio: it is the state in which the media element still holds its position but is not
 * advancing. Tts has no paused state, because a stopped utterance cannot be resumed.
 */
type Phase = 'idle' | 'playing' | 'paused';

/** The part of the file the track spans: `start…end`, or `start…duration` when the shortcode has no end. */
interface AudioSegment {
  start: number;
  end: number | null;
}

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
  /**
   * The visually hidden wrapper that holds the text a control replaced: the media path for audio, the body for a
   * tts element that carries the hide flag. Null when nothing is hidden.
   */
  fallback: HTMLElement | null;
  audio: HTMLAudioElement | null;
  handlers: AudioHandlers | null;
  loop: boolean;
  /** For audio: the inline group that holds the three buttons, the track and the readout. */
  group: HTMLElement | null;
  /** For audio: the seekable track beside the button, absent only after a load failure removed the control. */
  track: HTMLInputElement | null;
  /** For audio: the five-second buttons, disabled at their edge of the segment and before metadata arrives. */
  seekBack: HTMLButtonElement | null;
  seekForward: HTMLButtonElement | null;
  /** For audio: the visible `position / total` readout; aria-hidden, because the track already announces the value. */
  time: HTMLSpanElement | null;
  /** For audio: the file segment the track spans, read from the server's start/end attributes. */
  segment: AudioSegment | null;
  /** For audio: whether the media metadata has arrived, which is what makes the track seekable. */
  ready: boolean;
  /** For audio: a position chosen while stopped, used by the next play instead of the segment start. */
  pendingSeek: number | null;
  /**
   * For audio: which play attempt is current. A rejected `play()` promise removes the control only while its own
   * attempt is still the latest one; a pause followed by a resume produces a new attempt, and the older rejection is
   * the pause's own doing rather than a load failure.
   */
  playToken: number;
}

interface ActiveSpeech {
  state: ControlState;
  synth: SpeechSynthesis;
  utterance: SpeechSynthesisUtterance;
}

/** Containers that already carry the delegated listener. */
const boundContainers = new WeakSet<Element>();
/** State of every control, keyed by its button. */
const controls = new WeakMap<HTMLButtonElement, ControlState>();
/** State of every audio progress track, keyed by the track itself. */
const tracks = new WeakMap<HTMLInputElement, ControlState>();
/** The state and step behind each seek button, keyed by the button. */
const seeks = new WeakMap<HTMLButtonElement, { state: ControlState; delta: number }>();

/** At most one utterance and one audio play at a time, across the whole page. */
let activeSpeech: ActiveSpeech | null = null;
let activeAudio: ControlState | null = null;

function label(key: string, params?: Record<string, string | number>): string {
  return formatMessage(getLocale(), key, params);
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

type IconName = 'speak' | 'stop' | 'play' | 'pause' | 'rewind' | 'forward';

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
  rewind: [
    {
      tag: 'path',
      attrs: {
        d: 'M11 17 6 12l5-5M18 17l-5-5 5-5',
        fill: 'none',
        stroke: 'currentColor',
        'stroke-width': '2',
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
      },
    },
  ],
  forward: [
    {
      tag: 'path',
      attrs: {
        d: 'M13 7l5 5-5 5M6 7l5 5-5 5',
        fill: 'none',
        stroke: 'currentColor',
        'stroke-width': '2',
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
      },
    },
  ],
};

/** Builds the icon from constant attribute maps; no card text ever passes through this path. */
function makeIcon(name: IconName, size = 'size-[0.875em]'): SVGSVGElement {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  // The em-based class is what sizes the icon; the numeric attributes are the no-CSS fallback.
  svg.setAttribute('width', '14');
  svg.setAttribute('height', '14');
  svg.setAttribute('class', size);
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
  const text = label(playing ? keys.active : state.phase === 'paused' ? keys.resume : keys.play);
  state.button.className = playing ? BUTTON_PLAYING : BUTTON_IDLE;
  state.button.setAttribute('aria-pressed', playing ? 'true' : 'false');
  state.button.setAttribute('aria-label', text);
  state.button.setAttribute('title', text);
  const icon: IconName = state.kind === 'tts' ? (playing ? 'stop' : 'speak') : playing ? 'pause' : 'play';
  state.button.replaceChildren(makeIcon(icon));
}

/**
 * Moves every child of the source into a visually hidden wrapper and keeps it in the DOM: the text stays available to
 * assistive technology while the control takes the line. Nothing is deleted, so the same text is restored by removing
 * the hidden class. `marker` records what was hidden: the media path for audio, the body for a tts element with the
 * hide flag.
 */
function hideIntoWrapper(source: HTMLElement, marker: string): HTMLElement {
  const wrapper = document.createElement('span');
  wrapper.className = 'sr-only';
  wrapper.setAttribute(marker, '');
  while (source.firstChild) wrapper.appendChild(source.firstChild);
  source.appendChild(wrapper);
  return wrapper;
}

/** Puts the hidden text back into normal flow. The wrapper stays; without the hidden class it is an inline span. */
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
  // Removing the group takes the three buttons, the track and the readout with it; the fallback text sits outside it.
  state.group?.remove();
  state.group = null;
  if (state.track) {
    tracks.delete(state.track);
    state.track = null;
  }
  for (const seek of [state.seekBack, state.seekForward]) {
    if (seek) seeks.delete(seek);
  }
  state.seekBack = null;
  state.seekForward = null;
  state.time = null;
  state.segment = null;
  state.ready = false;
  state.pendingSeek = null;
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

/** Reads the segment the server described; an end that is not after the start means "no end". */
function audioSegment(source: HTMLElement): AudioSegment {
  const start = Math.max(0, numberAttr(source, 'data-audio-start') ?? 0);
  const endValue = numberAttr(source, 'data-audio-end');
  return { start, end: endValue !== null && endValue > start ? endValue : null };
}

/**
 * Length of the segment in seconds. Zero means the track cannot be measured yet, or the segment is empty; every
 * caller checks for zero before dividing by it.
 */
function segmentLength(segment: AudioSegment, duration: number): number {
  const boundary = segment.end ?? (Number.isFinite(duration) && duration > 0 ? duration : null);
  if (boundary === null) return 0;
  return Math.max(0, boundary - segment.start);
}

/** One decimal place without a trailing `.0`; the catalog supplies the unit. */
function seconds(value: number): string {
  return String(Math.round(Math.max(0, value) * 10) / 10);
}

/**
 * The visible clock. Below an hour it is `m:ss`; a segment of an hour or more is written `h:mm:ss` for the position
 * and the length alike, because a readout whose shape changes halfway through the segment is harder to read. The
 * reference is the segment length, so a position and its total always share one format.
 */
function clock(value: number, reference: number): string {
  const whole = Math.max(0, Math.floor(value + 1e-6));
  const pad = (part: number): string => String(part).padStart(2, '0');
  const minutes = Math.floor((whole % 3600) / 60);
  const secs = whole % 60;
  if (reference >= 3600) return `${Math.floor(whole / 3600)}:${pad(minutes)}:${pad(secs)}`;
  return `${minutes}:${pad(secs)}`;
}

/**
 * Where the player stands, on the segment's own timeline. A stopped control has no position of its own any more: its
 * media element may sit at the segment end, while the next play will start at the chosen seek or at the segment start.
 * Every caller that draws the track, the readout or the seek buttons reads this one value, so they agree with each
 * other and with the position the next play uses.
 */
function anchorPosition(state: ControlState): number {
  const segment = state.segment;
  if (!segment) return 0;
  if (state.phase === 'idle') return state.pendingSeek ?? segment.start;
  return state.audio?.currentTime ?? segment.start;
}

/**
 * Builds the native range that represents the segment. It starts disabled: the media element exists but carries no
 * source until the first play, and a track that cannot seek must not announce a position it does not have.
 */
function buildProgress(): HTMLInputElement {
  const track = document.createElement('input');
  track.type = 'range';
  track.min = '0';
  track.max = String(PROGRESS_MAX);
  track.step = '1';
  track.value = '0';
  track.disabled = true;
  track.className = PROGRESS_CLASS;
  track.setAttribute(PROGRESS_MARKER, 'audio');
  track.setAttribute('aria-label', label('shortcode.audio.progress'));
  track.style.backgroundImage = 'linear-gradient(to right, var(--brand) 0%, var(--muted) 0%)';
  return track;
}

/** The readout starts masked: no length is known until the media reports its duration. */
function buildTime(): HTMLSpanElement {
  const time = document.createElement('span');
  time.className = TIME_CLASS;
  time.setAttribute(TIME_MARKER, '');
  // The track carries the same information in `aria-valuetext`; announcing it twice would repeat every second.
  time.setAttribute('aria-hidden', 'true');
  time.textContent = `${clock(0, 0)} / ${TIME_PLACEHOLDER}`;
  return time;
}

/** Names a seek button after the step it takes; the sign of the step is the only thing that differs between the pair. */
function updateSeekLabel(button: HTMLButtonElement, delta: number): void {
  const text = label(delta < 0 ? 'shortcode.audio.back' : 'shortcode.audio.forward', { seconds: Math.abs(delta) });
  button.setAttribute('aria-label', text);
  button.setAttribute('title', text);
}

/**
 * Builds one seek button. It starts disabled because the media has no metadata yet; the label carries the step so the
 * control is understandable without the icon, and a native button gives it Enter and Space for free.
 */
function buildSeek(state: ControlState, delta: number): HTMLButtonElement {
  const button = document.createElement('button');
  button.type = 'button';
  button.setAttribute(SEEK_MARKER, String(delta));
  button.className = SEEK_BUTTON_CLASS;
  button.disabled = true;
  button.appendChild(makeIcon(delta < 0 ? 'rewind' : 'forward', 'size-[0.75em]'));
  seeks.set(button, { state, delta });
  updateSeekLabel(button, delta);
  return button;
}

/**
 * The segment's seekable bounds, or null while they cannot be measured. `position` is the anchor clamped into them, so
 * the edge tests never depend on how the media engine rounded `currentTime`.
 */
function seekBounds(state: ControlState): { min: number; max: number; position: number } | null {
  const audio = state.audio;
  const segment = state.segment;
  if (!state.ready || !audio || !segment) return null;
  const length = segmentLength(segment, audio.duration);
  if (length <= 0) return null;
  const min = segment.start;
  const max = segment.start + length;
  return { min, max, position: clamp(anchorPosition(state), min, max) };
}

/** Sets the edge state of the two seek buttons: off before metadata, and at the end each of them would reach. */
function updateSeekButtons(state: ControlState): void {
  const bounds = seekBounds(state);
  if (state.seekBack) {
    state.seekBack.disabled = bounds === null || bounds.position <= bounds.min + SEEK_EDGE_TOLERANCE;
  }
  if (state.seekForward) {
    state.seekForward.disabled = bounds === null || bounds.position >= bounds.max - SEEK_EDGE_TOLERANCE;
  }
}

/**
 * Draws the track, the readout and the seek buttons from the anchor. All three are written here because every event
 * that moves the position - metadata, timeupdate, a seek, a stop - has to leave them in agreement.
 */
function renderProgress(state: ControlState): void {
  const track = state.track;
  const audio = state.audio;
  if (!track) return;
  const segment = state.segment;
  const length = audio && segment ? segmentLength(segment, audio.duration) : 0;
  const measured = audio && segment && length > 0 ? anchorPosition(state) - segment.start : 0;
  const position = clamp(measured, 0, length);
  const ratio = length > 0 ? clamp(position / length, 0, 1) : 0;
  const percent = Math.round(ratio * 1000) / 10;
  track.value = String(Math.round(ratio * PROGRESS_MAX));
  track.style.backgroundImage = `linear-gradient(to right, var(--brand) ${percent}%, var(--muted) ${percent}%)`;
  track.disabled = !state.ready;
  if (state.ready && length > 0) {
    track.setAttribute(
      'aria-valuetext',
      label('shortcode.audio.progress.value', { position: seconds(position), total: seconds(length) })
    );
  } else {
    track.removeAttribute('aria-valuetext');
  }
  if (state.time) {
    state.time.textContent =
      state.ready && length > 0
        ? `${clock(position, length)} / ${clock(length, length)}`
        : `${clock(0, 0)} / ${TIME_PLACEHOLDER}`;
  }
  updateSeekButtons(state);
}

/**
 * Moves the element to `target`, clamped into the played segment. A seek taken while stopped is remembered as the next
 * play's position; a seek while playing or paused is already the element's own position.
 */
function seekTo(state: ControlState, target: number): void {
  const audio = state.audio;
  const segment = state.segment;
  if (!state.ready || !audio || !segment) return;
  const length = segmentLength(segment, audio.duration);
  if (length <= 0) return;
  const time = clamp(target, segment.start, segment.start + length);
  try {
    audio.currentTime = time;
  } catch {
    return;
  }
  if (state.phase === 'idle') state.pendingSeek = time;
  renderProgress(state);
}

/** Moves playback to the ratio the track shows; the caller has already clamped it. */
function seekProgress(state: ControlState, ratio: number): void {
  const audio = state.audio;
  const segment = state.segment;
  if (!state.ready || !audio || !segment) return;
  const length = segmentLength(segment, audio.duration);
  if (length <= 0) return;
  seekTo(state, segment.start + clamp(ratio, 0, 1) * length);
}

/** One press of a seek button; the edges are clamped by `seekTo`, and the buttons are disabled there anyway. */
function seekBy(state: ControlState, delta: number): void {
  const bounds = seekBounds(state);
  if (!bounds) return;
  seekTo(state, bounds.position + delta);
}

/** Puts the track at the segment start, which is where the next play will begin; the media element is left alone. */
function resetProgress(state: ControlState): void {
  state.pendingSeek = null;
  renderProgress(state);
}

function stopAudio(state: ControlState): void {
  detachAudio(state);
  if (activeAudio === state) activeAudio = null;
  resetPhase(state);
  resetProgress(state);
}

/**
 * Pauses without detaching or resetting: the element keeps its source, its handlers and its position, so the track,
 * the readout and the next play all still point at where playback stood.
 */
function pauseAudio(state: ControlState): void {
  if (state.phase !== 'playing') return;
  state.phase = 'paused';
  // The pause may reject the pending play() promise; that attempt is no longer the one the control reports on.
  state.playToken += 1;
  try {
    state.audio?.pause();
  } catch {
    // A detached element has nothing to pause; the state change is what the control shows either way.
  }
  updateButton(state);
  renderProgress(state);
}

/** Resumes the same element from its paused position: no new source, no load, no seek back to the segment start. */
function resumeAudio(state: ControlState): void {
  if (state.phase !== 'paused') return;
  // The control reports playing while the promise is pending, so a rejection of this attempt counts as a load failure;
  // the attempt token taken inside playAudio is what tells it apart from an abort of an older attempt.
  state.phase = 'playing';
  updateButton(state);
  renderProgress(state);
  playAudio(state);
}

function playAudio(state: ControlState): void {
  const audio = state.audio;
  if (!audio) {
    markUnavailable(state);
    return;
  }
  const token = ++state.playToken;
  let result: Promise<void> | undefined;
  try {
    result = audio.play();
  } catch {
    markUnavailable(state);
    return;
  }
  // A rejected play means the bytes could not be decoded or fetched: the control goes, the text comes back. A
  // rejection that arrives for an attempt a pause or a later play has superseded is that action's own doing, not a
  // load failure, so only the current attempt may remove the control.
  void Promise.resolve(result).catch(() => {
    if (token !== state.playToken) return;
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

  const segment = audioSegment(state.source);
  const end = segment.end;
  const rate = numberAttr(state.source, 'data-audio-rate');
  state.loop = state.source.hasAttribute('data-audio-loop');
  state.segment = segment;
  state.ready = false;
  // A track seek made while stopped wins over the segment start; it is consumed by this play only.
  const initial = state.pendingSeek ?? segment.start;
  state.pendingSeek = null;

  // The seek happens in the same click that starts playback. Assigning currentTime before metadata is best-effort;
  // the loadedmetadata listener repeats it once the duration is known.
  const seekElement = (time: number): void => {
    try {
      audio.currentTime = time;
    } catch {
      // Some engines refuse an early seek; the loadedmetadata pass runs again.
    }
  };
  const seekInitial = (): void => {
    if (initial > 0) seekElement(initial);
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
  seekInitial();
  renderProgress(state);

  const handlers: AudioHandlers = {
    loadedmetadata: () => {
      state.ready = true;
      seekInitial();
      renderProgress(state);
    },
    timeupdate: () => {
      // A paused element reports a position only because the learner seeked it. Reaching the end there is where they
      // put it, not playback finishing, so the control must stay paused at that position instead of resetting.
      if (state.phase === 'paused') {
        renderProgress(state);
        return;
      }
      if (end !== null && audio.currentTime >= end) {
        if (state.loop) {
          seekElement(segment.start);
          renderProgress(state);
          return;
        }
        stopAudio(state);
        return;
      }
      renderProgress(state);
    },
    ended: () => {
      if (!state.loop) {
        stopAudio(state);
        return;
      }
      seekElement(segment.start);
      renderProgress(state);
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
    pauseAudio(state);
    return;
  }
  if (state.phase === 'paused') {
    resumeAudio(state);
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
    group: null,
    track: null,
    seekBack: null,
    seekForward: null,
    time: null,
    segment: null,
    ready: false,
    pendingSeek: null,
    playToken: 0,
  };
  if (kind === 'audio') {
    state.fallback = hideIntoWrapper(source, FALLBACK_MARKER);
    state.audio = document.createElement('audio');
    state.audio.preload = 'none';
  } else if (source.getAttribute('data-tts-hide') === '1') {
    // The hide flag is the card author asking for the body to give way to the control; the wrapper keeps the text in
    // the DOM, and markUnavailable puts it back into view when the control cannot work.
    state.fallback = hideIntoWrapper(source, BODY_MARKER);
  }
  updateButton(state);
  if (kind === 'audio') {
    // The group keeps the three buttons, the track and the readout together on one line of card text. The track and
    // the readout share a nested box so a wrap moves them as one piece instead of leaving the time alone on a line.
    const group = document.createElement('span');
    group.className = PLAYER_CLASS;
    group.setAttribute(PLAYER_MARKER, 'audio');
    const trackGroup = document.createElement('span');
    trackGroup.className = TRACK_GROUP_CLASS;
    state.segment = audioSegment(source);
    state.track = buildProgress();
    state.time = buildTime();
    state.seekBack = buildSeek(state, -SEEK_STEP);
    state.seekForward = buildSeek(state, SEEK_STEP);
    trackGroup.append(state.track, state.time);
    group.append(state.seekBack, button, state.seekForward, trackGroup);
    state.group = group;
    source.insertBefore(group, source.firstChild);
    tracks.set(state.track, state);
    renderProgress(state);
  } else {
    source.insertBefore(button, source.firstChild);
  }
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
  const seekButton = target.closest<HTMLButtonElement>(`button[${SEEK_MARKER}]`);
  if (seekButton) {
    const action = seeks.get(seekButton);
    if (!action) return;
    event.preventDefault();
    event.stopPropagation();
    // A disabled button swallows its own clicks in a browser; the guard also covers a synthetic dispatch.
    if (seekButton.disabled) return;
    seekBy(action.state, action.delta);
    return;
  }
  const button = target.closest<HTMLButtonElement>(`button[${BUTTON_MARKER}]`);
  if (!button) return;
  const state = controls.get(button);
  if (!state) return;
  event.preventDefault();
  event.stopPropagation();
  if (state.kind === 'tts') toggleSpeech(state);
  else toggleAudio(state);
}

/** The state of the track an event came from, or null when the target is some other element. */
function trackStateOf(target: EventTarget | null): { track: HTMLInputElement; state: ControlState } | null {
  if (!(target instanceof HTMLInputElement)) return null;
  const state = tracks.get(target);
  if (!state || state.track !== target) return null;
  return { track: target, state };
}

/** A click or a drag on a native range has already set its value; this turns that value into a media seek. */
function handleProgressInput(event: Event): void {
  const found = trackStateOf(event.target);
  if (!found) return;
  seekProgress(found.state, Number(found.track.value) / PROGRESS_MAX);
}

/**
 * Left/Right move by five percent of the segment and Home/End go to its edges, as the acceptance requires. The
 * native press is cancelled because a native range would move by one unit of the 0-1000 click scale instead.
 */
function handleProgressKey(event: Event): void {
  if (!(event instanceof KeyboardEvent)) return;
  const found = trackStateOf(event.target);
  if (!found) return;
  const current = Number(found.track.value);
  let next: number;
  switch (event.key) {
    case 'ArrowLeft':
    case 'ArrowDown':
      next = current - PROGRESS_KEY_STEP;
      break;
    case 'ArrowRight':
    case 'ArrowUp':
      next = current + PROGRESS_KEY_STEP;
      break;
    case 'Home':
      next = 0;
      break;
    case 'End':
      next = PROGRESS_MAX;
      break;
    default:
      return;
  }
  event.preventDefault();
  found.track.value = String(clamp(Math.round(next), 0, PROGRESS_MAX));
  seekProgress(found.state, Number(found.track.value) / PROGRESS_MAX);
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
      container.addEventListener('input', handleProgressInput);
      container.addEventListener('keydown', handleProgressKey);
      boundContainers.add(container);
    }
    // Labels follow the active locale even when a card face is not re-rendered.
    for (const button of container.querySelectorAll<HTMLButtonElement>(`button[${BUTTON_MARKER}]`)) {
      const state = controls.get(button);
      if (state) updateButton(state);
    }
    for (const seek of container.querySelectorAll<HTMLButtonElement>(`button[${SEEK_MARKER}]`)) {
      const action = seeks.get(seek);
      if (action) updateSeekLabel(seek, action.delta);
    }
    for (const track of container.querySelectorAll<HTMLInputElement>(`input[${PROGRESS_MARKER}]`)) {
      const state = tracks.get(track);
      if (!state) continue;
      track.setAttribute('aria-label', label('shortcode.audio.progress'));
      renderProgress(state);
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
