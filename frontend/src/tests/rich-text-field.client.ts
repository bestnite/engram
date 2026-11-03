import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import RichTextField from '../lib/components/RichTextField.svelte';
import Harness from './rich-text-field-harness.svelte';
import { apiClient } from '../lib/api';
import { toast } from '../lib/components/ui/toast';

// Selection failures are reported through a toast; the stand-in records the calls the cases assert.
vi.mock('../lib/components/ui/toast', () => ({ toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));

/**
 * The parent side of the rich-text field: the toolbar sends commands, the frame's `change` updates
 * the form value, and the field options reach the frame. The iframe cannot load its document in the
 * test environment, so each case installs a fake frame window and answers `ready` the way the frame
 * runtime does.
 */

interface FakeFrame {
  postMessage: ReturnType<typeof vi.fn>;
}

let target: HTMLElement;
let instance: ReturnType<typeof mount> | null = null;
let value = '';
let uploads: Array<File> = [];

const upload = vi.fn(async (file: File) => {
  uploads.push(file);
  return { url: '/media/' + 'a'.repeat(64), name: 'a'.repeat(64), mime: 'image/png' };
});

function flush(times = 12): void {
  for (let i = 0; i < times; i += 1) void Promise.resolve();
  flushSync();
}

async function flushAsync(times = 12): Promise<void> {
  for (let i = 0; i < times; i += 1) await Promise.resolve();
  flushSync();
}

function frame(): FakeFrame | null {
  const iframe = target.querySelector('[data-testid="field"]') as HTMLIFrameElement | null;
  if (!iframe) return null;
  const fake = { postMessage: vi.fn() };
  Object.defineProperty(iframe, 'contentWindow', { configurable: true, value: fake });
  return fake;
}

function message(source: FakeFrame, data: Record<string, unknown>): void {
  window.dispatchEvent(new MessageEvent('message', { data, source: source as unknown as MessageEventSource }));
  flushSync();
}

function posted(fake: FakeFrame): Array<Record<string, unknown>> {
  return fake.postMessage.mock.calls.map((call) => call[0] as Record<string, unknown>);
}

function lastPosted(fake: FakeFrame): Record<string, unknown> | undefined {
  return posted(fake).at(-1);
}

function lastLoad(fake: FakeFrame): Record<string, unknown> | undefined {
  return posted(fake).filter((entry) => entry.type === 'load').at(-1);
}

function buttons(): Record<string, HTMLButtonElement> {
  const result: Record<string, HTMLButtonElement> = {};
  for (const element of target.querySelectorAll('[data-testid^="field-format-"]')) {
    const id = element.getAttribute('data-testid')!.replace('field-format-', '');
    result[id] = element as HTMLButtonElement;
  }
  return result;
}

function mountField(props: Record<string, unknown> = {}): FakeFrame {
  value = typeof props.value === 'string' ? props.value : '<p>start</p>';
  target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(RichTextField, {
    target,
    props: {
      value,
      label: 'Front',
      testId: 'field',
      upload,
      onvalue: (next: string) => {
        value = next;
      },
      ...props,
    },
  });
  flush();
  const fake = frame();
  if (fake) message(fake, { type: 'ready' });
  return fake ?? { postMessage: vi.fn() };
}

beforeEach(() => {
  uploads = [];
  upload.mockClear();
  document.body.innerHTML = '';
  document.documentElement.classList.remove('dark');
});

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  document.body.innerHTML = '';
});

