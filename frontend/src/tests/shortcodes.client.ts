import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { enhanceShortcodes, stopShortcodePlayback } from '../lib/shortcodes';
import { setLocale } from '../lib/i18n';

/**
 * Browser-side shortcode controls.
 *
 * The server contract is fixed: `span.engram-tts[data-tts-*]` and `span.engram-audio[data-audio-*]` arrive inside
 * HTML that bluemonday already sanitized, and the element's text is the visible fallback. The module may read only
 * those data attributes. Speech and media are stubbed at the global boundary because the test environment has
 * neither engine; the cases assert what the module asks them to do, not that a real voice exists.
 */

const SHA = 'a'.repeat(64);

function ttsHTML(text = 'こんにちは', attrs = 'data-tts-lang="ja-JP" data-tts-rate="0.9"'): string {
  return `<span class="engram-tts" data-tts-text="${text}" ${attrs}>${text}</span>`;
}

/** A tts element written with `|hide`: the server keeps the body and marks it as one the client may hide. */
function ttsHideHTML(text = 'こんにちは', attrs = 'data-tts-lang="ja-JP"'): string {
  return ttsHTML(text, `${attrs} data-tts-hide="1"`);
}

function audioHTML(attrs = ''): string {
  return `<span class="engram-audio" data-audio-src="/media/${SHA}" ${attrs}>/media/${SHA}</span>`;
}

interface FakeUtterance {
  text: string;
  lang: string;
  rate: number;
  voice: unknown;
  onend: (() => void) | null;
  onerror: ((event: { error: string }) => void) | null;
}

function installSpeech(voices: Array<{ lang: string; name?: string }> = [{ lang: 'ja-JP' }]) {
  const spoken: FakeUtterance[] = [];
  const utteranceConstructor = class implements FakeUtterance {
    text: string;
    lang = '';
    rate = 1;
    voice: unknown = null;
    onend: (() => void) | null = null;
    onerror: ((event: { error: string }) => void) | null = null;
    constructor(text: string) {
      this.text = text;
    }
  };
  const synth = {
    cancel: vi.fn(),
    speak: vi.fn((utterance: FakeUtterance) => {
      spoken.push(utterance);
    }),
    getVoices: vi.fn(() =>
      voices.map((voice, index) => ({
        lang: voice.lang,
        name: voice.name ?? `voice-${index}`,
        default: index === 0,
        localService: true,
        voiceURI: `voice-${index}`,
      }))
    ),
  };
  vi.stubGlobal('speechSynthesis', synth);
  vi.stubGlobal('SpeechSynthesisUtterance', utteranceConstructor);
  return { synth, spoken };
}

function installMedia() {
  const plays: HTMLAudioElement[] = [];
  const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(function (this: HTMLAudioElement) {
    plays.push(this);
    return Promise.resolve();
  });
  const pause = vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => undefined);
  const load = vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
  return { play, pause, load, plays };
}

function container(html: string): HTMLElement {
  const element = document.createElement('div');
  element.innerHTML = html;
  document.body.appendChild(element);
  return element;
}

function click(button: Element): void {
  button.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
}

function control(root: ParentNode): HTMLButtonElement {
  const button = root.querySelector<HTMLButtonElement>('button[data-shortcode-control]');
  expect(button).toBeTruthy();
  return button!;
}

function progress(root: ParentNode): HTMLInputElement {
  const input = root.querySelector<HTMLInputElement>('input[data-shortcode-progress]');
  expect(input).toBeTruthy();
  return input!;
}

/** One of the two five-second buttons; the step is the marker the module dispatches on. */
function seekButton(root: ParentNode, step: number): HTMLButtonElement {
  const button = root.querySelector<HTMLButtonElement>(`button[data-shortcode-seek="${step}"]`);
  expect(button).toBeTruthy();
  return button!;
}

function timeText(root: ParentNode): string {
  const time = root.querySelector<HTMLElement>('[data-shortcode-time]');
  expect(time).toBeTruthy();
  return time!.textContent ?? '';
}

/** Gives the stubbed element a duration and runs the one-time metadata pass the module waits for. */
function loaded(audio: HTMLAudioElement, duration: number): void {
  Object.defineProperty(audio, 'duration', { value: duration, configurable: true });
  audio.dispatchEvent(new Event('loadedmetadata'));
}

