import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import ShortcodeInsert from '../lib/components/ShortcodeInsert.svelte';
import { apiClient } from '../lib/api';
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

function setup(props: { targetLabel?: string; targetWritable?: boolean; showHeading?: boolean } = {}): void {
  target = document.createElement('div');
  document.body.appendChild(target);
  inserted = [];
  instance = mount(ShortcodeInsert, {
    target,
    props: {
      targetLabel: props.targetLabel ?? '正面',
      targetWritable: props.targetWritable ?? true,
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

    click(el('shortcode-audio-picker-toggle'));
    for (let i = 0; i < 3; i++) await flush();

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

describe('ShortcodeInsert — target and help', () => {
  it('names the target field and links to the syntax guide', () => {
    setup({ targetLabel: '背面' });

    expect(target.textContent).toContain(
      formatMessage('zh-CN', 'shortcode.insert.target', { field: '背面' })
    );
    const link = el<HTMLAnchorElement>('shortcode-insert-help');
    expect(link.getAttribute('href')).toBe('/help/shortcodes');
    expect(link.textContent?.trim()).toBe(formatMessage('zh-CN', 'shortcode.insert.help'));
  });

  it('stays unavailable when the chosen field cannot hold a shortcode', () => {
    setup({ targetWritable: false });
    setInput('shortcode-tts-body', '你好');

    expect(errorText()).toBe(formatMessage('zh-CN', 'shortcode.insert.error.not_text_field'));
    expect(insertButton().disabled).toBe(true);
  });
});

describe('ShortcodeInsert — heading', () => {
  it('prints no heading when a dialog already titles the panel, and keeps the syntax link', () => {
    setup({ showHeading: false });

    expect(target.querySelector('h2')).toBeNull();
    expect(el<HTMLAnchorElement>('shortcode-insert-help').getAttribute('href')).toBe('/help/shortcodes');
  });
});
