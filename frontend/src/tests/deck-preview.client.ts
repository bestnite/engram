import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
import DeckPreviewView from '../lib/views/DeckPreviewView.svelte';
import { apiClient } from '../lib/api';
import type { CardFace, DeckPreviewCard, DeckPreviewNoteTypeSection, DeckPreviewSort } from '../lib/api';
import { routeStore, matchRoute } from '../lib/router';
import { setLocale } from '../lib/i18n';

/**
 * Deck content preview: both rendered faces of the current card, previous/next navigation over a
 * paged deck, the editable card number, the note type jump, note stepping, the order switch and the
 * empty/failed states. The card faces themselves are the card frame's job and covered by the
 * card-frame tests; here the page only has to hand them over and move the cursor.
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

  function pageCards(
    page: number,
    perPage: number,
    total: number,
    noteTypeFor: (index: number) => string,
  ): DeckPreviewCard[] {
    const start = (page - 1) * perPage;
    return Array.from({ length: Math.max(0, Math.min(perPage, total - start)) }, (_, i) =>
      card(start + i, noteTypeFor(start + i)),
    );
  }

  function mountAtRoute(): void {
    setLocale('en');
    window.history.replaceState({}, '', '/decks/d1/preview');
    routeStore.set(matchRoute('/decks/d1/preview'));
    target = document.createElement('div');
    document.body.appendChild(target);
  }

  interface MountOptions {
    /** The note type grouping the note type order response carries. */
    noteTypes?: DeckPreviewNoteTypeSection[];
    /** Maps a zero-based card position to its note type; defaults to one Basic note per card. */
    noteTypeFor?: (index: number) => string;
    /** The note step answers; defaults to no adjacent note in either direction. */
    noteStep?: (input: { from: number; direction: 'next' | 'prev' }) => Promise<{ position: number | null }>;
  }

  async function mountPreview(total: number, options: MountOptions = {}): Promise<void> {
    mountAtRoute();
    const noteTypeFor = options.noteTypeFor ?? (() => 'Basic');
    vi.spyOn(apiClient, 'deckPreviewCards').mockImplementation(
      async (_deckId: string, input: { page?: number; perPage?: number; sort?: DeckPreviewSort } = {}) => {
        const page = input.page ?? 1;
        const sort = input.sort ?? 'created';
        const noteTypes = sort === 'note_type' ? options.noteTypes : undefined;
        return {
          cards: pageCards(page, 20, total, noteTypeFor),
          total,
          page,
          per_page: 20,
          sort,
          ...(noteTypes ? { note_types: noteTypes } : {}),
        };
      },
    );
    vi.spyOn(apiClient, 'deckPreviewNoteStep').mockImplementation(
      async (_deckId: string, input: { from: number; direction: 'next' | 'prev' }) =>
        options.noteStep ? options.noteStep(input) : { position: null },
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

  /** The card number the position control currently shows. */
  function shownPosition(): string {
    return (el('preview-jump') as HTMLInputElement | null)?.value ?? '';
  }

  /** Types a value into the card number field and presses Enter after the typed value rendered. */
  async function typeJump(value: string): Promise<void> {
    const input = el('preview-jump') as HTMLInputElement;
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await flush();
  }

  /** Types a value into the card number field and leaves it, which commits the same jump. */
  async function typeBlurJump(value: string): Promise<void> {
    const input = el('preview-jump') as HTMLInputElement;
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await flush();
    input.dispatchEvent(new FocusEvent('blur'));
    await flush();
  }

  /** Selects one option of an open test-id select; the component commits on pointerup. */
  async function chooseOption(selectTestId: string, label: string): Promise<void> {
    (el(selectTestId) as HTMLElement).click();
    await flush();
    const option = Array.from(document.body.querySelectorAll('[role="option"]')).find(
      (entry) => entry.textContent?.trim() === label,
    );
    expect(option, `option ${label}`).toBeTruthy();
    option!.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerType: 'mouse' }));
    await flush();
  }

  it('renders both faces of the first card and the position control', async () => {
    await mountPreview(21);
    expect(shownPosition()).toBe('1');
    expect(text('preview-position')).toBe('/ 21');
    expect(text('preview-nav-position')).toBe('1 / 21');
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
    expect(shownPosition()).toBe('21');
    expect(text('preview-nav-position')).toBe('21 / 21');
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
    expect(shownPosition()).toBe('20');
    expect(apiClient.deckPreviewCards).toHaveBeenCalledTimes(2);
  });

  it('jumps to a typed card number, clamps an out-of-range number and ignores junk', async () => {
    await mountPreview(50);

    await typeJump('25');
    expect(shownPosition()).toBe('25');
    expect(apiClient.deckPreviewCards).toHaveBeenLastCalledWith(
      'd1',
      { page: 2, perPage: 20, sort: 'created' },
      { frame: true },
    );

    // Past the last card clamps to the total.
    await typeJump('999');
    expect(shownPosition()).toBe('50');
    expect(apiClient.deckPreviewCards).toHaveBeenLastCalledWith(
      'd1',
      { page: 3, perPage: 20, sort: 'created' },
      { frame: true },
    );

    // Before the first card clamps to 1, and junk keeps the cursor where it was.
    await typeJump('0');
    expect(shownPosition()).toBe('1');
    await typeJump('abc');
    expect(shownPosition()).toBe('1');

    // Leaving the field commits the typed number, so a click elsewhere does not discard it.
    await typeBlurJump('40');
    expect(shownPosition()).toBe('40');
  });

  it('offers the note type jump only in the note type order and lands on the section start', async () => {
    const noteTypes: DeckPreviewNoteTypeSection[] = [
      { name: 'Basic', count: 2, first_position: 1 },
      { name: 'Cloze', count: 3, first_position: 3 },
    ];
    await mountPreview(5, {
      noteTypes,
      noteTypeFor: (index) => (index < 2 ? 'Basic' : 'Cloze'),
    });
    expect(el('preview-type')).toBeNull();

    await chooseOption('preview-sort', 'Note type');
    expect(el('preview-type')?.textContent?.trim()).toContain('Basic');
    expect(apiClient.deckPreviewCards).toHaveBeenLastCalledWith(
      'd1',
      { page: 1, perPage: 20, sort: 'note_type' },
      { frame: true },
    );

    await chooseOption('preview-type', 'Cloze (3)');
    expect(shownPosition()).toBe('3');
    expect(text('preview-meta')).toContain('Cloze');
    expect(el('preview-type')?.textContent?.trim()).toContain('Cloze');
  });

  it('steps to the adjacent note and disables a direction the deck cannot satisfy', async () => {
    const noteStep = vi.fn(async ({ from, direction }: { from: number; direction: 'next' | 'prev' }) => {
      if (direction === 'next' && from === 1) return { position: 3 };
      return { position: null };
    });
    await mountPreview(5, { noteStep });

    el('preview-note-next')!.click();
    await flush();
    expect(shownPosition()).toBe('3');
    expect(noteStep).toHaveBeenCalledWith({ from: 1, direction: 'next', sort: 'created' });

    // From the third card the server reports no later note, so the button turns itself off.
    el('preview-note-next')!.click();
    await flush();
    expect(shownPosition()).toBe('3');
    expect(el('preview-note-next')?.hasAttribute('disabled')).toBe(true);

    el('preview-note-prev')!.click();
    await flush();
    expect(el('preview-note-prev')?.hasAttribute('disabled')).toBe(true);
  });

  it('steps by note with Shift+ArrowRight', async () => {
    const noteStep = vi.fn(async () => ({ position: 3 }));
    await mountPreview(5, { noteStep });
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', shiftKey: true, bubbles: true }));
    await flush();
    expect(noteStep).toHaveBeenCalledWith({ from: 1, direction: 'next', sort: 'created' });
    expect(shownPosition()).toBe('3');
  });

  it('resets the cursor and refetches when the order switches to note type', async () => {
    await mountPreview(21);
    el('preview-next')!.click();
    await flush();
    expect(shownPosition()).toBe('2');

    await chooseOption('preview-sort', 'Note type');

    expect(shownPosition()).toBe('1');
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
    expect(el('preview-jump')).toBeNull();
  });

  it('shows a retry action when the page cannot be loaded', async () => {
    mountAtRoute();
    const spy = vi
      .spyOn(apiClient, 'deckPreviewCards')
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ cards: pageCards(1, 20, 1, () => 'Basic'), total: 1, page: 1, per_page: 20, sort: 'created' });
    instance = mount(DeckPreviewView, { target });
    await flush();
    expect(el('preview-failed')).not.toBeNull();

    el('preview-retry')!.click();
    await flush();
    expect(el('preview-failed')).toBeNull();
    expect(shownPosition()).toBe('1');
    expect(spy).toHaveBeenCalledTimes(2);
  });
});