function seekInput(input: HTMLInputElement, value: number): void {
  input.value = String(value);
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

function press(input: HTMLInputElement, key: string): void {
  input.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
}

beforeEach(() => {
  setLocale('en');
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

describe('enhanceShortcodes — speech', () => {
  it('adds one control and keeps the spoken text visible', () => {
    installSpeech();
    const root = container(`<p>${ttsHTML()}</p>`);

    enhanceShortcodes([root]);

    const button = control(root);
    expect(button.getAttribute('data-shortcode-control')).toBe('tts');
    expect(button.getAttribute('aria-label')).toBe('Read aloud');
    expect(button.getAttribute('aria-pressed')).toBe('false');
    // The fallback stays in the DOM: this is the no-JavaScript / no-speech-engine reading.
    expect(root.textContent).toContain('こんにちは');
    expect(root.querySelector('.sr-only')).toBeNull();
  });

  it('speaks the data attribute, not the element text, and reflects the playing state', () => {
    const { synth, spoken } = installSpeech();
    const root = container(ttsHTML('こんにちは'));
    // A different text node must not change what is spoken: only the server's data attribute is read.
    root.querySelector('.engram-tts')!.textContent = 'visible-but-not-spoken';
    enhanceShortcodes([root]);
    const button = control(root);

    click(button);

    expect(synth.speak).toHaveBeenCalledTimes(1);
    expect(spoken[0]!.text).toBe('こんにちは');
    expect(spoken[0]!.lang).toBe('ja-JP');
    expect(spoken[0]!.rate).toBeCloseTo(0.9);
    expect(spoken[0]!.voice).toMatchObject({ lang: 'ja-JP' });
    expect(button.getAttribute('aria-pressed')).toBe('true');
    expect(button.getAttribute('aria-label')).toBe('Stop reading');

    click(button);
    expect(synth.cancel).toHaveBeenCalled();
    expect(button.getAttribute('aria-pressed')).toBe('false');
  });

  it('prefers a requested voice by name and falls back to the language voice', () => {
    const named = installSpeech([
      { lang: 'en-US', name: 'English' },
      { lang: 'ja-JP', name: 'Japanese' },
    ]);
    const root = container(ttsHTML('こんにちは', 'data-tts-lang="ja-JP" data-tts-voices="Missing, Japanese"'));
    enhanceShortcodes([root]);

    click(control(root));

    expect(named.spoken[0]!.voice).toMatchObject({ name: 'Japanese' });
  });

  it('falls back to the language default when no requested name matches', () => {
    const named = installSpeech([
      { lang: 'en-US', name: 'English' },
      { lang: 'ja-JP', name: 'Japanese' },
    ]);
    const root = container(ttsHTML('こんにちは', 'data-tts-lang="ja-JP" data-tts-voices="Missing"'));
    enhanceShortcodes([root]);

    click(control(root));

    expect(named.spoken[0]!.voice).toMatchObject({ name: 'Japanese' });
  });

  it('leaves only the visible text when the speech API is missing', () => {
    const root = container(ttsHTML());

    enhanceShortcodes([root]);

    expect(root.querySelector('button')).toBeNull();
    expect(root.textContent).toContain('こんにちは');
    expect(root.querySelector('[data-shortcode-enhanced]')).toBeNull();
  });

  it('leaves only the visible text when no voice speaks the requested language', () => {
    installSpeech([{ lang: 'en-US' }]);
    const root = container(ttsHTML());

    enhanceShortcodes([root]);

    expect(root.querySelector('button')).toBeNull();
    expect(root.textContent).toContain('こんにちは');
  });

  it('removes the control and restores the text when a click finds no voice for the language', () => {
    // Voices are still empty at enhancement time (they load asynchronously), so the control exists; by the click the
    // list has arrived without a Japanese voice, and the button must not stay behind doing nothing.
    const { synth } = installSpeech([]);
    const root = container(ttsHTML());
    enhanceShortcodes([root]);
    const button = control(root);
    synth.getVoices.mockReturnValue([
      { lang: 'en-US', name: 'voice-en', default: true, localService: true, voiceURI: 'voice-en' },
    ]);

    click(button);

    expect(root.querySelector('button')).toBeNull();
    expect(root.textContent).toContain('こんにちは');
    expect(root.textContent).toContain('Reading aloud is not available here');
  });

  it('stops the utterance when the card face is replaced', () => {
    const { synth } = installSpeech();
    const root = container(ttsHTML());
    enhanceShortcodes([root]);
    click(control(root));

    // A card change replaces the container's HTML and then enhances the new face.
    root.innerHTML = '<p>next card</p>';
    enhanceShortcodes([root]);

    expect(synth.cancel).toHaveBeenCalled();
  });
});

describe('enhanceShortcodes — tts body hide', () => {
  it('moves the body into a visually hidden wrapper only with the hide flag', () => {
    installSpeech();
    const root = container(`<p>${ttsHideHTML()}</p><p>${ttsHTML('そのまま')}</p>`);

    enhanceShortcodes([root]);

    const hidden = root.querySelectorAll('[data-shortcode-body]');
    expect(hidden).toHaveLength(1);
    expect(hidden[0]!.classList.contains('sr-only')).toBe(true);
    // The body is hidden, not removed: the text and the data attribute stay in the DOM.
    expect(hidden[0]!.textContent).toBe('こんにちは');
    expect(root.querySelectorAll('button[data-shortcode-control="tts"]')).toHaveLength(2);

    // Without the flag the body stays in normal flow and no wrapper is created.
    const plain = root.querySelectorAll('.engram-tts')[1]!;
    expect(plain.querySelector('[data-shortcode-body]')).toBeNull();
    expect(plain.textContent).toBe('そのまま');
  });

  it('keeps the body visible and adds no control without the speech API', () => {
    const root = container(`<p>${ttsHideHTML()}</p>`);

    enhanceShortcodes([root]);

    expect(root.querySelector('button')).toBeNull();
    expect(root.querySelector('[data-shortcode-body]')).toBeNull();
    expect(root.textContent).toContain('こんにちは');
  });

  it('leaves the body visible when no voice speaks the requested language', () => {
    installSpeech([{ lang: 'en-US' }]);
    const root = container(`<p>${ttsHideHTML()}</p>`);

    enhanceShortcodes([root]);

    expect(root.querySelector('button')).toBeNull();
    expect(root.querySelector('[data-shortcode-body]')).toBeNull();
    expect(root.textContent).toContain('こんにちは');
  });

  it('restores the body when a click finds no voice for the language', () => {
    const { synth } = installSpeech([]);
    const root = container(`<p>${ttsHideHTML()}</p>`);
    enhanceShortcodes([root]);
    const body = root.querySelector('[data-shortcode-body]')!;
    expect(body.classList.contains('sr-only')).toBe(true);
    synth.getVoices.mockReturnValue([
      { lang: 'en-US', name: 'voice-en', default: true, localService: true, voiceURI: 'voice-en' },
    ]);

    click(control(root));

    expect(root.querySelector('button')).toBeNull();
    expect(body.classList.contains('sr-only')).toBe(false);
    expect(root.textContent).toContain('こんにちは');
    expect(root.textContent).toContain('Reading aloud is not available here');
  });

  it('adds no second wrapper or control on a second enhancement', () => {
    installSpeech();
    const root = container(`<p>${ttsHideHTML()}</p>`);

    enhanceShortcodes([root]);
    enhanceShortcodes([root]);

    expect(root.querySelectorAll('[data-shortcode-body]')).toHaveLength(1);
    expect(root.querySelectorAll('button[data-shortcode-control]')).toHaveLength(1);
  });
});

describe('enhanceShortcodes — audio', () => {
  it('hides the media path behind the control while keeping it in the DOM', () => {
    installMedia();
    const root = container(audioHTML('data-audio-start="12.5" data-audio-end="15" data-audio-rate="0.75" data-audio-loop="1"'));

    enhanceShortcodes([root]);

    const button = control(root);
    expect(button.getAttribute('data-shortcode-control')).toBe('audio');
    expect(button.getAttribute('aria-label')).toBe('Play audio');
    const fallback = root.querySelector('[data-shortcode-fallback]');
    expect(fallback).toBeTruthy();
    expect(fallback!.classList.contains('sr-only')).toBe(true);
    expect(root.textContent).toContain(`/media/${SHA}`);
  });

  it('seeks in the same click and honours start, rate and loop', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="12.5" data-audio-end="15" data-audio-rate="0.75" data-audio-loop="1"'));
    enhanceShortcodes([root]);
    const button = control(root);

    click(button);

    expect(media.play).toHaveBeenCalledTimes(1);
    const audio = media.plays[0]!;
    expect(audio.getAttribute('src')).toBe(`/media/${SHA}`);
    expect(audio.playbackRate).toBeCloseTo(0.75);
    expect(audio.currentTime).toBeCloseTo(12.5);
    expect(audio.loop).toBe(false);
    expect(button.getAttribute('aria-pressed')).toBe('true');

    // Reaching the end of the segment while looping seeks back and keeps playing instead of stopping.
    audio.currentTime = 15;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(audio.currentTime).toBeCloseTo(12.5);
    expect(button.getAttribute('aria-pressed')).toBe('true');
  });

  it('stops at the segment end when loop is absent', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="1" data-audio-end="2"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;

    audio.currentTime = 2;
    audio.dispatchEvent(new Event('timeupdate'));

    expect(audio.paused).toBe(true);
    expect(button.getAttribute('aria-pressed')).toBe('false');
  });

  it('never lets two audio controls play at once', () => {
    const media = installMedia();
    const root = container(`<p>${audioHTML()}</p><p>${audioHTML('data-audio-start="0"')}</p>`);
    enhanceShortcodes([root]);
    const buttons = root.querySelectorAll<HTMLButtonElement>('button[data-shortcode-control="audio"]');
    expect(buttons).toHaveLength(2);

    click(buttons[0]!);
    click(buttons[1]!);

    expect(buttons[0]!.getAttribute('aria-pressed')).toBe('false');
    expect(buttons[1]!.getAttribute('aria-pressed')).toBe('true');
    expect(media.pause).toHaveBeenCalled();
  });

  it('removes the control rather than showing a broken player when the media fails to load', () => {
    const media = installMedia();
    const root = container(audioHTML());
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;

    audio.dispatchEvent(new Event('error'));

    expect(root.querySelector('button')).toBeNull();
    const fallback = root.querySelector('[data-shortcode-fallback]');
    expect(fallback!.classList.contains('sr-only')).toBe(false);
    expect(root.textContent).toContain(`/media/${SHA}`);
    expect(root.textContent).toContain('This audio could not be loaded');
  });

  it('removes the control when playback is rejected', async () => {
    const media = installMedia();
    media.play.mockImplementation(() => Promise.reject(new Error('unsupported source')));
    const root = container(audioHTML());
    enhanceShortcodes([root]);
    click(control(root));
    await Promise.resolve();
    await Promise.resolve();

    expect(root.querySelector('button')).toBeNull();
    expect(root.textContent).toContain(`/media/${SHA}`);
  });
});

describe('enhanceShortcodes — audio progress', () => {
  it('starts disabled and shows no position before the metadata arrives', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14"'));
    enhanceShortcodes([root]);

    const track = progress(root);
    expect(track.disabled).toBe(true);
    expect(track.getAttribute('aria-label')).toBe('Audio progress');
    expect(track.getAttribute('aria-valuetext')).toBeNull();
    expect(media.plays).toHaveLength(0);
  });

  it('fills from start to end when both are present', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14"'));
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;
    loaded(audio, 20);
    const track = progress(root);
    expect(track.disabled).toBe(false);
    expect(track.value).toBe('0');

    audio.currentTime = 11;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(track.value).toBe('250');

    audio.currentTime = 13;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(Number(track.value)).toBe(750);
    expect(track.getAttribute('aria-valuetext')).toBe('3 of 4 seconds');
  });

  it('spans start to the media duration when end is absent', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10"'));
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;
    loaded(audio, 20);

    audio.currentTime = 15;
    audio.dispatchEvent(new Event('timeupdate'));

    expect(Number(progress(root).value)).toBe(500);
    expect(progress(root).getAttribute('aria-valuetext')).toBe('5 of 10 seconds');
  });

  it('spans the whole file when neither start nor end is present', () => {
    const media = installMedia();
    const root = container(audioHTML());
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;
    loaded(audio, 20);

    audio.currentTime = 5;
    audio.dispatchEvent(new Event('timeupdate'));

    expect(progress(root).value).toBe('250');
  });

  it('never divides by zero when end equals start', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="10"'));
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;
    loaded(audio, 20);

    // The playback path already treats an end that is not later than the start as absent, so the track spans the
    // rest of the file instead of producing a 0/0 ratio.
    audio.currentTime = 15;
    audio.dispatchEvent(new Event('timeupdate'));

    expect(progress(root).value).toBe('500');
    expect(progress(root).getAttribute('aria-valuetext')).toBe('5 of 10 seconds');
  });

  it('seeks inside the segment on a click or drag, and keeps playing', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14"'));
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;
    loaded(audio, 20);

    seekInput(progress(root), 250);

    expect(audio.currentTime).toBeCloseTo(11);
    expect(progress(root).value).toBe('250');
    expect(control(root).getAttribute('aria-pressed')).toBe('true');
  });

  it('seeks with Left/Right by five percent and Home/End to the edges', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14"'));
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;
    loaded(audio, 20);
    const track = progress(root);

    press(track, 'ArrowRight');
    expect(track.value).toBe('50');
    expect(audio.currentTime).toBeCloseTo(10.2);

    press(track, 'ArrowRight');
    expect(track.value).toBe('100');
    expect(audio.currentTime).toBeCloseTo(10.4);

    press(track, 'ArrowLeft');
    expect(track.value).toBe('50');
    expect(audio.currentTime).toBeCloseTo(10.2);

    press(track, 'End');
    expect(track.value).toBe('1000');
    expect(audio.currentTime).toBeCloseTo(14);
    expect(track.getAttribute('aria-valuetext')).toBe('4 of 4 seconds');

    press(track, 'Home');
    expect(track.value).toBe('0');
    expect(audio.currentTime).toBeCloseTo(10);
  });

  it('resets the track to the segment start when playback ends', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14"'));
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;
    loaded(audio, 20);
    audio.currentTime = 13;
    audio.dispatchEvent(new Event('timeupdate'));

    audio.dispatchEvent(new Event('ended'));

    expect(control(root).getAttribute('aria-pressed')).toBe('false');
    expect(progress(root).value).toBe('0');
    expect(progress(root).getAttribute('aria-valuetext')).toBe('0 of 4 seconds');
  });

  it('restarts at the segment start when the segment loops', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14" data-audio-loop="1"'));
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;
    loaded(audio, 20);

    audio.currentTime = 14;
    audio.dispatchEvent(new Event('timeupdate'));

    expect(audio.currentTime).toBeCloseTo(10);
    expect(progress(root).value).toBe('0');
    expect(control(root).getAttribute('aria-pressed')).toBe('true');
  });

  it('seeks while stopped, and that position survives as the next play target', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="0" data-audio-end="8"'));
    enhanceShortcodes([root]);
    click(control(root));
    const audio = media.plays[0]!;
    loaded(audio, 20);
    audio.dispatchEvent(new Event('ended'));

    seekInput(progress(root), 500);
    expect(audio.currentTime).toBeCloseTo(4);

    click(control(root));
    expect(media.play).toHaveBeenCalledTimes(2);
    expect(audio.currentTime).toBeCloseTo(4);
  });

  it('adds no second track or listener on a second enhancement', () => {
    installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14"'));
    const added = vi.spyOn(root, 'addEventListener');

    enhanceShortcodes([root]);
    const bound = added.mock.calls.length;
    expect(bound).toBeGreaterThan(0);

    enhanceShortcodes([root]);

    expect(root.querySelectorAll('input[data-shortcode-progress]')).toHaveLength(1);
    expect(added.mock.calls.length).toBe(bound);
  });

  it('removes the track with the button when the media fails to load', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14"'));
    enhanceShortcodes([root]);
    click(control(root));
    loaded(media.plays[0]!, 20);

    media.plays[0]!.dispatchEvent(new Event('error'));

    expect(root.querySelector('button')).toBeNull();
    expect(root.querySelector('input[data-shortcode-progress]')).toBeNull();
    const fallback = root.querySelector('[data-shortcode-fallback]');
    expect(fallback!.classList.contains('sr-only')).toBe(false);
    expect(root.textContent).toContain(`/media/${SHA}`);
  });

  it('relabels the track from the catalog when the locale changes', () => {
    installMedia();
    const root = container(audioHTML());
    enhanceShortcodes([root]);
    expect(progress(root).getAttribute('aria-label')).toBe('Audio progress');

    setLocale('zh-CN');
    enhanceShortcodes([root]);

    expect(progress(root).getAttribute('aria-label')).toBe('音频进度');
  });
});

