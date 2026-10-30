import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRuntime, type FrameRuntime } from '../cardframe/runtime';
import { formatMessage, getLocale, setLocale } from '../lib/i18n';

/**
 * The frame boots with the language the server resolved for the request and wrote into `<html lang>`; the runtime may not read
 * a cookie, so this attribute is the only source. The shortcode controls are built inside the frame, so their labels follow the
 * user's catalog rather than the browser language.
 */

let parent: { postMessage: ReturnType<typeof vi.fn> };
let runtime: FrameRuntime | null = null;

function fromParent(data: unknown): MessageEvent {
  return new MessageEvent('message', { data, source: parent as unknown as MessageEventSource });
}

function showAudio(): MessageEvent {
  return fromParent({
    type: 'show',
    html: '<p><span class="engram-audio" data-audio-src="/fm/token/clip.mp3">clip.mp3</span></p>',
    css: '',
    bodyClass: '',
    side: 'question',
    mediaBase: '/fm/token/',
    autoplay: false,
  });
}

beforeEach(() => {
  parent = { postMessage: vi.fn() };
  document.documentElement.lang = '';
  document.head.innerHTML = '';
  document.body.innerHTML = '<div id="qa"></div>';
  setLocale('en');
});

afterEach(() => {
  runtime?.destroy();
  runtime = null;
  document.documentElement.lang = '';
  vi.restoreAllMocks();
});

describe('card frame locale', () => {
  it('adopts <html lang> so the shortcode labels come from that catalog', async () => {
    document.documentElement.lang = 'zh-CN';
    runtime = createRuntime({ parent: parent as unknown as Window });

    expect(getLocale()).toBe('zh-CN');
    await runtime.handleMessage(showAudio());

    const button = document.querySelector('#qa button[data-shortcode-control]');
    expect(button).not.toBeNull();
    expect(button?.getAttribute('aria-label')).toBe(formatMessage('zh-CN', 'shortcode.audio.play'));
    expect(button?.getAttribute('aria-label')).toBe('播放音频');
    expect(button?.getAttribute('aria-label')).not.toBe(formatMessage('en', 'shortcode.audio.play'));
  });

  it('keeps the browser-language fallback when the document has no usable lang', () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    expect(getLocale()).toBe('en');

    runtime.destroy();
    document.documentElement.lang = 'fr';
    runtime = createRuntime({ parent: parent as unknown as Window });
    // An unsupported code is not guessed at: the module's own detection stays in charge.
    expect(getLocale()).toBe('en');
  });
});
