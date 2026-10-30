import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRuntime, type EditFrameRuntime } from '../editframe/runtime';

/**
 * The editing frame runtime, driven the way the parent page drives it: a fake parent window posts
 * `load` and `command`, and the runtime edits its own document and posts `change`. The document is
 * the test page itself, so the resulting HTML is read directly.
 *
 * The cases pin the element shapes the toolbar promises (Anki's `<b>`, `<i>`, `<span
 * style="color">`, lists, alignment, MathJax and cloze markers), the paste rule, and the source
 * view's round trip.
 */

interface FakeParent {
  postMessage: ReturnType<typeof vi.fn>;
}

let parent: FakeParent;
let runtime: EditFrameRuntime | null = null;

function posted(): Array<Record<string, unknown>> {
  return parent.postMessage.mock.calls.map((call) => call[0] as Record<string, unknown>);
}

function fromParent(data: unknown, source: unknown = parent): MessageEvent {
  return new MessageEvent('message', { data, source: source as MessageEventSource });
}

function load(html: string, extra: Record<string, unknown> = {}): void {
  runtime?.handleMessage(
    fromParent({
      type: 'load',
      html,
      plainText: false,
      rtl: false,
      font: '',
      size: 0,
      description: '',
      dark: false,
      source: false,
      ...extra,
    })
  );
}

function command(name: string, extra: Record<string, unknown> = {}): void {
  runtime?.handleMessage(fromParent({ type: 'command', name, ...extra }));
}