describe('enhanceShortcodes — audio pause and resume', () => {
  it('pauses in place: the element, its position and the fill all stay', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="30"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;
    loaded(audio, 40);
    audio.currentTime = 20;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(progress(root).value).toBe('500');

    click(button);

    expect(audio.currentTime).toBeCloseTo(20);
    expect(progress(root).value).toBe('500');
    expect(timeText(root)).toBe('0:10 / 0:20');
    expect(button.getAttribute('aria-pressed')).toBe('false');
    expect(button.getAttribute('aria-label')).toBe('Resume audio');
    // Nothing was reloaded: a pause is not a teardown.
    expect(media.load).toHaveBeenCalledTimes(1);
    expect(media.plays).toHaveLength(1);
  });

  it('resumes the same element from the paused position without a new source or a load', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="30"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;
    loaded(audio, 40);
    audio.currentTime = 20;
    audio.dispatchEvent(new Event('timeupdate'));

    click(button);
    click(button);

    expect(media.plays).toHaveLength(2);
    expect(media.plays[1]).toBe(audio);
    expect(media.load).toHaveBeenCalledTimes(1);
    expect(audio.getAttribute('src')).toBe(`/media/${SHA}`);
    expect(audio.currentTime).toBeCloseTo(20);
    expect(button.getAttribute('aria-pressed')).toBe('true');
    expect(button.getAttribute('aria-label')).toBe('Pause audio');

    audio.currentTime = 24;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(progress(root).value).toBe('700');
    expect(timeText(root)).toBe('0:14 / 0:20');
  });

  it('resumes from a position that changed while paused', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="30"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;
    loaded(audio, 40);
    audio.currentTime = 20;
    audio.dispatchEvent(new Event('timeupdate'));
    click(button);

    audio.currentTime = 26;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(timeText(root)).toBe('0:16 / 0:20');

    click(button);

    // A resume must not fall back to the segment start the first play used.
    expect(audio.currentTime).toBeCloseTo(26);
    expect(media.play).toHaveBeenCalledTimes(2);
  });

  it('resets a paused control to its segment start when a different audio starts', () => {
    const media = installMedia();
    const root = container(
      `<p>${audioHTML('data-audio-start="10" data-audio-end="20"')}</p><p>${audioHTML('data-audio-start="2"')}</p>`
    );
    enhanceShortcodes([root]);
    const buttons = root.querySelectorAll<HTMLButtonElement>('button[data-shortcode-control="audio"]');
    expect(buttons).toHaveLength(2);
    click(buttons[0]!);
    const first = media.plays[0]!;
    loaded(first, 40);
    first.currentTime = 15;
    first.dispatchEvent(new Event('timeupdate'));

    click(buttons[0]!);
    expect(timeText(root)).toBe('0:05 / 0:10');

    click(buttons[1]!);

    expect(timeText(root)).toBe('0:00 / 0:10');
    expect(progress(root).value).toBe('0');
    expect(buttons[0]!.getAttribute('aria-pressed')).toBe('false');

    click(buttons[0]!);
    expect(media.plays.at(-1)).toBe(first);
    expect(first.currentTime).toBeCloseTo(10);
  });

  it('keeps the control when the pause aborts the play attempt it superseded', async () => {
    const media = installMedia();
    let rejectFirst: (reason: unknown) => void = () => {};
    // The first play never settles inside the test: it is the attempt the pause aborts.
    media.play.mockImplementationOnce(function (this: HTMLAudioElement) {
      media.plays.push(this);
      return new Promise<void>((_resolve, reject) => {
        rejectFirst = reject;
      });
    });
    const root = container(audioHTML('data-audio-start="0" data-audio-end="10"'));
    enhanceShortcodes([root]);
    const button = control(root);

    click(button);
    click(button);
    click(button);
    rejectFirst(new Error('aborted by pause'));
    await Promise.resolve();
    await Promise.resolve();

    // The abort belongs to an attempt a resume has since replaced; removing the player here would lose the control.
    expect(root.querySelector('button[data-shortcode-control="audio"]')).toBe(button);
    expect(button.getAttribute('aria-pressed')).toBe('true');
  });

  it('resets a paused control when the page stops shortcode playback', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="20"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;
    loaded(audio, 40);
    audio.currentTime = 15;
    audio.dispatchEvent(new Event('timeupdate'));
    click(button);

    stopShortcodePlayback();

    expect(button.getAttribute('aria-pressed')).toBe('false');
    expect(button.getAttribute('aria-label')).toBe('Play audio');
    expect(progress(root).value).toBe('0');
    expect(timeText(root)).toBe('0:00 / 0:10');
  });
});

