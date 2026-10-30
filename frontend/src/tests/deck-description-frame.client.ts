import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import { apiClient } from '../lib/api';
import type { Deck } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';
import { setCardTypes } from '../lib/card-types';

/**
 * The deck description is stored HTML (or Markdown rendered to HTML by the server) and is untrusted content, so it must live
 * only in the sandboxed frame. These cases mount the real view and follow the frame protocol the way a browser does: the
 * frame becomes ready, and the parent answers with the `show` message that carries the description.
 */

let instance: ReturnType<typeof mount> | null = null;

const scriptDescription = '<p>intro</p><script>window.__deck_description_ran = true</script>';

function baseDeck(overrides: Partial<Deck> = {}): Deck {
  return {
    id: 'd1',
    name: 'Biology',
    description: scriptDescription,
    description_html: scriptDescription,
    description_markdown: false,
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: '1',
    created_at: '2026-01-01T00:00:00Z',
    role: 'reader',
    ...overrides,
  };
}

function mountView(deck: Deck): HTMLElement {
  const target = document.createElement('div');
  document.body.appendChild(target);
  instance = mount(DeckDetailView, {
    target,
    props: { initialDeck: deck, initialNotes: [], initialTotal: 0, initialLoading: false },
  });
  flushSync();
  return target;
}

function frameOf(target: HTMLElement): HTMLIFrameElement {
  const element = target.querySelector<HTMLIFrameElement>('iframe[data-card-frame]');
  if (!element) throw new Error('no description frame rendered');
  return element;
}

function receive(source: MessageEventSource | null, data: unknown): void {
  window.dispatchEvent(new MessageEvent('message', { data, source }));
}

function posted(win: Window): Array<Record<string, unknown>> {
  return ((win.postMessage as unknown as { mock?: { calls: unknown[][] } }).mock?.calls ?? []).map(
    (call) => call[0] as Record<string, unknown>,
  );
}

async function flush(): Promise<void> {
  for (let i = 0; i < 5; i += 1) await Promise.resolve();
  flushSync();
}

beforeEach(() => {
  setLocale('en');
  setCardTypes(null);
  routeStore.set(matchRoute('/decks/d1'));
  vi.spyOn(apiClient, 'getDeckQueueCounts').mockRejectedValue(new Error('unused in this test'));
});

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  document.body.innerHTML = '';
  vi.restoreAllMocks();
});

describe('DeckDetailView description frame', () => {
  it('keeps a stored script out of the parent and sends it only into the sandboxed frame', async () => {
    const target = mountView(baseDeck());

    // The parent page never receives the description markup: it is addressed to the frame with `show`.
    expect(document.body.innerHTML).not.toContain('window.__deck_description_ran');

    const frame = frameOf(target);
    expect(frame.getAttribute('sandbox')).toBe('allow-scripts');
    expect(frame.hasAttribute('data-card-frame')).toBe(true);

    const win = frame.contentWindow!;
    vi.spyOn(win, 'postMessage').mockImplementation(() => {});
    receive(win, { type: 'ready' });
    await flush();

    const show = posted(win).find((message) => message.type === 'show');
    expect(show?.html).toBe(scriptDescription);
    // The payload still never enters the parent document while the frame owns it.
    expect(document.body.innerHTML).not.toContain('window.__deck_description_ran');
  });

  it('shows the server-rendered Markdown instead of the Markdown source', async () => {
    const target = mountView(
      baseDeck({ description: '# Heading\n\n**bold**', description_html: '<h1>Heading</h1><p><strong>bold</strong></p>', description_markdown: true }),
    );

    // Neither the source nor the rendered HTML is inlined into the parent page.
    expect(document.body.innerHTML).not.toContain('# Heading');
    expect(document.body.innerHTML).not.toContain('<h1>Heading</h1>');

    const win = frameOf(target).contentWindow!;
    vi.spyOn(win, 'postMessage').mockImplementation(() => {});
    receive(win, { type: 'ready' });
    await flush();

    const show = posted(win).find((message) => message.type === 'show');
    expect(show?.html).toBe('<h1>Heading</h1><p><strong>bold</strong></p>');
  });
});