describe('RichTextField message protocol', () => {
  it('sends the field HTML and its options when the frame is ready', () => {
    const fake = mountField({ value: '<p>hello</p>', options: { rtl: true, font: 'serif', size: 18, description: 'Write here' } });
    const load = lastLoad(fake);
    expect(load).toMatchObject({
      type: 'load',
      html: '<p>hello</p>',
      plainText: false,
      rtl: true,
      font: 'serif',
      size: 18,
      description: 'Write here',
    });
  });

  it('updates the bound value from the frame change and does not echo it back', () => {
    const fake = mountField();
    const loadCount = posted(fake).filter((entry) => entry.type === 'load').length;
    message(fake, { type: 'change', html: '<p>typed</p>' });
    expect(value).toBe('<p>typed</p>');
    expect(posted(fake).filter((entry) => entry.type === 'load')).toHaveLength(loadCount);
  });

  it('accepts only messages from its own frame', () => {
    mountField();
    const foreign = { postMessage: vi.fn() };
    message(foreign, { type: 'change', html: '<p>foreign</p>' });
    expect(value).toBe('<p>start</p>');
  });

  it('sends the theme when the document theme changes', async () => {
    const fake = mountField();
    document.documentElement.classList.add('dark');
    await flushAsync();
    expect(posted(fake).some((entry) => entry.type === 'theme' && entry.dark === true)).toBe(true);
  });

  it('sends the media base with the field and reloads when it arrives later, even after typing', () => {
    target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(Harness, { target });
    flush();
    const fake = frame()!;
    message(fake, { type: 'ready' });
    expect(lastLoad(fake)).toMatchObject({ html: '<p>start</p>', mediaBase: '' });
    message(fake, { type: 'change', html: '<p>typed</p>' });
    (target.querySelector('[data-testid="harness-base"]') as HTMLButtonElement).click();
    flush();
    expect(lastLoad(fake)).toMatchObject({ html: '<p>typed</p>', mediaBase: '/fm/token/' });
  });

  it('keeps the frame hidden until its own frame confirms the theme', () => {
    const fake = mountField();
    const iframe = target.querySelector('[data-testid="field"]') as HTMLIFrameElement;
    expect(iframe.classList.contains('invisible')).toBe(true);
    message({ postMessage: vi.fn() }, { type: 'themed' });
    expect(iframe.classList.contains('invisible')).toBe(true);
    message(fake, { type: 'themed' });
    expect(iframe.classList.contains('invisible')).toBe(false);
  });

  it('hides a frame created again by expanding the field until it confirms the theme', () => {
    mountField({ options: { collapsed: true } });
    const toggle = target.querySelector('[data-testid="field-collapse-toggle"]') as HTMLButtonElement;
    const expand = (): FakeFrame => {
      toggle.click();
      flushSync();
      const fake = frame()!;
      message(fake, { type: 'ready' });
      return fake;
    };
    message(expand(), { type: 'themed' });
    toggle.click();
    flushSync();
    const next = expand();
    const iframe = target.querySelector('[data-testid="field"]') as HTMLIFrameElement;
    expect(iframe.classList.contains('invisible')).toBe(true);
    message(next, { type: 'themed' });
    expect(iframe.classList.contains('invisible')).toBe(false);
  });
});