describe('enhanceShortcodes — audio readout', () => {
  it('masks the length before metadata and uses tabular numbers', () => {
    installMedia();
    const root = container(audioHTML('data-audio-start="0" data-audio-end="65"'));
    enhanceShortcodes([root]);
    const time = root.querySelector<HTMLElement>('[data-shortcode-time]')!;

    expect(time.textContent).toBe('0:00 / --:--');
    // Repeating the track's own value would make a screen reader announce the position twice a second.
    expect(time.getAttribute('aria-hidden')).toBe('true');
    expect(time.className).toContain('tabular-nums');
    expect(seekButton(root, -5).disabled).toBe(true);
    expect(seekButton(root, 5).disabled).toBe(true);
  });

  it('writes m:ss and updates on metadata and timeupdate', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;

    expect(timeText(root)).toBe('0:00 / --:--');

    loaded(audio, 20);
    expect(timeText(root)).toBe('0:00 / 0:04');

    audio.currentTime = 11.9;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(timeText(root)).toBe('0:01 / 0:04');

    audio.currentTime = 13.99;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(timeText(root)).toBe('0:03 / 0:04');
  });

  it('writes h:mm:ss when the segment reaches an hour', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="0" data-audio-end="3900"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;
    loaded(audio, 4000);

    expect(timeText(root)).toBe('0:00:00 / 1:05:00');

    audio.currentTime = 3725;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(timeText(root)).toBe('1:02:05 / 1:05:00');
  });

  it('resets the readout at the segment end', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="1" data-audio-end="5"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;
    loaded(audio, 20);
    audio.currentTime = 4;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(timeText(root)).toBe('0:03 / 0:04');

    audio.dispatchEvent(new Event('ended'));

    expect(timeText(root)).toBe('0:00 / 0:04');
    expect(button.getAttribute('aria-pressed')).toBe('false');
  });
});

