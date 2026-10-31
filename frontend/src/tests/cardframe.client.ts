import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRuntime, type FrameRuntime } from '../cardframe/runtime';

/**
 * The parent window is faked: happy-dom does not implement postMessage across windows, and the runtime accepts messages only
 * from the exact object it was built with. This makes both the "foreign source is ignored" case and the outbound message
 * assertions exact instead of relying on the harness window.
 */
interface FakeParent {
  postMessage: ReturnType<typeof vi.fn>;
}

let parent: FakeParent;
let runtime: FrameRuntime | null = null;

function posted(): Array<Record<string, unknown>> {
  return parent.postMessage.mock.calls.map((call) => call[0] as Record<string, unknown>);
}

function fromParent(data: unknown, source: unknown = parent): MessageEvent {
  return new MessageEvent('message', { data, source: source as MessageEventSource });
}

function showMessage(overrides: Record<string, unknown> = {}): MessageEvent {
  return fromParent({
    type: 'show',
    html: '',
    css: '',
    bodyClass: '',
    side: 'question',
    mediaBase: '',
    autoplay: false,
    ordinal: 0,
    ...overrides,
  });
}

/** Lets the microtask queue drain so every awaited step before the next listener runs. */
async function flush(count = 5): Promise<void> {
  for (let i = 0; i < count; i += 1) await Promise.resolve();
}

beforeEach(() => {
  parent = { postMessage: vi.fn() };
  document.head.innerHTML = '';
  document.body.innerHTML = '<div id="qa"></div>';
  delete (window as unknown as { MathJax?: unknown }).MathJax;
  delete (window as unknown as { onUpdateHook?: unknown }).onUpdateHook;
  delete (window as unknown as { onShownHook?: unknown }).onShownHook;
});

afterEach(() => {
  runtime?.destroy();
  runtime = null;
  vi.restoreAllMocks();
});

describe('card frame script re-execution', () => {
  it('replaces inline and external scripts in document order and waits for the external load', async () => {
    const created: HTMLScriptElement[] = [];
    const createElement = document.createElement.bind(document);
    vi.spyOn(document, 'createElement').mockImplementation(((tag: string, options?: ElementCreationOptions) => {
      const element = createElement(tag, options);
      if (tag === 'script') created.push(element as HTMLScriptElement);
      return element;
    }) as typeof document.createElement);

    runtime = createRuntime({ parent: parent as unknown as Window });
    const done = runtime.handleMessage(
      showMessage({
        html:
          '<script>window.first = true</script>' +
          '<script type="application/json" src="https://example.com/external.js"></script>' +
          '<script>window.third = true</script>',
      }),
    );
    await flush();

    // The inline script was replaced (and therefore executed synchronously in a real browser) before the external load resolved.
    const external = created.find((script) => script.src.endsWith('/external.js'));
    expect(external).toBeTruthy();
    expect(external?.isConnected).toBe(true);
    // The inline script is first, the external second; the trailing inline script has not been processed yet.
    expect(created).toHaveLength(2);
    expect(created[0]?.textContent).toContain('window.first = true');
    expect(created[0]?.isConnected).toBe(true);
    expect(created[1]).toBe(external);

    // Simulate the external script's load (happy-dom does not execute dynamically inserted scripts).
    external?.dispatchEvent(new Event('load'));
    await done;
    await flush();

    expect(created).toHaveLength(3);
    expect(created[2]?.textContent).toContain('window.third = true');
    expect(created[2]?.isConnected).toBe(true);
  });

  it('keeps an external script element in place when the load fails, without blocking the sequence', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    const done = runtime.handleMessage(
      showMessage({
        html: '<script type="application/json" src="https://example.com/missing.js"></script><script>window.after = true</script>',
      }),
    );
    await flush();
    document.querySelector('#qa script[src]')?.dispatchEvent(new Event('error'));
    await done;
    expect(document.querySelector('#qa')?.querySelectorAll('script')).toHaveLength(2);
  });
});

