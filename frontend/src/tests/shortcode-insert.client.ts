import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ShortcodeInsert from '../lib/components/ShortcodeInsert.svelte';
import { apiClient, ApiClientError } from '../lib/api';
import { setLocale, formatMessage } from '../lib/i18n';

/**
 * The insertion panel writes one source string and nothing else: the preview is text, the insert
 * button is unavailable while the source would be printed verbatim by the server, and the audio
 * section lists only audio files so a pick can only produce an audio shortcode.
 */

const AUDIO_SHA = 'b'.repeat(64);
const IMAGE_SHA = 'c'.repeat(64);
const AUDIO_URL = `/media/${AUDIO_SHA}`;
const IMAGE_URL = `/media/${IMAGE_SHA}`;

let instance: ReturnType<typeof mount> | null = null;
let target: HTMLElement;
let inserted: string[];

function setup(
  props: { targetLabel?: string; targetWritable?: boolean; deckId?: string; showHeading?: boolean } = {}
): void {
  target = document.createElement('div');
  document.body.appendChild(target);
  inserted = [];
  instance = mount(ShortcodeInsert, {
    target,
    props: {
      targetLabel: props.targetLabel ?? '正面',
      targetWritable: props.targetWritable ?? true,
      deckId: props.deckId ?? '',
      showHeading: props.showHeading ?? true,
      oninsert: (source: string) => inserted.push(source),
    },
  });
  flushSync();
}

async function flush(): Promise<void> {
  for (let i = 0; i < 24; i++) await Promise.resolve();
  flushSync();
}

function el<T extends Element = HTMLElement>(testid: string): T {
  const node = target.querySelector(`[data-testid="${testid}"]`);
  if (!node) throw new Error(`missing [data-testid="${testid}"]`);
  return node as T;
}

function setInput(testid: string, value: string): void {
  const input = el<HTMLInputElement>(testid);
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function click(node: Element): void {
  node.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true }));
  node.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, cancelable: true }));
  node.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
  flushSync();
}

function tab(name: string): void {
  click(el(`shortcode-tab-${name}`));
}

function preview(): string {
  return el('shortcode-insert-preview').textContent?.trim() ?? '';
}

function errorText(): string {
  const node = target.querySelector('[data-testid="shortcode-insert-error"]');
  return node?.textContent?.trim() ?? '';
}

function insertButton(): HTMLButtonElement {
  return el<HTMLButtonElement>('shortcode-insert-submit');
}

function dropFile(testid: string, file: File): void {
  const input = el<HTMLInputElement>(testid);
  Object.defineProperty(input, 'files', { configurable: true, value: [file] });
  input.dispatchEvent(new Event('change', { bubbles: true }));
  flushSync();
}

beforeEach(() => {
  setLocale('zh-CN');
  vi.restoreAllMocks();
});

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  document.body.innerHTML = '';
});