describe('enhanceShortcodes — five-second seek', () => {
  it('steps by five seconds and clamps at both edges', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="30"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;
    loaded(audio, 40);
    const back = seekButton(root, -5);
    const forward = seekButton(root, 5);

    expect(back.disabled).toBe(true);
    expect(forward.disabled).toBe(false);
    expect(back.getAttribute('aria-label')).toBe('Back 5 seconds');
    expect(forward.getAttribute('aria-label')).toBe('Forward 5 seconds');
    // The same 40px pointer target as the play button.
    expect(back.className).toContain('size-10');

    click(forward);
    expect(audio.currentTime).toBeCloseTo(15);
    expect(back.disabled).toBe(false);

    click(back);
    expect(audio.currentTime).toBeCloseTo(10);
    expect(back.disabled).toBe(true);

    press(progress(root), 'End');
    expect(audio.currentTime).toBeCloseTo(30);
    expect(forward.disabled).toBe(true);

    // A press on a disabled edge button changes nothing.
    click(forward);
    expect(audio.currentTime).toBeCloseTo(30);
    click(back);
    expect(audio.currentTime).toBeCloseTo(25);
    expect(timeText(root)).toBe('0:15 / 0:20');
  });

  it('seeks while paused and stays paused', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="30"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;
    loaded(audio, 40);
    audio.currentTime = 20;
    audio.dispatchEvent(new Event('timeupdate'));
    click(button);
    expect(button.getAttribute('aria-pressed')).toBe('false');

    click(seekButton(root, 5));

    expect(audio.currentTime).toBeCloseTo(25);
    expect(button.getAttribute('aria-pressed')).toBe('false');
    expect(media.plays).toHaveLength(1);
    expect(timeText(root)).toBe('0:15 / 0:20');

    click(button);
    expect(media.plays).toHaveLength(2);
    expect(audio.currentTime).toBeCloseTo(25);
  });

  it('keeps a paused seek at the segment end instead of resetting it', () => {
    const media = installMedia();
    const root = container(audioHTML('data-audio-start="2" data-audio-end="8"'));
    enhanceShortcodes([root]);
    const button = control(root);
    click(button);
    const audio = media.plays[0]!;
    loaded(audio, 30);
    audio.currentTime = 3;
    audio.dispatchEvent(new Event('timeupdate'));
    click(button);
    const back = seekButton(root, -5);
    const forward = seekButton(root, 5);

    // The browser fires timeupdate after the seek; a paused element is being positioned, not finishing playback.
    click(forward);
    audio.dispatchEvent(new Event('timeupdate'));

    expect(audio.currentTime).toBeCloseTo(8);
    expect(progress(root).value).toBe('1000');
    expect(timeText(root)).toBe('0:06 / 0:06');
    expect(button.getAttribute('aria-label')).toBe('Resume audio');
    expect(forward.disabled).toBe(true);
    expect(back.disabled).toBe(false);
  });

  it('relabels both buttons from the catalog on a locale change', () => {
    installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="30"'));
    enhanceShortcodes([root]);
    expect(seekButton(root, -5).getAttribute('aria-label')).toBe('Back 5 seconds');
    expect(seekButton(root, 5).getAttribute('aria-label')).toBe('Forward 5 seconds');

    setLocale('zh-CN');
    enhanceShortcodes([root]);

    expect(seekButton(root, -5).getAttribute('aria-label')).toBe('后退 5 秒');
    expect(seekButton(root, 5).getAttribute('aria-label')).toBe('前进 5 秒');
  });

  it('draws a longer track and keeps the whole player in one group', () => {
    installMedia();
    const root = container(audioHTML('data-audio-start="10" data-audio-end="14"'));
    enhanceShortcodes([root]);
    const track = progress(root);

    // 8rem at the narrow end and 10rem from the large breakpoint; the old track was 4rem (w-16).
    expect(track.className).toContain('w-32');
    expect(track.className).toContain('lg:w-40');
    expect(track.className).not.toContain('w-16');

    const group = root.querySelector<HTMLElement>('[data-shortcode-player]')!;
    expect(group.className).toContain('inline-flex');
    expect(group.className).toContain('flex-wrap');
    // The three buttons, the track and the readout all sit in that one group.
    expect(group.querySelectorAll('button')).toHaveLength(3);
    expect(group.contains(track)).toBe(true);
    expect(group.querySelector('[data-shortcode-time]')).toBeTruthy();
  });
});

