import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import CardFrame from '../lib/components/CardFrame.svelte';
import Harness from './card-frame-harness.svelte';

/**
 * The parent side of the frame protocol.
 *
 * happy-dom gives a mounted iframe a `contentWindow`, so the tests can do exactly what a browser does: post a message into the
 * parent with that window as `source`, and spy on what the component sends back. The frame document itself never loads here.
 */

let instance: ReturnType<typeof mount> | null = null;

function frame(target: HTMLElement): HTMLIFrameElement {
  const element = target.querySelector<HTMLIFrameElement>('iframe[data-card-frame]');
  if (!element) throw new Error('no card frame rendered');
  return element;
}

/** Delivers one message to the parent page as if it came from `source`. */
function receive(source: MessageEventSource | null, data: unknown): void {
  window.dispatchEvent(new MessageEvent('message', { data, source }));
}

function posted(win: Window): Array<Record<string, unknown>> {
  return ((win.postMessage as unknown as { mock?: { calls: unknown[][] } }).mock?.calls ?? []).map(
    (call) => call[0] as Record<string, unknown>,
  );
}

/** Lets the effect that sends `show` run after the ready message. */
async function flush(): Promise<void> {
  for (let i = 0; i < 5; i += 1) await Promise.resolve();
  flushSync();
}

beforeEach(() => {
  document.documentElement.className = '';
});

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  document.body.innerHTML = '';
  vi.restoreAllMocks();
});

function mountFrame(): HTMLElement {
  const target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(CardFrame, {
    target,
    props: { html: '<p>face</p>', css: '#qa{color:red}', bodyClass: 'card', side: 'question', mediaBase: '/fm/token/', title: 'Card face' },
  });
  flushSync();
  return target;
}

describe('CardFrame outbound messages', () => {
  it('sends the face only after ready, and sends it again when a prop changes', async () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(Harness, { target, props: { html: '<p>one</p>' } });
    flushSync();

    const win = frame(target).contentWindow!;
    vi.spyOn(win, 'postMessage').mockImplementation(() => {});
    expect(posted(win)).toEqual([]);

    receive(win, { type: 'ready' });
    await flush();
    expect(posted(win)).toContainEqual({
      type: 'show',
      html: '<p>one</p>',
      css: '#qa{font-size:1rem}',
      bodyClass: 'card',
      side: 'question',
      mediaBase: '/fm/token/',
      autoplay: false,
    });

    (target.querySelector('[data-testid="harness-next"]') as HTMLButtonElement).click();
    await flush();
    const shows = posted(win).filter((message) => message.type === 'show');
    expect(shows[shows.length - 1]?.html).toBe('<p>two</p>');
  });

  it('sends the current theme once after ready and again when the html class changes', async () => {
    const target = mountFrame();
    const win = frame(target).contentWindow!;
    vi.spyOn(win, 'postMessage').mockImplementation(() => {});

    receive(win, { type: 'ready' });
    await flush();
    expect(posted(win)).toContainEqual({ type: 'theme', dark: false });

    document.documentElement.classList.add('dark');
    await flush();
    expect(posted(win)).toContainEqual({ type: 'theme', dark: true });
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });

  it('opens a reported link in a new tab with noopener, and refuses a non-http target', async () => {
    const target = mountFrame();
    const win = frame(target).contentWindow!;
    vi.spyOn(win, 'postMessage').mockImplementation(() => {});
    const open = vi.spyOn(window, 'open').mockImplementation(() => null);

    receive(win, { type: 'link', href: 'https://example.com/page' });
    expect(open).toHaveBeenCalledWith('https://example.com/page', '_blank', 'noopener,noreferrer');

    open.mockClear();
    receive(win, { type: 'link', href: 'javascript:alert(1)' });
    expect(open).not.toHaveBeenCalled();
  });
});

describe('CardFrame inbound messages', () => {
  it('applies a height only from its own iframe', () => {
    const target = mountFrame();
    const win = frame(target).contentWindow!;
    vi.spyOn(win, 'postMessage').mockImplementation(() => {});

    receive(window, { type: 'height', px: 250 });
    flushSync();
    expect(frame(target).style.height).not.toBe('250px');

    const other = document.createElement('iframe');
    document.body.appendChild(other);
    receive(other.contentWindow, { type: 'height', px: 251 });
    flushSync();
    expect(frame(target).style.height).not.toBe('251px');

    receive(win, { type: 'height', px: 250 });
    flushSync();
    expect(frame(target).style.height).toBe('250px');
  });

  it('ignores a malformed height and a malformed key, and forwards a valid key', () => {
    const target = mountFrame();
    const win = frame(target).contentWindow!;
    vi.spyOn(win, 'postMessage').mockImplementation(() => {});

    receive(win, { type: 'height', px: 'tall' });
    receive(win, { type: 'height', px: -5 });
    receive(win, { type: 'key', key: 'a', code: 'KeyA', ctrl: 'yes' });
    flushSync();
    expect(frame(target).style.height).toBe('0px');
  });

  it('forwards a valid key to the caller', () => {
    const onkey = vi.fn();
    const target = document.createElement('div');
    document.body.appendChild(target);
    instance = mount(CardFrame, {
      target,
      props: { html: '<p>face</p>', css: '', bodyClass: '', side: 'question', mediaBase: '', onkey, title: 'Card face' },
    });
    flushSync();
    const win = frame(target).contentWindow!;
    vi.spyOn(win, 'postMessage').mockImplementation(() => {});

    const message = { type: 'key', key: ' ', code: 'Space', ctrl: false, alt: false, shift: false, meta: false };
    receive(win, message);
    expect(onkey).toHaveBeenCalledWith({
      key: ' ',
      code: 'Space',
      ctrl: false,
      alt: false,
      shift: false,
      meta: false,
    });

    // The same message from another window is not this frame's key press.
    onkey.mockClear();
    receive(window, message);
    expect(onkey).not.toHaveBeenCalled();
  });

  it('renders the sandboxed iframe of the current build', () => {
    const target = mountFrame();
    const element = frame(target);
    expect(element.getAttribute('sandbox')).toBe('allow-scripts');
    expect(element.getAttribute('allow')).toBe('autoplay');
    expect(element.getAttribute('title')).toBe('Card face');
    expect(element.getAttribute('src')).toBe('/card-frame/dev');
  });
});