describe('RichTextField toolbar commands', () => {
  it.each([
    ['bold', { type: 'command', name: 'bold' }],
    ['italic', { type: 'command', name: 'italic' }],
    ['underline', { type: 'command', name: 'underline' }],
    ['superscript', { type: 'command', name: 'superscript' }],
    ['subscript', { type: 'command', name: 'subscript' }],
    ['clear', { type: 'command', name: 'clearFormatting' }],
    ['bullet', { type: 'command', name: 'unorderedList' }],
    ['ordered', { type: 'command', name: 'orderedList' }],
    ['align_left', { type: 'command', name: 'align', value: 'left' }],
    ['align_center', { type: 'command', name: 'align', value: 'center' }],
    ['align_right', { type: 'command', name: 'align', value: 'right' }],
    ['align_justify', { type: 'command', name: 'align', value: 'justify' }],
    ['indent', { type: 'command', name: 'indent', delta: 1 }],
    ['outdent', { type: 'command', name: 'indent', delta: -1 }],
    ['mathjax_inline', { type: 'command', name: 'mathjaxInline' }],
    ['mathjax_block', { type: 'command', name: 'mathjaxBlock' }],
    ['latex', { type: 'command', name: 'latex' }],
    ['cloze', { type: 'command', name: 'cloze' }],
    ['cloze_same', { type: 'command', name: 'clozeSame' }],
  ])('%s sends %o', (id, expected) => {
    const fake = mountField();
    buttons()[id]!.click();
    expect(lastPosted(fake)).toMatchObject(expected);
  });

  it('sends the picked text colour and highlight', () => {
    const fake = mountField();
    const textColor = target.querySelector('[data-testid="field-text-color"]') as HTMLInputElement;
    textColor.value = '#ff0000';
    textColor.dispatchEvent(new Event('input', { bubbles: true }));
    expect(lastPosted(fake)).toMatchObject({ type: 'command', name: 'textColor', color: '#ff0000' });

    const highlight = target.querySelector('[data-testid="field-highlight"]') as HTMLInputElement;
    highlight.value = '#fff59d';
    highlight.dispatchEvent(new Event('input', { bubbles: true }));
    expect(lastPosted(fake)).toMatchObject({ type: 'command', name: 'highlight', color: '#fff59d' });
  });

  it('toggles the HTML source view without reloading the field', () => {
    const fake = mountField();
    const toggle = target.querySelector('[data-testid="field-source-toggle"]') as HTMLButtonElement;
    toggle.click();
    flushSync();
    expect(lastPosted(fake)).toEqual({ type: 'source', open: true });
    expect(toggle.getAttribute('aria-pressed')).toBe('true');
    toggle.click();
    flushSync();
    expect(lastPosted(fake)).toEqual({ type: 'source', open: false });
    expect(toggle.getAttribute('aria-pressed')).toBe('false');
  });

  it('disables the formatting controls while the HTML source is shown', () => {
    const fake = mountField();
    const toggle = target.querySelector('[data-testid="field-source-toggle"]') as HTMLButtonElement;
    toggle.click();
    flushSync();

    const bold = buttons()['bold']!;
    expect(bold.disabled).toBe(true);
    expect(target.querySelector('[data-testid="field-text-color"]')!.hasAttribute('disabled')).toBe(true);
    expect((target.querySelector('[data-testid="field-insert-image"]') as HTMLButtonElement).disabled).toBe(true);
    expect(target.querySelector('[data-testid="field-source-hint"]')).not.toBeNull();

    const before = posted(fake).length;
    bold.click();
    expect(posted(fake)).toHaveLength(before);

    toggle.click();
    flushSync();
    expect(bold.disabled).toBe(false);
    expect(target.querySelector('[data-testid="field-source-hint"]')).toBeNull();
  });

  it('lays the toolbar out as one wrapping row without separator elements', () => {
    mountField();
    const toolbar = target.querySelector('[data-testid="field-toolbar"]') as HTMLElement;
    const tags = Array.from(toolbar.children).map((child) => child.tagName);
    expect(tags.length).toBeGreaterThan(20);
    expect(tags.every((tag) => tag === 'BUTTON' || tag === 'LABEL')).toBe(true);
  });

  it('toggles line wrapping and tells the frame', () => {
    const fake = mountField();
    const toggle = target.querySelector('[data-testid="field-wrap-toggle"]') as HTMLButtonElement;
    expect(toggle.getAttribute('aria-pressed')).toBe('true');

    toggle.click();
    flushSync();
    expect(toggle.getAttribute('aria-pressed')).toBe('false');
    expect(lastPosted(fake)).toEqual({ type: 'wrap', wrapped: false });

    toggle.click();
    flushSync();
    expect(toggle.getAttribute('aria-pressed')).toBe('true');
    expect(lastPosted(fake)).toEqual({ type: 'wrap', wrapped: true });
  });

  it('keeps the wrap toggle available in a plain-text field', () => {
    const fake = mountField({ options: { plainText: true } });
    const toggle = target.querySelector('[data-testid="field-wrap-toggle"]') as HTMLButtonElement;
    expect(toggle).not.toBeNull();
    toggle.click();
    flushSync();
    expect(lastPosted(fake)).toEqual({ type: 'wrap', wrapped: false });
  });

  it('uploads a chosen file and asks the frame to insert it', async () => {
    const fake = mountField();
    const input = target.querySelector('[data-testid="field-file-input"]') as HTMLInputElement;
    const file = new File(['picture'], 'picture.png', { type: 'image/png' });
    Object.defineProperty(input, 'files', { configurable: true, value: [file] });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    await flushAsync();

    expect(upload).toHaveBeenCalledWith(file);
    expect(lastPosted(fake)).toMatchObject({
      type: 'command',
      name: 'insertMedia',
      media: { url: '/media/' + 'a'.repeat(64), name: 'a'.repeat(64), mime: 'image/png' },
    });
  });

  it('answers a frame upload request with the uploaded media', async () => {
    const fake = mountField();
    const file = new File(['sound'], 'sound.mp3', { type: 'audio/mpeg' });
    message(fake, { type: 'upload', id: 'u1', file, kind: 'sound' });
    await flushAsync();
    expect(lastPosted(fake)).toMatchObject({ type: 'uploaded', id: 'u1', name: 'a'.repeat(64) });
  });
});