describe('enhanceShortcodes — binding', () => {
  it('binds nothing twice when the same container is enhanced again', () => {
    const { synth } = installSpeech();
    const root = container(ttsHTML());
    enhanceShortcodes([root]);
    enhanceShortcodes([root]);

    expect(root.querySelectorAll('button[data-shortcode-control]')).toHaveLength(1);
    click(control(root));
    expect(synth.speak).toHaveBeenCalledTimes(1);
  });

  it('enhances a container whose shortcode HTML was re-rendered', () => {
    installSpeech();
    const root = container(`<p>${ttsHTML()}</p>`);
    enhanceShortcodes([root]);
    expect(root.querySelectorAll('button')).toHaveLength(1);

    root.innerHTML = `<p>${ttsHTML('さようなら', 'data-tts-lang="ja-JP"')}</p>`;
    enhanceShortcodes([root]);

    expect(root.querySelectorAll('button[data-shortcode-control]')).toHaveLength(1);
  });

  it('adds no control inside an interactive element, where it would nest two controls', () => {
    installSpeech();
    const root = container(`<button type="button"><span data-tts-text="hi" data-tts-lang="en-US">hi</span></button>`);

    enhanceShortcodes([root]);

    expect(root.querySelector('[data-shortcode-control]')).toBeNull();
    expect(root.textContent).toContain('hi');
  });

  it('ignores nullish nodes and leaves ruby markup alone', () => {
    installSpeech();
    const root = container('<ruby>漢字<rt>かんじ</rt></ruby>');

    expect(() => enhanceShortcodes([null, undefined])).not.toThrow();
    enhanceShortcodes([null, root, undefined]);

    expect(root.innerHTML).toBe('<ruby>漢字<rt>かんじ</rt></ruby>');
  });
});
