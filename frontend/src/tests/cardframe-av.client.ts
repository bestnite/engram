import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRuntime, type FrameRuntime } from '../cardframe/runtime';
import type { AVTag } from '../lib/api/types';

/**
 * Audio, video and TTS playback in the card frame.
 *
 * The server extracts every tag before the frame sees the face: the HTML carries [anki:play:<side>:<n>] markers and
 * the face carries the tag list. These cases pin the playback model: the question plays its own tags in order, the
 * answer plays only the tags it adds (the question's markers carried in by {{FrontSide}} stay silent), a segment and
 * loop are read back from span.engram-sound, and an mp4 becomes an inline video.
 */

interface FakeParent {
  postMessage: ReturnType<typeof vi.fn>;
}

let parent: FakeParent;
let runtime: FrameRuntime | null = null;

beforeEach(() => {
  parent = { postMessage: vi.fn() };
  document.head.innerHTML = '';
  document.body.innerHTML = '<div id="qa"></div>';
  delete (window as unknown as { MathJax?: unknown }).MathJax;
});

afterEach(() => {
  runtime?.destroy();
  runtime = null;
  vi.restoreAllMocks();
});

function fromParent(data: unknown): MessageEvent {
  return new MessageEvent('message', { data, source: parent as unknown as MessageEventSource });
}

function showMessage(overrides: Record<string, unknown> = {}): MessageEvent {
  return fromParent({
    type: 'show',
    html: '',
    css: '',
    bodyClass: 'card',
    side: 'question',
    mediaBase: '',
    autoplay: false,
    avTags: [],
    ordinal: 0,
    ...overrides,
  });
}

function sound(side: 'q' | 'a', name: string): AVTag {
  return { kind: 'sound', side, name, speed: 1 };
}

function installMedia() {
  const plays: HTMLAudioElement[] = [];
  const play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(function (this: HTMLAudioElement) {
    plays.push(this);
    return Promise.resolve();
  });
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => undefined);
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => undefined);
  return { play, plays };
}

async function flush(count = 10): Promise<void> {
  for (let i = 0; i < count; i += 1) await Promise.resolve();
}

describe('card frame audio playback', () => {
  it('autoplays the question tags one after another in document order', async () => {
    const media = installMedia();
    runtime = createRuntime({ parent: parent as unknown as Window });

    await runtime.handleMessage(
      showMessage({
        html: '<p>[anki:play:q:0] then [anki:play:q:1]</p>',
        avTags: [sound('q', 'first.mp3'), sound('q', 'second.mp3')],
        autoplay: true,
      }),
    );
    await flush();

    expect(media.play).toHaveBeenCalledTimes(1);
    expect(media.plays[0]?.getAttribute('src')).toBe('first.mp3');

    media.plays[0]!.dispatchEvent(new Event('ended'));
    await flush();

    expect(media.play).toHaveBeenCalledTimes(2);
    expect(media.plays[1]?.getAttribute('src')).toBe('second.mp3');
  });

  it('does not replay the question tags carried into the answer by {{FrontSide}}', async () => {
    const media = installMedia();
    runtime = createRuntime({ parent: parent as unknown as Window });

    await runtime.handleMessage(
      showMessage({
        side: 'answer',
        html: '<p>[anki:play:q:0] answer [anki:play:a:0]</p>',
        avTags: [sound('q', 'question.mp3'), sound('a', 'answer.mp3')],
        autoplay: true,
      }),
    );
    await flush();

    // The question marker still gets a control, but only the answer's own tag starts.
    expect(media.play).toHaveBeenCalledTimes(1);
    expect(media.plays[0]?.getAttribute('src')).toBe('answer.mp3');
    expect(document.querySelectorAll('[data-av-side]')).toHaveLength(2);
  });

  it('honours the segment, rate and loop of span.engram-sound', async () => {
    const media = installMedia();
    runtime = createRuntime({ parent: parent as unknown as Window });

    await runtime.handleMessage(
      showMessage({
        html:
          '<p><span class="engram-sound" data-start="1" data-end="2" data-rate="0.5" data-loop="1">' +
          '[anki:play:q:0]</span></p>',
        avTags: [sound('q', 'clip.mp3')],
      }),
    );

    document.querySelector<HTMLButtonElement>('button[data-shortcode-control="audio"]')!.click();
    await flush();

    const audio = media.plays[0]!;
    expect(audio.currentTime).toBeCloseTo(1);
    expect(audio.playbackRate).toBeCloseTo(0.5);
    expect(audio.loop).toBe(false);

    // Reaching the segment end loops back to its start instead of stopping.
    audio.currentTime = 2;
    audio.dispatchEvent(new Event('timeupdate'));
    expect(audio.currentTime).toBeCloseTo(1);
    expect(document.querySelector('button[data-shortcode-control="audio"]')?.getAttribute('aria-pressed')).toBe('true');
  });

  it('plays an mp4 or webm tag in an inline video element', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });

    await runtime.handleMessage(
      showMessage({
        html: '<p>[anki:play:q:0]</p>',
        avTags: [sound('q', 'clip.webm')],
      }),
    );

    const video = document.querySelector<HTMLVideoElement>('video.engram-video');
    expect(video).not.toBeNull();
    expect(video!.controls).toBe(true);
    expect(video!.getAttribute('src')).toBe('clip.webm');
  });

  it('maps a TTS tag to the speech source attributes', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });

    await runtime.handleMessage(
      showMessage({
        html: '<p>[anki:play:q:0]</p>',
        avTags: [
          {
            kind: 'tts',
            side: 'q',
            lang: 'en_US',
            voices: ['Alpha', 'Beta'],
            speed: 0.75,
            field_text: 'hello',
          },
        ],
      }),
    );

    const speech = document.querySelector<HTMLElement>('span.engram-tts');
    expect(speech).not.toBeNull();
    expect(speech!.getAttribute('data-tts-lang')).toBe('en-US');
    expect(speech!.getAttribute('data-tts-voices')).toBe('Alpha,Beta');
    expect(speech!.getAttribute('data-tts-rate')).toBe('0.75');
    expect(speech!.getAttribute('data-tts-text')).toBe('hello');
  });

  it('replays on r and F5 without posting the key to the parent', async () => {
    const media = installMedia();
    runtime = createRuntime({ parent: parent as unknown as Window });

    await runtime.handleMessage(
      showMessage({
        html: '<p>[anki:play:q:0]</p>',
        avTags: [sound('q', 'clip.mp3')],
      }),
    );

    const key = new KeyboardEvent('keydown', { key: 'r', cancelable: true });
    window.dispatchEvent(key);
    await flush();
    expect(key.defaultPrevented).toBe(true);
    expect(media.play).toHaveBeenCalledTimes(1);

    const reload = new KeyboardEvent('keydown', { key: 'F5', cancelable: true });
    window.dispatchEvent(reload);
    await flush();
    expect(reload.defaultPrevented).toBe(true);
    expect(media.play).toHaveBeenCalledTimes(2);
    expect(parent.postMessage.mock.calls.some(([message]) => (message as { type?: string }).type === 'key')).toBe(false);
  });
});