describe('RichTextField ruby and speech dialogs', () => {
  function input(testid: string): HTMLInputElement {
    return document.querySelector(`[data-testid="${testid}"]`) as HTMLInputElement;
  }

  function setValue(testid: string, next: string): void {
    const element = input(testid);
    element.value = next;
    element.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
  }

  function submit(testid: string): void {
    (document.querySelector(`[data-testid="${testid}"]`) as HTMLButtonElement).closest('form')!.requestSubmit();
    flushSync();
  }

  let stored: Map<string, string>;
  beforeEach(() => {
    // happy-dom leaves window.localStorage undefined under this configuration.
    stored = new Map();
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: (key: string) => stored.get(key) ?? null,
        setItem: (key: string, next: string) => void stored.set(key, String(next)),
      },
    });
  });

  it('asks the frame for the selection and prefills the ruby dialog with it', async () => {
    const fake = mountField();
    (target.querySelector('[data-testid="field-insert-ruby"]') as HTMLButtonElement).click();
    expect(lastPosted(fake)).toEqual({ type: 'selection' });
    expect(input('field-ruby-base')).toBeNull();

    message(fake, { type: 'selection', text: ' 漢字 ' });
    await flushAsync();
    expect(input('field-ruby-base').value).toBe('漢字');

    setValue('field-ruby-reading', 'かんじ');
    submit('field-ruby-submit');
    expect(lastPosted(fake)).toMatchObject({ type: 'command', name: 'insertRuby', base: '漢字', reading: 'かんじ' });
  });

  it('keeps the ruby insert disabled until both parts are filled', async () => {
    const fake = mountField();
    (target.querySelector('[data-testid="field-insert-ruby"]') as HTMLButtonElement).click();
    message(fake, { type: 'selection', text: '' });
    await flushAsync();
    const button = document.querySelector('[data-testid="field-ruby-submit"]') as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    setValue('field-ruby-base', '字');
    expect(button.disabled).toBe(true);
    setValue('field-ruby-reading', 'じ');
    expect(button.disabled).toBe(false);
  });

  it('inserts speech with the language, speed and visibility, and remembers the language', async () => {
    const fake = mountField();
    (target.querySelector('[data-testid="field-insert-tts"]') as HTMLButtonElement).click();
    message(fake, { type: 'selection', text: 'bonjour' });
    await flushAsync();
    expect(input('field-tts-text').value).toBe('bonjour');

    setValue('field-tts-lang', 'fr-FR');
    setValue('field-tts-speed', '0.9');
    submit('field-tts-submit');
    expect(lastPosted(fake)).toMatchObject({
      type: 'command',
      name: 'insertTts',
      text: 'bonjour',
      lang: 'fr_FR',
      speed: '0.9',
      visible: true,
    });
    expect(stored.get('engram-editor-tts-lang')).toBe('fr_FR');
  });

  it('refuses a malformed language or speed with a message', async () => {
    const fake = mountField();
    (target.querySelector('[data-testid="field-insert-tts"]') as HTMLButtonElement).click();
    message(fake, { type: 'selection', text: 'hi' });
    await flushAsync();
    const button = document.querySelector('[data-testid="field-tts-submit"]') as HTMLButtonElement;

    setValue('field-tts-lang', 'en US');
    expect(document.querySelector('[data-testid="field-tts-lang-error"]')).not.toBeNull();
    expect(button.disabled).toBe(true);

    setValue('field-tts-lang', 'en_US');
    setValue('field-tts-speed', '0');
    expect(document.querySelector('[data-testid="field-tts-speed-error"]')).not.toBeNull();
    expect(button.disabled).toBe(true);
  });
});

