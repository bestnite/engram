import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { enhanceShortcodes } from '../lib/shortcodes';
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

function installSpeech(voices: Array<{ lang: string }> = [{ lang: 'ja-JP' }]) {
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
        name: `voice-${index}`,
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
