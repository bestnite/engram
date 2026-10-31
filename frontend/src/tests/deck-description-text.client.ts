import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckDetailView from '../lib/views/DeckDetailView.svelte';
import { apiClient } from '../lib/api';
import type { Deck } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';

/**
 * The deck description is plain text. Whatever it holds, markup included, is shown as text in the page: no frame is
 * needed, and a stored script is printed rather than run. Editing saves one line, because the server rejects a line
 * break in a description.
 */

let instance: ReturnType<typeof mount> | null = null;

const scriptDescription = '<p>intro</p><script>window.__deck_description_ran = true</script>';

function baseDeck(overrides: Partial<Deck> = {}): Deck {
  return {
    id: 'd1',
    name: 'Biology',
    description: scriptDescription,
    new_per_day: 20,
    reviews_per_day: 200,
    preset_id: '1',
    created_at: '2026-01-01T00:00:00Z',
    role: 'owner',
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

async function flush(): Promise<void> {
  for (let i = 0; i < 10; i += 1) await Promise.resolve();
  flushSync();
}

beforeEach(() => {
  setLocale('en');
  routeStore.set(matchRoute('/decks/d1'));
  vi.spyOn(apiClient, 'getDeckQueueCounts').mockRejectedValue(new Error('unused in this test'));
  delete (window as unknown as { __deck_description_ran?: boolean }).__deck_description_ran;
});

afterEach(() => {
  if (instance) unmount(instance);
  instance = null;
  document.body.innerHTML = '';
  vi.restoreAllMocks();
});

describe('DeckDetailView description', () => {
  it('prints stored markup as text, in the page and without a frame', () => {
    const target = mountView(baseDeck());
    const shown = target.querySelector('[data-testid="deck-description"]')!;
    expect(shown.textContent).toBe(scriptDescription);
    expect(shown.querySelector('p, script')).toBeNull();
    expect(target.querySelector('iframe')).toBeNull();
    expect((window as unknown as { __deck_description_ran?: boolean }).__deck_description_ran).toBeUndefined();
  });

  it('saves the edited description on one line', async () => {
    const updateDeck = vi
      .spyOn(apiClient, 'updateDeck')
      .mockResolvedValue({ ...baseDeck(), description: 'first second' } as never);
    const target = mountView(baseDeck({ description: 'old' }));
    (target.querySelector('[data-testid="deck-description"]') as HTMLElement).click();
    flushSync();

    const input = target.querySelector('[data-testid="deck-description-input"]') as HTMLTextAreaElement;
    input.value = 'first\nsecond';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    // Shift+Enter saves too: there is no line break to insert.
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', shiftKey: true, bubbles: true }));
    await flush();

    expect(updateDeck).toHaveBeenCalledWith('d1', { name: 'Biology', description: 'first second' });
    expect(target.querySelector('[data-testid="deck-description"]')?.textContent).toBe('first second');
  });
});