describe('card frame show sequence', () => {
  it('runs onUpdateHook, then typesetting, then onShownHook', async () => {
    const calls: string[] = [];
    (window as unknown as { onUpdateHook: () => void }).onUpdateHook = () => calls.push('update');
    (window as unknown as { onShownHook: () => void }).onShownHook = () => calls.push('shown');
    (window as unknown as { MathJax: unknown }).MathJax = {
      typesetPromise: async () => {
        calls.push('typeset');
      },
    };

    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ html: '<p>Solve \\(x^2\\)</p>' }));

    expect(calls).toEqual(['update', 'typeset', 'shown']);
  });

  it('skips typesetting when the content has no math', async () => {
    const calls: string[] = [];
    (window as unknown as { onUpdateHook: () => void }).onUpdateHook = () => calls.push('update');
    (window as unknown as { onShownHook: () => void }).onShownHook = () => calls.push('shown');
    (window as unknown as { MathJax: unknown }).MathJax = {
      typesetPromise: async () => {
        calls.push('typeset');
      },
    };

    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ html: '<p>plain</p>' }));

    expect(calls).toEqual(['update', 'shown']);
  });

  it('applies the head, classes and content base', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(
      showMessage({
        html: '<div id="answer">answer</div>',
        css: '.card{color:red}',
        bodyClass: 'card card-answer',
        side: 'answer',
        mediaBase: '/fm/tok123/',
      }),
    );

    expect(document.head.querySelectorAll('style')).toHaveLength(1);
    expect(document.head.querySelector('style')?.textContent).toBe('.card{color:red}');
    expect(document.head.querySelector('base')?.getAttribute('href')).toBe('/fm/tok123/');
    expect(document.body.className).toBe('card card-answer card1');
    expect(document.getElementById('qa')?.innerHTML).toContain('id="answer"');
  });

  it('toggles dark on the frame document', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });

    await runtime.handleMessage(fromParent({ type: 'theme', dark: true }));
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(document.documentElement.style.colorScheme).toBe('dark');
  });

  it('keeps the frame document stylesheet and attaches the shortcode controls inside the frame', async () => {
    document.head.innerHTML = '<link rel="stylesheet" href="/assets/card-frame.css" />';
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(
      showMessage({
        html: '<p><span class="engram-audio" data-audio-src="/fm/token/clip.mp3">clip.mp3</span></p>',
        css: '#qa{font-size:1.25rem}',
      }),
    );

    // The runtime bundle's own stylesheet must survive a show: it carries the tokens and utility classes the controls use.
    expect(document.head.querySelector('link[rel="stylesheet"]')).not.toBeNull();
    const style = document.head.querySelector('style[data-card-css]');
    expect(style?.textContent).toBe('#qa{font-size:1.25rem}');
    expect(document.querySelector('#qa button[data-shortcode-control]')).not.toBeNull();
  });

  it('rewrites one card stylesheet element instead of adding one per show', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ css: '#qa{color:red}' }));
    await runtime.handleMessage(showMessage({ css: '#qa{color:blue}' }));

    expect(document.head.querySelectorAll('style')).toHaveLength(1);
    expect(document.head.querySelector('style')?.textContent).toBe('#qa{color:blue}');
  });

  it('does nothing for a malformed show message', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ autoplay: 'yes' }));
    expect(document.getElementById('qa')?.innerHTML).toBe('');
  });
});

describe('card frame message origin', () => {
  it('ignores a show whose source is not the parent window', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(fromParent({ type: 'show', html: '<p>spoofed</p>', css: '', bodyClass: '', side: 'question', mediaBase: '', autoplay: false, ordinal: 0 }, window));

    expect(document.getElementById('qa')?.innerHTML).toBe('');
    expect(document.head.querySelector('style')).toBeNull();
    expect(posted()).toEqual([]);
  });
});

describe('card frame outbound messages', () => {
  it('posts link instead of navigating on an https anchor click', () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    const anchor = document.createElement('a');
    anchor.href = 'https://example.com/page';
    anchor.textContent = 'external';
    document.getElementById('qa')?.appendChild(anchor);

    const click = new MouseEvent('click', { bubbles: true, cancelable: true });
    const notCancelled = anchor.dispatchEvent(click);

    expect(notCancelled).toBe(false);
    expect(posted()).toContainEqual({ type: 'link', href: 'https://example.com/page' });
  });

  it('leaves a non-http link alone', () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    const anchor = document.createElement('a');
    anchor.href = 'mailto:someone@example.com';
    document.body.appendChild(anchor);

    anchor.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

    expect(posted().some((message) => message.type === 'link')).toBe(false);
  });

  it('posts ready once at startup and keys except inside an input', () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    runtime.signalReady();

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', code: 'KeyA', ctrlKey: true }));
    expect(posted()).toContainEqual({
      type: 'key',
      key: 'a',
      code: 'KeyA',
      ctrl: true,
      alt: false,
      shift: false,
      meta: false,
    });

    const input = document.createElement('input');
    document.body.appendChild(input);
    input.focus();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'b', code: 'KeyB' }));
    expect(posted().some((message) => message.key === 'b')).toBe(false);
  });
});