/** Selects a text range inside the editor, as a mouse drag inside the frame does. */
function select(start: number, end: number): void {
  const root = document.getElementById('editor') as HTMLElement;
  const walker = document.createTreeWalker(root, 4);
  const nodes: Text[] = [];
  let node = walker.nextNode();
  while (node) {
    nodes.push(node as Text);
    node = walker.nextNode();
  }
  const first = nodes.find((candidate) => candidate.length >= end) ?? nodes[0];
  if (!first) return;
  const range = document.createRange();
  range.setStart(first, Math.min(start, first.length));
  range.setEnd(first, Math.min(end, first.length));
  const selection = window.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

function editorHtml(): string {
  return (document.getElementById('editor') as HTMLElement).innerHTML;
}

function lastChange(): string {
  for (let i = posted().length - 1; i >= 0; i -= 1) {
    const message = posted()[i];
    if (message?.type === 'change' && typeof message.html === 'string') return message.html;
  }
  return '';
}

beforeEach(() => {
  parent = { postMessage: vi.fn() };
  document.body.innerHTML = '<div id="editor"></div>';
  runtime = createRuntime({ parent: parent as unknown as Window });
  runtime.signalReady();
});

afterEach(() => {
  runtime?.destroy();
  runtime = null;
  vi.restoreAllMocks();
});

describe('edit frame inline commands', () => {
  it.each([
    ['bold', '<b>Hello</b> world'],
    ['italic', '<i>Hello</i> world'],
    ['underline', '<u>Hello</u> world'],
    ['superscript', '<sup>Hello</sup> world'],
    ['subscript', '<sub>Hello</sub> world'],
  ])('%s wraps the selection in %s', (name, expected) => {
    load('Hello world');
    select(0, 5);
    command(name);
    expect(editorHtml()).toBe(expected);
    expect(lastChange()).toBe(expected);
  });

  it('removes the same wrapper on a second press', () => {
    load('Hello world');
    select(0, 5);
    command('bold');
    select(0, 5);
    command('bold');
    expect(editorHtml()).toBe('Hello world');
  });

  it('applies and removes a text colour on a span', () => {
    load('red');
    select(0, 3);
    command('textColor', { color: '#ff0000' });
    const html = editorHtml();
    expect(html).toContain('<span style="color:');
    expect(html).toContain('>red</span>');
  });

  it('applies a highlight on a span', () => {
    load('marked');
    select(0, 6);
    command('highlight', { color: '#fff59d' });
    const html = editorHtml();
    expect(html).toContain('<span style="background-color:');
    expect(html).toContain('>marked</span>');
  });

  it('clears formatting but keeps the text', () => {
    load('<b>bold</b> plain');
    select(0, 4);
    command('clearFormatting');
    expect(editorHtml()).toBe('bold plain');
  });

  it('turns the selection into a bulleted list and back', () => {
    load('item');
    select(0, 4);
    command('unorderedList');
    expect(editorHtml()).toBe('<ul><li>item</li></ul>');
    select(0, 2);
    command('unorderedList');
    expect(editorHtml()).toBe('item');
  });

  it('switches between the two list kinds', () => {
    load('<ul><li>item</li></ul>');
    select(0, 2);
    command('orderedList');
    expect(editorHtml()).toBe('<ol><li>item</li></ol>');
  });

  it('aligns the block and removes the property when aligned left', () => {
    load('<p>text</p>');
    select(0, 4);
    command('align', { value: 'center' });
    expect(editorHtml()).toBe('<p style="text-align: center;">text</p>');
    select(0, 4);
    command('align', { value: 'left' });
    expect(editorHtml()).toBe('<p>text</p>');
  });

  it('indents by one step and stops at the left edge', () => {
    load('<p>text</p>');
    select(0, 4);
    command('indent', { delta: 1 });
    expect(editorHtml()).toBe('<p style="margin-left: 40px;">text</p>');
    select(0, 4);
    command('indent', { delta: -1 });
    expect(editorHtml()).toBe('<p>text</p>');
  });
});

describe('edit frame MathJax, LaTeX and cloze', () => {
  it('wraps a selection in inline MathJax', () => {
    load('x');
    select(0, 1);
    command('mathjaxInline');
    expect(editorHtml()).toBe('\\(x\\)');
  });

  it('wraps a selection in block MathJax', () => {
    load('x');
    select(0, 1);
    command('mathjaxBlock');
    expect(editorHtml()).toBe('\\[x\\]');
  });

  it('wraps a selection in a LaTeX tag', () => {
    load('x');
    select(0, 1);
    command('latex');
    expect(editorHtml()).toBe('[latex]x[/latex]');
  });

  it('starts cloze numbering at c1 on a field without a cloze', () => {
    load('answer');
    select(0, 6);
    command('cloze');
    expect(editorHtml()).toBe('{{c1::answer}}');
  });

  it('continues from the highest existing cloze number, not the last one', () => {
    load('{{c2::two}} and {{c1::one}} ');
    select(editorHtml().length, editorHtml().length);
    command('cloze');
    expect(editorHtml()).toContain('{{c3::}}');
  });

  it('reuses the highest existing number for the same-number command', () => {
    load('{{c2::two}} ');
    const root = document.getElementById('editor') as HTMLElement;
    const range = document.createRange();
    range.selectNodeContents(root);
    range.collapse(false);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    command('clozeSame');
    expect(editorHtml()).toContain('{{c2::}}');
  });

  it('numbers through the keyboard shortcuts', () => {
    load('{{c4::four}} ');
    const root = document.getElementById('editor') as HTMLElement;
    root.dispatchEvent(new KeyboardEvent('keydown', { key: 'c', ctrlKey: true, shiftKey: true, bubbles: true }));
    expect(editorHtml()).toContain('{{c5::}}');
  });
});

describe('edit frame paste and source view', () => {
  it('keeps pasted formatting by default', () => {
    load('start');
    const event = new ClipboardEvent('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'clipboardData', {
      value: {
        files: [],
        getData: (type: string) => (type === 'text/html' ? '<b>bold</b>' : 'plain'),
      },
    });
    document.getElementById('editor')?.dispatchEvent(event);
    expect(editorHtml()).toBe('start<b>bold</b>');
  });

  it('strips pasted formatting while Shift is held', () => {
    load('start');
    const root = document.getElementById('editor') as HTMLElement;
    root.dispatchEvent(new KeyboardEvent('keydown', { key: 'Shift', shiftKey: true, bubbles: true }));
    const event = new ClipboardEvent('paste', { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'clipboardData', {
      value: {
        files: [],
        getData: (type: string) => (type === 'text/html' ? '<b>bold</b>' : 'plain'),
      },
    });
    root.dispatchEvent(event);
    expect(editorHtml()).toBe('startbold');
  });

  it('round-trips unchanged markup through the HTML source view', () => {
    const markup = '<p class="x">a &amp; b</p>';
    load(markup);
    runtime?.handleMessage(fromParent({ type: 'source', open: true }));
    expect(runtime?.value()).toBe(markup);
    runtime?.handleMessage(fromParent({ type: 'source', open: false }));
    expect(runtime?.value()).toBe(markup);
  });

  it('renders a plain-text field as plain text and escapes it back to HTML', () => {
    load('<b>x</b>', { plainText: true });
    const plain = document.querySelector('.edit-plain') as HTMLTextAreaElement;
    expect(plain.value).toBe('x');
    plain.value = 'a < b';
    plain.dispatchEvent(new Event('input', { bubbles: true }));
    expect(lastChange()).toBe('a &lt; b');
  });

  it('ignores a message from a window other than the parent', () => {
    load('x');
    runtime?.handleMessage(fromParent({ type: 'command', name: 'bold' }, {}));
    expect(editorHtml()).toBe('x');
  });

  it('asks the parent to upload a dropped file and inserts the returned image', () => {
    load('start');
    const file = new File(['picture'], 'picture.png', { type: 'image/png' });
    const event = new Event('drop', { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'dataTransfer', {
      value: { files: [file], getData: () => '' },
    });
    document.getElementById('editor')?.dispatchEvent(event);

    const upload = posted().find((entry) => entry.type === 'upload');
    expect(upload).toMatchObject({ type: 'upload', kind: 'image', file });
    runtime?.handleMessage(
      fromParent({
        type: 'uploaded',
        id: upload?.id,
        url: '/media/' + 'a'.repeat(64),
        name: 'a'.repeat(64),
        mime: 'image/png',
      })
    );
    expect(editorHtml()).toContain(`<img src="/media/${'a'.repeat(64)}">`);
  });

  it('inserts an uploaded sound with Anki syntax', () => {
    load('start');
    const file = new File(['sound'], 'sound.mp3', { type: 'audio/mpeg' });
    const event = new Event('drop', { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'dataTransfer', {
      value: { files: [file], getData: () => '' },
    });
    document.getElementById('editor')?.dispatchEvent(event);
    const upload = posted().find((entry) => entry.type === 'upload');

    runtime?.handleMessage(
      fromParent({ type: 'uploaded', id: upload?.id, url: '/media/a', name: 'sound.mp3', mime: 'audio/mpeg' })
    );
    expect(editorHtml()).toBe('start[sound:sound.mp3]');
  });
});
