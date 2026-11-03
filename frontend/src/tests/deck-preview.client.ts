import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckPreviewView from '../lib/views/DeckPreviewView.svelte';
import { apiClient } from '../lib/api';
import type { CardFace, DeckPreviewCard, DeckPreviewSort } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';

/**
 * Deck content preview: both rendered faces of the current card, previous/next navigation over a
 * paged deck, the position counter, the order switch and the empty/failed states. The card faces
 * themselves are the card frame's job and covered by the card-frame tests; here the page only has
 * to hand them over and move the cursor.
 */
describe('deck preview page', () => {
  let instance: ReturnType<typeof mount> | null = null;
  let target: HTMLElement;

  afterEach(() => {
    if (instance) unmount(instance);
    instance = null;
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  async function flush(): Promise<void> {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    flushSync();
  }

  function face(side: 'front' | 'back'): CardFace {
    return { html: `<p>${side}</p>`, css: '.card{}', body_class: 'card', side, media_base: '' };
  }

  function card(index: number, noteType = 'Basic'): DeckPreviewCard {
    return {
      card_id: `c${index}`,
      note_id: `n${index}`,
      note_type: noteType,
      template: 'Card 1',
      ordinal: 0,
      front: face('front'),
      back: face('back'),
      type_in: false,
      answer_includes_question: true,
      empty: false,
      edit_href: `/decks/d1/notes/n${index}`,
    };
  }

  function pageCards(page: number, perPage: number, total: number): DeckPreviewCard[] {
    const start = (page - 1) * perPage;
    return Array.from({ length: Math.max(0, Math.min(perPage, total - start)) }, (_, i) => card(start + i));
  }

  function mountAtRoute(): void {
    setLocale('en');
    window.history.replaceState({}, '', '/decks/d1/preview');
    routeStore.set(matchRoute('/decks/d1/preview'));
    target = document.createElement('div');
    document.body.appendChild(target);
  }

  async function mountPreview(total: number): Promise<void> {
    mountAtRoute();
    vi.spyOn(apiClient, 'deckPreviewCards').mockImplementation(
      async (_deckId: string, input: { page?: number; perPage?: number; sort?: DeckPreviewSort } = {}) => {
        const page = input.page ?? 1;
        const sort = input.sort ?? 'created';
        return { cards: pageCards(page, 20, total), total, page, per_page: 20, sort };
      },
    );
    instance = mount(DeckPreviewView, { target });
    await flush();
  }

  function el(testId: string): HTMLElement | null {
    return target.querySelector(`[data-testid="${testId}"]`);
  }

  function text(testId: string): string {
    return el(testId)?.textContent?.trim() ?? '';
  }

  it('renders both faces of the first card and the position counter', async () => {
    await mountPreview(21);
    expect(text('preview-position')).toBe('1 / 21');
    expect(el('preview-prev')?.hasAttribute('disabled')).toBe(true);
    expect(el('preview-next')?.hasAttribute('disabled')).toBe(false);
    // One frame for the front face and one for the back face of the same card.
    expect(target.querySelectorAll('iframe').length).toBeGreaterThanOrEqual(2);
    expect(text('preview-meta')).toContain('Basic');
    expect(apiClient.deckPreviewCards).toHaveBeenCalledWith('d1', { page: 1, perPage: 20, sort: 'created' }, { frame: true });
  });

  it('moves inside a fetched page without a request and crosses the page boundary once', async () => {
    await mountPreview(21);
    for (let i = 0; i < 20; i++) {
      el('preview-next')!.click();
      await flush();
    }
    expect(text('preview-position')).toBe('21 / 21');
    expect(el('preview-next')?.hasAttribute('disabled')).toBe(true);
    expect(el('preview-prev')?.hasAttribute('disabled')).toBe(false);
    expect(apiClient.deckPreviewCards).toHaveBeenCalledTimes(2);
    expect(apiClient.deckPreviewCards).toHaveBeenLastCalledWith(
      'd1',
      { page: 2, perPage: 20, sort: 'created' },
      { frame: true },
    );
    el('preview-prev')!.click();
    await flush();
    expect(text('preview-position')).toBe('20 / 21');
    expect(apiClient.deckPreviewCards).toHaveBeenCalledTimes(2);
  });

  it('resets the cursor and refetches when the order switches to note type', async () => {
    await mountPreview(21);
    el('preview-next')!.click();
    await flush();
    expect(text('preview-position')).toBe('2 / 21');

    (el('preview-sort') as HTMLElement).click();
    await flush();
    const option = Array.from(document.body.querySelectorAll('[role="option"]')).find(
      (entry) => entry.textContent?.trim() === 'Note type',
    );
    expect(option, 'note type option').toBeTruthy();
    // The select commits on pointerup; a plain click is ignored by the component.
    option!.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerType: 'mouse' }));
    await flush();

    expect(text('preview-position')).toBe('1 / 21');
    expect(apiClient.deckPreviewCards).toHaveBeenLastCalledWith(
      'd1',
      { page: 1, perPage: 20, sort: 'note_type' },
      { frame: true },
    );
  });

  it('shows the empty state for a deck with no cards', async () => {
    await mountPreview(0);
    expect(el('preview-empty')).not.toBeNull();
    expect(el('preview-card')).toBeNull();
  });

  it('shows a retry action when the page cannot be loaded', async () => {
    mountAtRoute();
    const spy = vi
      .spyOn(apiClient, 'deckPreviewCards')
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ cards: pageCards(1, 20, 1), total: 1, page: 1, per_page: 20, sort: 'created' });
    instance = mount(DeckPreviewView, { target });
    await flush();
    expect(el('preview-failed')).not.toBeNull();

    el('preview-retry')!.click();
    await flush();
    expect(el('preview-failed')).toBeNull();
    expect(text('preview-position')).toBe('1 / 1');
    expect(spy).toHaveBeenCalledTimes(2);
  });
});