describe('card frame height report', () => {
  it('adds the body padding and margin to the content height, so the frame never scrolls', () => {
    let callback: ResizeObserverCallback | null = null;
    class FakeObserver {
      constructor(cb: ResizeObserverCallback) {
        callback = cb;
      }
      observe(): void {}
      disconnect(): void {}
    }
    vi.stubGlobal('ResizeObserver', FakeObserver);
    document.body.style.padding = '2px 4px 3px';
    document.body.style.margin = '1px 0 0';
    runtime = createRuntime({ parent: parent as unknown as Window });
    const root = document.getElementById('qa')!;
    callback!([{ target: root, contentRect: { height: 100 } } as unknown as ResizeObserverEntry], {} as ResizeObserver);
    expect(posted()).toContainEqual({ type: 'height', px: 106 });
    vi.unstubAllGlobals();
    document.body.removeAttribute('style');
  });
});

describe('card frame shown report', () => {
  it('reports shown with the parent seq after the face is in the document', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ html: '<p id="face">face</p>', seq: 7 }));
    expect(document.getElementById('face')).not.toBeNull();
    expect(posted()).toContainEqual({ type: 'shown', seq: 7, answerTop: null });
  });

  it('reports seq 0 for a show without one', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ html: '<p>face</p>' }));
    expect(posted()).toContainEqual({ type: 'shown', seq: 0, answerTop: null });
  });

  it('still reports shown when a face hook throws', async () => {
    (window as unknown as { onShownHook?: () => void }).onShownHook = () => {
      throw new Error('card script failed');
    };
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ html: '<p>face</p>', seq: 3 })).catch(() => {});
    expect(posted()).toContainEqual({ type: 'shown', seq: 3, answerTop: null });
  });

  it('reports where #answer sits on an answer face, so the parent can scroll to it', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    const rect = vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
      return { top: this.id === 'answer' ? 240.4 : 0, height: 0 } as DOMRect;
    });
    await runtime.handleMessage(showMessage({ html: '<p>Q</p><hr id=answer><p>A</p>', side: 'answer', seq: 2 }));
    rect.mockRestore();
    expect(posted()).toContainEqual({ type: 'shown', seq: 2, answerTop: 240 });
  });

  it('reports no answer position for an answer face without #answer or for a question face', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(showMessage({ html: '<p>A</p>', side: 'answer', seq: 1 }));
    await runtime.handleMessage(showMessage({ html: '<p>Q</p><hr id=answer>', side: 'question', seq: 2 }));
    expect(posted()).toContainEqual({ type: 'shown', seq: 1, answerTop: null });
    expect(posted()).toContainEqual({ type: 'shown', seq: 2, answerTop: null });
  });

  it('posts the face height before shown, so the parent scrolls a frame that already has its size', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    const rect = vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
      return { top: 0, height: this.id === 'qa' ? 321 : 0 } as DOMRect;
    });
    await runtime.handleMessage(showMessage({ html: '<p>tall</p>', side: 'answer', seq: 4 }));
    rect.mockRestore();
    const types = posted().map((message) => message.type);
    expect(posted()).toContainEqual({ type: 'height', px: 321 });
    expect(types.lastIndexOf('height')).toBeLessThan(types.indexOf('shown'));
  });

  it('puts the caret in a question face input unless the show turns focus off', async () => {
    const cases: Array<{ focus?: boolean; side: string; want: boolean }> = [
      { side: 'question', want: true },
      { focus: true, side: 'question', want: true },
      { focus: false, side: 'question', want: false },
      { focus: true, side: 'answer', want: false },
    ];
    for (const { focus, side, want } of cases) {
      runtime?.destroy();
      document.body.innerHTML = '<div id="qa"></div>';
      (document.activeElement as HTMLElement | null)?.blur?.();
      runtime = createRuntime({ parent: parent as unknown as Window });
      const extra = focus === undefined ? {} : { focus };
      await runtime.handleMessage(showMessage({ html: '<p>Q</p><input id="typeans" type="text">', side, ...extra }));
      expect(document.activeElement === document.getElementById('typeans'), `focus ${String(focus)} on ${side}`).toBe(want);
    }
  });

  it('does not report shown for a show from a foreign source', async () => {
    runtime = createRuntime({ parent: parent as unknown as Window });
    await runtime.handleMessage(fromParent({ type: 'show', html: '<p>x</p>', css: '', bodyClass: '', side: 'question', mediaBase: '', autoplay: false, ordinal: 0, seq: 1 }, window));
    expect(posted().some((message) => message.type === 'shown')).toBe(false);
  });
});