describe('ShortcodeInsert — tts', () => {
  it('previews and returns the minimal source, and starts unavailable while the text is empty', () => {
    setup();

    expect(preview()).toBe('—');
    expect(insertButton().disabled).toBe(true);
    expect(errorText()).toBe(formatMessage('zh-CN', 'shortcode.insert.error.body_required'));

    setInput('shortcode-tts-body', '你好');

    expect(preview()).toBe('{{tts:你好}}');
    expect(errorText()).toBe('');
    expect(insertButton().disabled).toBe(false);

    click(insertButton());
    expect(inserted).toEqual(['{{tts:你好}}']);
  });

  it('offers the instance locale as the language placeholder and writes every parameter', () => {
    setup();
    expect(el<HTMLInputElement>('shortcode-tts-lang').placeholder).toBe('zh-CN');

    setInput('shortcode-tts-body', 'こんにちは');
    setInput('shortcode-tts-lang', 'ja-JP');
    setInput('shortcode-tts-rate', '0.9');

    expect(preview()).toBe('{{tts:こんにちは|lang=ja-JP|rate=0.9}}');
    click(insertButton());
    expect(inserted).toEqual(['{{tts:こんにちは|lang=ja-JP|rate=0.9}}']);
  });

  it('writes the hide flag only when its checkbox is checked', () => {
    setup();
    const hide = el('shortcode-tts-hide');

    click(hide);
    // The flag does not make an empty body valid: the button stays unavailable.
    expect(preview()).toBe('—');
    expect(insertButton().disabled).toBe(true);

    setInput('shortcode-tts-body', '你好');
    expect(preview()).toBe('{{tts:你好|hide}}');
    expect(insertButton().disabled).toBe(false);
    click(insertButton());
    expect(inserted).toEqual(['{{tts:你好|hide}}']);

    click(hide);
    expect(preview()).toBe('{{tts:你好}}');
  });

  it('keeps the flag last and shows the checkbox on the tts tab only', () => {
    setup();
    setInput('shortcode-tts-body', 'こんにちは');
    setInput('shortcode-tts-lang', 'ja-JP');
    setInput('shortcode-tts-rate', '0.9');
    click(el('shortcode-tts-hide'));

    expect(preview()).toBe('{{tts:こんにちは|lang=ja-JP|rate=0.9|hide}}');

    tab('audio');
    expect(target.querySelector('[data-testid="shortcode-tts-hide"]')).toBeNull();
    tab('ruby');
    expect(target.querySelector('[data-testid="shortcode-tts-hide"]')).toBeNull();
    tab('tts');
    // The choice survives a round trip through the other tabs.
    expect(preview()).toBe('{{tts:こんにちは|lang=ja-JP|rate=0.9|hide}}');
  });

  it('reports the shape of a rejected rate instead of inserting', () => {
    setup();
    setInput('shortcode-tts-body', 'Hi');
    setInput('shortcode-tts-rate', '1.');

    expect(errorText()).toBe(formatMessage('zh-CN', 'shortcode.insert.error.rate_shape'));
    expect(insertButton().disabled).toBe(true);
    // The offending input is marked, and the message is linked to it.
    expect(el<HTMLInputElement>('shortcode-tts-rate').getAttribute('aria-invalid')).toBe('true');
    expect(el('shortcode-tts-rate').getAttribute('aria-describedby')).toBe(el('shortcode-insert-error').id);
  });

  it('keeps the source as text — a body with markup is never rendered', () => {
    setup();
    setInput('shortcode-tts-body', '<b>x</b>');

    const block = el('shortcode-insert-preview');
    expect(block.textContent).toBe('{{tts:<b>x</b>}}');
    expect(block.querySelector('b')).toBeNull();
  });
});

describe('ShortcodeInsert — ruby', () => {
  it('writes both parts and rejects a separator inside them', () => {
    setup();
    tab('ruby');

    setInput('shortcode-ruby-base', '漢字');
    setInput('shortcode-ruby-reading', 'かんじ');
    expect(preview()).toBe('{{ruby:漢字|かんじ}}');

    click(insertButton());
    expect(inserted).toEqual(['{{ruby:漢字|かんじ}}']);

    setInput('shortcode-ruby-reading', 'かん|じ');
    expect(errorText()).toBe(formatMessage('zh-CN', 'shortcode.insert.error.ruby_part'));
    expect(insertButton().disabled).toBe(true);
  });
});

describe('ShortcodeInsert — audio', () => {
  beforeEach(() => {
    vi.spyOn(apiClient, 'listMedia').mockResolvedValue({
      items: [
        { sha256: IMAGE_SHA, mime: 'image/png', bytes: 4, url: IMAGE_URL, created_at: '' },
        { sha256: AUDIO_SHA, mime: 'audio/mpeg', bytes: 9, url: AUDIO_URL, created_at: '' },
      ],
      next_cursor: '',
    });
  });

  it('lists only audio files and fills the path from the picked one', async () => {
    setup();
    tab('audio');

    // The composer keeps the on-demand picker: its library is opened from the path field, not shown under it.
    expect(target.querySelector('[data-testid="shortcode-audio-picker"]')).toBeNull();
    click(el('shortcode-audio-picker-toggle'));
    for (let i = 0; i < 3; i++) await flush();
    expect(target.querySelector('[data-testid="shortcode-audio-picker"]')).toBeTruthy();

    expect(target.querySelector(`[data-testid="shortcode-audio-item-${AUDIO_SHA}"]`)).toBeTruthy();
    // The picture is not offered: this section can only write an audio shortcode.
    expect(target.querySelector(`[data-testid="shortcode-audio-item-${IMAGE_SHA}"]`)).toBeNull();
    // An audio tile is not an image request (a sound file in an <img> is a broken picture).
    expect(target.querySelector(`[data-testid="shortcode-audio-item-${AUDIO_SHA}"] img`)).toBeNull();

    click(el(`shortcode-audio-item-${AUDIO_SHA}`));
    for (let i = 0; i < 3; i++) await flush();

    expect(el<HTMLInputElement>('shortcode-audio-source').value).toBe(AUDIO_URL);
    expect(preview()).toBe(`{{audio:${AUDIO_URL}}}`);
  });

  it('writes the full source and refuses a reversed range', async () => {
    setup();
    tab('audio');

    setInput('shortcode-audio-source', AUDIO_URL);
    setInput('shortcode-audio-start', '12.5');
    setInput('shortcode-audio-end', '15');
    setInput('shortcode-audio-rate', '0.75');
    click(el('shortcode-audio-loop'));

    expect(preview()).toBe(`{{audio:${AUDIO_URL}|start=12.5|end=15|rate=0.75|loop}}`);
    click(insertButton());
    expect(inserted).toEqual([`{{audio:${AUDIO_URL}|start=12.5|end=15|rate=0.75|loop}}`]);

    setInput('shortcode-audio-start', '20');
    expect(errorText()).toBe(formatMessage('zh-CN', 'shortcode.insert.error.range_order'));
    expect(insertButton().disabled).toBe(true);
    expect(el<HTMLInputElement>('shortcode-audio-end').getAttribute('aria-invalid')).toBe('true');
  });

  it('refuses a path that is not this service media path', () => {
    setup();
    tab('audio');
    setInput('shortcode-audio-source', 'https://example.com/sound.mp3');

    expect(errorText()).toBe(formatMessage('zh-CN', 'shortcode.insert.error.media_shape'));
    expect(insertButton().disabled).toBe(true);
  });
});

