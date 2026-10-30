import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import RichTextField from '../lib/components/RichTextField.svelte';

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