describe('RichTextField field options', () => {
  it('keeps the value after a reset only when the field is sticky', () => {
    const sticky = mountField({ value: '<p>kept</p>', options: { sticky: true } });
    instance?.reset();
    expect(value).toBe('<p>kept</p>');
    expect(posted(sticky).some((entry) => entry.type === 'load' && entry.html === '')).toBe(false);

    unmount(instance!);
    const free = mountField({ value: '<p>cleared</p>' });
    instance?.reset();
    expect(value).toBe('');
    expect(lastLoad(free)).toMatchObject({ type: 'load', html: '' });
  });

  it('renders no toolbar for a plain-text field and tells the frame', () => {
    const fake = mountField({ options: { plainText: true } });
    expect(target.querySelector('[data-testid="field-toolbar"]')).toBeNull();
    expect(target.querySelector('[data-testid="field-source-toggle"]')).toBeNull();
    expect(lastLoad(fake)).toMatchObject({ type: 'load', plainText: true });
  });

  it('keeps a collapsed field closed until the writer opens it', () => {
    mountField({ options: { collapsed: true } });
    expect(target.querySelector('[data-testid="field"]')).toBeNull();
    const toggle = target.querySelector('[data-testid="field-collapse-toggle"]') as HTMLButtonElement;
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    toggle.click();
    flushSync();
    expect(target.querySelector('[data-testid="field"]')).not.toBeNull();
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
  });
});

describe('RichTextField media library picker', () => {
  const sha = 'b'.repeat(64);
  const name = `${sha}.png`;
  const libraryItem = { sha256: sha, mime: 'image/png', bytes: 12, url: `/media/${sha}`, created_at: '2026-11-01T00:00:00Z', names: [], decks: [], state: 'referenced' as const, cleanup_at: null };

  function libraryButton(): HTMLButtonElement | null {
    return target.querySelector('[data-testid="field-insert-library"]') as HTMLButtonElement | null;
  }

  function pickerDialog(): HTMLElement | null {
    return document.body.querySelector('[data-testid="field-media-picker"]');
  }

  it('offers the library button only when a picker callback is provided', () => {
    mountField();
    expect(libraryButton()).toBeNull();
  });

  it('inserts a chosen library item without reloading the field', async () => {
    vi.spyOn(apiClient, 'listMediaForPicker').mockResolvedValue({ items: [libraryItem], next_cursor: '' });
    const chosen = vi.fn(async () => ({ url: name, name, mime: 'image/png' }));
    const fake = mountField({ pickMedia: chosen, mediaDeckId: 'deck-1' });
    const loadsBefore = posted(fake).filter((entry) => entry.type === 'load').length;

    libraryButton()!.click();
    await flushAsync();

    expect(pickerDialog()).toBeTruthy();
    const item = document.body.querySelector(`[data-testid="field-media-picker-item-${sha}"]`) as HTMLButtonElement | null;
    expect(item).toBeTruthy();
    item!.click();
    await flushAsync();

    expect(chosen).toHaveBeenCalledWith(libraryItem);
    expect(lastPosted(fake)).toMatchObject({
      type: 'command',
      name: 'insertMedia',
      media: { url: name, name, mime: 'image/png' },
    });
    // Opening the dialog and choosing the item never reloads the field, so the frame's saved caret is still the insert point.
    expect(posted(fake).filter((entry) => entry.type === 'load').length).toBe(loadsBefore);
  });

  it('reports a rejected selection and keeps the picker open', async () => {
    vi.spyOn(apiClient, 'listMediaForPicker').mockResolvedValue({ items: [libraryItem], next_cursor: '' });
    const chosen = vi.fn(async () => {
      throw new Error('refused');
    });
    mountField({ pickMedia: chosen });

    libraryButton()!.click();
    await flushAsync();
    (document.body.querySelector(`[data-testid="field-media-picker-item-${sha}"]`) as HTMLButtonElement).click();
    await flushAsync();

    expect(chosen).toHaveBeenCalledTimes(1);
    expect(toast.error).toHaveBeenCalled();
    expect(pickerDialog()).toBeTruthy();
  });
});