describe('ShortcodeInsert — target', () => {
  it('names the target field', () => {
    setup({ targetLabel: '背面' });

    expect(target.textContent).toContain(
      formatMessage('zh-CN', 'shortcode.insert.target', { field: '背面' })
    );
  });

  it('stays unavailable when the chosen field cannot hold a shortcode', () => {
    setup({ targetWritable: false });
    setInput('shortcode-tts-body', '你好');

    expect(errorText()).toBe(formatMessage('zh-CN', 'shortcode.insert.error.not_text_field'));
    expect(insertButton().disabled).toBe(true);
  });
});

describe('ShortcodeInsert — audio upload', () => {
  it('offers no upload action when the host has no deck to upload into', () => {
    setup();
    tab('audio');
    expect(target.querySelector('[data-testid="shortcode-audio-upload"]')).toBeNull();
  });

  it('uploads a sound file into the path field, so the composer still works without a page-level upload', async () => {
    vi.spyOn(apiClient, 'uploadDeckMedia').mockResolvedValue({
      sha256: AUDIO_SHA,
      mime: 'audio/mpeg',
      bytes: 9,
      url: AUDIO_URL,
    });
    setup({ deckId: '5' });
    tab('audio');

    dropFile('shortcode-audio-upload', new File(['sound'], 'sound.mp3', { type: 'audio/mpeg' }));
    await flush();

    expect(apiClient.uploadDeckMedia).toHaveBeenCalledWith('5', expect.any(File));
    expect(el<HTMLInputElement>('shortcode-audio-source').value).toBe(AUDIO_URL);
    expect(preview()).toBe(`{{audio:${AUDIO_URL}}}`);
    // An uploaded sound never becomes the image form.
    expect(preview()).not.toContain('![](');
  });

  it('refuses a picture before the request and leaves the path empty', async () => {
    vi.spyOn(apiClient, 'uploadDeckMedia');
    setup({ deckId: '5' });
    tab('audio');

    dropFile('shortcode-audio-upload', new File(['picture'], 'picture.png', { type: 'image/png' }));
    await flush();

    expect(apiClient.uploadDeckMedia).not.toHaveBeenCalled();
    expect(el<HTMLInputElement>('shortcode-audio-source').value).toBe('');
    expect(preview()).toBe('—');
  });

  it('keeps the path field empty and the panel usable when the upload fails', async () => {
    vi.spyOn(apiClient, 'uploadDeckMedia').mockRejectedValue(
      new ApiClientError('HTTP 413', { status: 413, code: 'media_too_large' })
    );
    setup({ deckId: '5' });
    tab('audio');

    dropFile('shortcode-audio-upload', new File(['sound'], 'sound.mp3', { type: 'audio/mpeg' }));
    await flush();

    expect(el<HTMLInputElement>('shortcode-audio-source').value).toBe('');
    expect(el<HTMLInputElement>('shortcode-audio-upload').disabled).toBe(false);
  });
});

describe('ShortcodeInsert — heading', () => {
  it('prints no heading when a dialog already titles the panel', () => {
    setup({ showHeading: false });

    expect(target.querySelector('h2')).toBeNull();
  });
});
