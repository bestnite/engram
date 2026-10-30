import $ from 'jquery';
import type { AVTag } from '../lib/api/types';

/**
 * The Anki-compatible reviewer environment of the card frame.
 *
 * A card template is written against the desktop reviewer: it expects the body classes `card cardN`, the browser and
 * platform classes Anki's `ts/reviewer/browser_selector.ts` adds at tag 26.09.3, the globals `$`, `jQuery`,
 * `getTypedAnswer`, `pycmd` and `bridgeCommand`, and answer inputs that hand their value back when the learner is done.
 * This module supplies that surface inside the sandboxed frame and nothing else; scheduling, grading and the rest of the
 * review page stay in the parent.
 */

/** The dark-theme body classes Anki's reviewer sets next to `card cardN`. */
const NIGHT_CLASSES = ['nightMode', 'night_mode'];

/**
 * browserClasses is a direct port of Anki's `addBrowserClasses` at tag 26.09.3: the same regular expressions, the same
 * else-if order, and the same class names, so a card stylesheet written for Anki selects the same elements here.
 */
export function browserClasses(userAgent: string): string[] {
  const ua = userAgent.toLowerCase();
  const classes: string[] = [];
  const test = (regex: RegExp): boolean => regex.test(ua);
  const add = (name: string): void => {
    if (!classes.includes(name)) classes.push(name);
  };

  if (test(/ipad/)) add('ipad');
  else if (test(/iphone/)) add('iphone');
  else if (test(/android/)) add('android');

  if (test(/ipad|iphone|ipod/)) add('ios');

  if (test(/ipad|iphone|ipod|android/)) add('mobile');
  else if (test(/linux/)) add('linux');
  else if (test(/windows/)) add('win');
  else if (test(/mac/)) add('mac');

  if (test(/firefox\//)) add('firefox');
  else if (test(/chrome\//)) add('chrome');
  else if (test(/safari\//)) add('safari');

  return classes;
}

/**
 * platformClasses names the operating system family the way Anki's desktop webview does. Anki knows the platform
 * directly; inside a browser frame only the user agent is available, so the mapping follows the same branches as the
 * browser classes: a mobile user agent (iPad/iPhone/Android) is neither a Mac nor Windows desktop, a `windows` token is
 * Windows, a `mac` token is macOS and everything else is Linux.
 */
export function platformClasses(userAgent: string): string[] {
  const ua = userAgent.toLowerCase();
  if (/(ipad|iphone|ipod|android)/.test(ua)) return ['isLin'];
  if (/windows/.test(ua)) return ['isWin'];
  if (/mac/.test(ua)) return ['isMac'];
  return ['isLin'];
}

/** Applies the browser and platform classes to the frame's own `<html>` element. */
export function applyBrowserClasses(root: Element, userAgent: string): void {
  for (const name of [...browserClasses(userAgent), ...platformClasses(userAgent)]) {
    root.classList.add(name);
  }
}

/**
 * bodyClassName composes the reviewer body classes: the server's base class (the note type's `card`), `cardN` with
 * N = the card ordinal + 1 as Anki writes it, and the two dark-theme spellings when the theme is dark.
 */
export function bodyClassName(base: string, ordinal: number, dark: boolean): string {
  const classes: string[] = [];
  const add = (name: string): void => {
    if (name !== '' && !classes.includes(name)) classes.push(name);
  };
  for (const name of base.split(/\s+/)) add(name);
  add('card');
  add(`card${Math.max(0, Math.trunc(ordinal)) + 1}`);
  if (dark) for (const name of NIGHT_CLASSES) add(name);
  return classes.join(' ');
}

/** One answer value posted by a control: the shapes internal/answer decodes. */
export type FrameAnswerValue = string | number | boolean | number[] | string[];

/** Reads the current value of the answer control in the frame content. */
export interface AnswerReader {
  /** The value the control would post now, or undefined while a choice has no selection. */
  currentAnswer(): FrameAnswerValue | undefined;
  /** The text a card template's `getTypedAnswer()` expects: the first text input or textarea, or the empty string. */
  typedAnswer(): string;
}

const TEXT_CONTROLS = new Set(['text', 'numeric']);

function checkedInput(group: Element | null, selector: string): HTMLInputElement | null {
  return group?.querySelector<HTMLInputElement>(selector) ?? null;
}

/**
 * readControlValue turns the control the event came from into the postable answer. The markup is the server's answer
 * contract (internal/answer/markup.go): text-like inputs carry `.answer-input` and `data-control`, a group of blanks is
 * `.answer-inputs` with `data-index` order, and the choices are `input[type=radio|checkbox]` named after their group.
 */
export function readControlValue(target: Element): FrameAnswerValue | undefined {
  const control = target.closest('[data-control]')?.getAttribute('data-control') ?? '';
  if (control === '') return undefined;

  switch (control) {
    case 'text':
    case 'numeric':
      return target instanceof HTMLInputElement ? target.value : undefined;
    case 'short_answer':
      return target instanceof HTMLTextAreaElement ? target.value : undefined;
    case 'choice_single':
    case 'true_false': {
      const radio = checkedInput(target.closest('.answer-choices'), 'input[type=radio]:checked');
      if (!radio) return undefined;
      return control === 'choice_single' ? Number(radio.value) : radio.value === 'true';
    }
    case 'choice_multi': {
      const checked = target.closest('.answer-choices')?.querySelectorAll<HTMLInputElement>('input[type=checkbox]:checked') ?? [];
      return Array.from(checked)
        .map((input) => Number(input.value))
        .sort((a, b) => a - b);
    }
    case 'list':
    case 'cloze_blanks': {
      const inputs = Array.from(target.closest('.answer-inputs')?.querySelectorAll<HTMLInputElement>('input.answer-input') ?? []);
      if (inputs.length === 0) return undefined;
      return inputs
        .sort((a, b) => Number(a.dataset.index ?? 0) - Number(b.dataset.index ?? 0))
        .map((input) => input.value);
    }
    default:
      return undefined;
  }
}

/** The answer inputs the frame currently holds, in document order. */
function answerInputs(root: Element): HTMLInputElement[] {
  return Array.from(root.querySelectorAll<HTMLInputElement>('input.answer-input, textarea.answer-input'));
}

/** readCurrent reads the value the controls hold now, without waiting for an input event. */
function readCurrent(root: Element): FrameAnswerValue | undefined {
  const filled = answerInputs(root)[0];
  if (filled) return readControlValue(filled);
  const checked = root.querySelector<HTMLInputElement>('input[type=checkbox]:checked, input[type=radio]:checked');
  if (checked) return readControlValue(checked);
  const group = root.querySelector<HTMLElement>('.answer-choices[data-control], .answer-inputs[data-control]');
  if (group) return readControlValue(group);
  return undefined;
}

/**
 * attachAnswerControls listens for input and change on the whole content root and posts one `answer` message per
 * user action. One delegated pair of listeners survives a card change because the runtime detaches them with the root,
 * and the postable value is read from the live control rather than kept in frame state.
 */
export function attachAnswerControls(root: Element, emit: (value: FrameAnswerValue) => void): AnswerReader {
  const onInput = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const value = readControlValue(target);
    if (value !== undefined) emit(value);
  };
  root.addEventListener('input', onInput);
  root.addEventListener('change', onInput);

  return {
    currentAnswer(): FrameAnswerValue | undefined {
      return readCurrent(root);
    },
    typedAnswer(): string {
      const inputs = answerInputs(root);
      const typed = inputs.find(
        (input) => input instanceof HTMLTextAreaElement || TEXT_CONTROLS.has(input.getAttribute('data-control') ?? ''),
      );
      return typed?.value ?? '';
    },
  };
}

/* ---------------------------------------------------------------------------------------------------------------- */
/* [sound:] and TTS tags                                                                                             */
/* ---------------------------------------------------------------------------------------------------------------- */

const SOUND_PATTERN = /\[sound:([^[\]]+)\]/g;
/** The marker ExtractAVTags writes in place of a tag; the tag list describes it by side and position. */
const PLAY_MARKER_PATTERN = /\[anki:play:([qa]):(\d+)\]/g;
/** A [sound:] whose file the browser plays as video rather than audio. */
const VIDEO_EXTENSION = /\.(?:mp4|webm)(?:[?#].*)?$/i;

/** Copies the editor's segment attributes from a wrapping span.engram-sound onto the playable element. */
function applySegment(wrapper: Element | null, target: HTMLElement): void {
  if (!wrapper) return;
  const pairs: Array<[string, string]> = [
    ['data-start', 'data-audio-start'],
    ['data-end', 'data-audio-end'],
    ['data-rate', 'data-audio-rate'],
  ];
  for (const [from, to] of pairs) {
    const value = wrapper.getAttribute(from);
    if (value !== null && value !== '') target.setAttribute(to, value);
  }
  if (wrapper.hasAttribute('data-loop')) target.setAttribute('data-audio-loop', '1');
}

/** The wrapper an audio tag's segment attributes live on, when the author set any. */
function soundWrapper(node: Text): Element | null {
  return node.parentElement?.closest('.engram-sound') ?? null;
}

/** One play marker's element: an audio source for the shortcode player, a video, or a speech source. */
function avElement(doc: Document, node: Text, tag: AVTag, side: 'q' | 'a', index: number): HTMLElement {
  if (tag.kind === 'sound') {
    const name = tag.name ?? '';
    if (VIDEO_EXTENSION.test(name)) {
      const video = doc.createElement('video');
      video.className = 'engram-video';
      video.setAttribute('controls', '');
      video.setAttribute('playsinline', '');
      video.setAttribute('preload', 'metadata');
      video.setAttribute('data-av-side', side);
      video.setAttribute('data-av-index', String(index));
      video.src = name;
      return video;
    }
    const source = doc.createElement('span');
    source.className = 'engram-audio engram-sound';
    source.setAttribute('data-audio-src', name);
    source.setAttribute('data-av-side', side);
    source.setAttribute('data-av-index', String(index));
    applySegment(soundWrapper(node), source);
    source.textContent = name;
    return source;
  }

  const source = doc.createElement('span');
  source.className = 'engram-tts';
  source.setAttribute('data-tts-text', tag.field_text ?? '');
  // Anki removes the spoken body from the card text and leaves only the play control; the shortcode control's hide
  // flag does the same, while restoring the text if speech is unavailable.
  source.setAttribute('data-tts-hide', '1');
  const lang = (tag.lang ?? '').replace(/_/g, '-');
  if (lang !== '') source.setAttribute('data-tts-lang', lang);
  if (tag.voices && tag.voices.length > 0) source.setAttribute('data-tts-voices', tag.voices.join(','));
  if (Number.isFinite(tag.speed)) source.setAttribute('data-tts-rate', String(tag.speed));
  source.setAttribute('data-av-side', side);
  source.setAttribute('data-av-index', String(index));
  source.textContent = tag.field_text ?? '';
  return source;
}

/**
 * enhanceAVMarkers replaces every [anki:play:<side>:<n>] marker with the control source the tag list describes.
 *
 * The server extracts the tags before the frame sees the face, so a marker's side and index identify its entry in
 * `tags`. A sound tag becomes the same `data-audio-src` element the audio shortcode produces, carrying the segment
 * attributes of its wrapping span.engram-sound; an mp4 or webm becomes an inline video; a TTS tag becomes the same
 * `data-tts-*` element the speech shortcode produces. The elements are returned in document order.
 */
export function enhanceAVMarkers(doc: Document, root: Element, tags: AVTag[]): HTMLElement[] {
  if (tags.length === 0) return [];
  const byMarker = new Map<string, AVTag>();
  const counters = new Map<string, number>();
  for (const tag of tags) {
    // The server lists the question tags first on the answer face; within one side the order it returns is the
    // marker order, so the running index per side is the marker index.
    const index = counters.get(tag.side) ?? 0;
    byMarker.set(`${tag.side}:${index}`, tag);
    counters.set(tag.side, index + 1);
  }

  const walker = doc.createTreeWalker(root, 0x4 /* NodeFilter.SHOW_TEXT */);
  const textNodes: Text[] = [];
  for (let node = walker.nextNode(); node; node = walker.nextNode()) textNodes.push(node as Text);

  for (const node of textNodes) {
    const text = node.data;
    const matches = [...text.matchAll(PLAY_MARKER_PATTERN)];
    if (matches.length === 0) continue;
    const fragment = doc.createDocumentFragment();
    let cursor = 0;
    for (const match of matches) {
      const index = match.index ?? 0;
      if (index > cursor) fragment.appendChild(doc.createTextNode(text.slice(cursor, index)));
      const side = match[1] === 'a' ? 'a' : 'q';
      const ordinal = Number(match[2] ?? -1);
      const tag = byMarker.get(`${side}:${ordinal}`);
      if (tag) {
        fragment.appendChild(avElement(doc, node, tag, side, ordinal));
      } else {
        // A marker with no tag in the list stays text: inventing a control for it would play the wrong sound.
        fragment.appendChild(doc.createTextNode(match[0]));
      }
      cursor = index + match[0].length;
    }
    if (cursor < text.length) fragment.appendChild(doc.createTextNode(text.slice(cursor)));
    node.replaceWith(fragment);
  }

  return Array.from(root.querySelectorAll<HTMLElement>('[data-av-side]'));
}

/**
 * enhanceSoundTags is the fallback for a face that still carries literal [sound:name] tags rather than extracted
 * markers (an older response, or a caller that assembled the HTML itself). The tag grammar is Anki's `[sound:name]`:
 * the prefix is case-sensitive and the name is non-empty and free of `]`.
 *
 * The elements are returned in document order, which is the order `play:q:N` / `play:a:N` addresses.
 */
export function enhanceSoundTags(doc: Document, root: Element, side: 'q' | 'a'): HTMLElement[] {
  const walker = doc.createTreeWalker(root, 0x4 /* NodeFilter.SHOW_TEXT */);
  const textNodes: Text[] = [];
  for (let node = walker.nextNode(); node; node = walker.nextNode()) textNodes.push(node as Text);

  const created: HTMLElement[] = [];
  for (const node of textNodes) {
    const text = node.data;
    const matches = [...text.matchAll(SOUND_PATTERN)];
    if (matches.length === 0) continue;
    const fragment = doc.createDocumentFragment();
    let cursor = 0;
    for (const match of matches) {
      const index = match.index ?? 0;
      if (index > cursor) fragment.appendChild(doc.createTextNode(text.slice(cursor, index)));
      const name = match[1] ?? '';
      const marker = created.length;
      const source = doc.createElement('span');
      source.className = 'engram-audio engram-sound';
      source.setAttribute('data-audio-src', name);
      source.setAttribute('data-av-side', side);
      source.setAttribute('data-av-index', String(marker));
      applySegment(soundWrapper(node), source);
      source.textContent = name;
      fragment.appendChild(source);
      created.push(source);
      cursor = index + match[0].length;
    }
    if (cursor < text.length) fragment.appendChild(doc.createTextNode(text.slice(cursor)));
    node.replaceWith(fragment);
  }
  return created;
}

/** Plays the tag at `side:index`: clicks its shortcode control, or starts a video element. */
export function playAVTag(sources: HTMLElement[], side: string, index: number): void {
  const source = sources.find(
    (element) => element.dataset.avSide === side && element.dataset.avIndex === String(index),
  );
  if (!source) return;
  if (source.tagName.toLowerCase() === 'video') {
    const video = source as HTMLVideoElement;
    try {
      video.currentTime = 0;
    } catch {
      // A video whose metadata is not loaded cannot seek; it still plays from its start.
    }
    void video.play().catch(() => {});
    return;
  }
  source.querySelector<HTMLButtonElement>('button[data-shortcode-control]')?.click();
}

/* ---------------------------------------------------------------------------------------------------------------- */
/* Template globals                                                                                                  */
/* ---------------------------------------------------------------------------------------------------------------- */

export interface ReviewerGlobals {
  /** The current control value; `pycmd('ans')` posts it. */
  answer(): FrameAnswerValue | undefined;
  /** Posts the answer the way a control's own input event does. */
  emitAnswer(value: FrameAnswerValue): void;
  /** The text `getTypedAnswer()` returns. */
  typedAnswer(): string;
  /** Plays the tag at `index` of the requested side (`q` or `a`). */
  play(side: string, index: number): void;
}

interface AnkiWindow extends Window {
  $?: unknown;
  jQuery?: unknown;
  getTypedAnswer?: () => string;
  pycmd?: (command: unknown) => void;
  bridgeCommand?: (command: unknown) => void;
}

const PLAY_COMMAND = /^play:([qa]):(\d+)$/;

/**
 * installReviewerGlobals exposes the globals shared card templates call. `pycmd` accepts exactly three commands and
 * ignores everything else: `ans` posts the current answer, and `play:q:N` / `play:a:N` play tag N of that side.
 * jQuery is exposed as-is, because a template may use either spelling.
 */
export function installReviewerGlobals(win: Window, globals: ReviewerGlobals): void {
  const target = win as AnkiWindow;
  target.$ = $;
  target.jQuery = $;
  target.getTypedAnswer = () => globals.typedAnswer();
  const pycmd = (command: unknown): void => {
    if (typeof command !== 'string') return;
    if (command === 'ans') {
      const value = globals.answer();
      if (value !== undefined) globals.emitAnswer(value);
      return;
    }
    const play = PLAY_COMMAND.exec(command);
    if (play) globals.play(play[1] ?? '', Number(play[2] ?? -1));
  };
  target.pycmd = pycmd;
  target.bridgeCommand = pycmd;
}
