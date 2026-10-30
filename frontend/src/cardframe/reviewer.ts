import $ from 'jquery';

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
/* [sound:] tags                                                                                                     */
/* ---------------------------------------------------------------------------------------------------------------- */

const SOUND_PATTERN = /\[sound:([^[\]]+)\]/g;

/**
 * enhanceSoundTags replaces every literal `[sound:name]` in the face's text nodes with an audio control source. The
 * server leaves the tag in the rendered HTML (it cannot know whether the client can play audio); the frame turns it
 * into the same `data-audio-src` element the audio shortcode produces, so engram's play/pause, seek, ±5 s and time
 * controls attach to every tag. The tag grammar is Anki's `[sound:name]`: the prefix is case-sensitive and the name is
 * non-empty and free of `]`.
 *
 * The elements are returned in document order, which is the order `play:q:N` / `play:a:N` addresses.
 */
export function enhanceSoundTags(doc: Document, root: Element): HTMLElement[] {
  const walker = doc.createTreeWalker(root, 0x4 /* NodeFilter.SHOW_TEXT */);
  const textNodes: Text[] = [];
  for (let node = walker.nextNode(); node; node = walker.nextNode()) textNodes.push(node as Text);

  for (const node of textNodes) {
    const text = node.data;
    const matches = [...text.matchAll(SOUND_PATTERN)];
    if (matches.length === 0) continue;
    const fragment = doc.createDocumentFragment();
    let cursor = 0;
    for (const match of matches) {
      const index = match.index ?? 0;
      if (index > cursor) fragment.appendChild(doc.createTextNode(text.slice(cursor, index)));
      const source = doc.createElement('span');
      source.className = 'engram-audio engram-sound';
      source.setAttribute('data-audio-src', match[1] ?? '');
      source.textContent = match[0];
      fragment.appendChild(source);
      cursor = index + match[0].length;
    }
    if (cursor < text.length) fragment.appendChild(doc.createTextNode(text.slice(cursor)));
    node.replaceWith(fragment);
  }

  return Array.from(root.querySelectorAll<HTMLElement>('span.engram-sound'));
}

/** Clicks the audio control of one tag; the playback itself belongs to the shortcode control that owns the element. */
export function playSound(sounds: HTMLElement[], index: number): void {
  const source = sounds[index];
  const button = source?.querySelector<HTMLButtonElement>('button[data-shortcode-control="audio"]');
  button?.click();
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
